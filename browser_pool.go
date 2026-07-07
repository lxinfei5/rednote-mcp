package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/sirupsen/logrus"
	"github.com/xpzouying/xiaohongshu-mcp/browser"
	"github.com/xpzouying/xiaohongshu-mcp/configs"
)

const (
	maxConcurrentTabs    = 3
	minInterOpGap        = 800 * time.Millisecond
	closeBrowserTimeout  = 5 * time.Second
	tabTimeoutRead       = 30 * time.Second
	tabTimeoutReadLong   = 3 * time.Minute
	tabTimeoutWrite      = 5 * time.Minute
	tabTimeoutWriteVideo = 10 * time.Minute
)

// slotWaitTimeout 等槽位最长时间，测试可临时改小。
var slotWaitTimeout = 60 * time.Second

var (
	ErrPoolBusy           = errors.New("browser pool busy")
	ErrTabTimeout         = errors.New("tab operation timeout")
	ErrBrowserUnavailable = errors.New("browser unavailable")
)

type tabTimeoutClass int

const (
	tabRead tabTimeoutClass = iota
	tabReadLong
	tabWrite
	tabWriteVideo
)

func timeoutFor(class tabTimeoutClass) time.Duration {
	switch class {
	case tabReadLong:
		return tabTimeoutReadLong
	case tabWrite:
		return tabTimeoutWrite
	case tabWriteVideo:
		return tabTimeoutWriteVideo
	default:
		return tabTimeoutRead
	}
}

type browserPool struct {
	lifecycleMu sync.Mutex
	conn        *browser.Browser
	inflight    int
	gen         uint64

	pageSem chan struct{}

	paceMu     sync.Mutex
	lastPageOp time.Time

	longLivedMu     sync.Mutex
	warmupPage      *rod.Page
	longLivedExtras []*rod.Page
}

var sharedPool = &browserPool{
	pageSem: make(chan struct{}, maxConcurrentTabs),
}

func isConnDeadErr(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "use of closed network connection") ||
		strings.Contains(s, "connection reset") ||
		strings.Contains(s, "EOF") ||
		strings.Contains(s, "Session with given id not found")
}

func (p *browserPool) waitForSlot(ctx context.Context) (time.Duration, error) {
	start := time.Now()
	timer := time.NewTimer(slotWaitTimeout)
	defer timer.Stop()

	select {
	case p.pageSem <- struct{}{}:
		return time.Since(start), nil
	case <-timer.C:
		return time.Since(start), fmt.Errorf("%w: waited %v for one of %d slots",
			ErrPoolBusy, slotWaitTimeout, maxConcurrentTabs)
	case <-ctx.Done():
		return time.Since(start), ctx.Err()
	}
}

func (p *browserPool) releaseSlot() {
	<-p.pageSem
}

func (p *browserPool) pace() {
	p.paceMu.Lock()
	defer p.paceMu.Unlock()
	if gap := time.Since(p.lastPageOp); gap < minInterOpGap {
		time.Sleep(minInterOpGap - gap)
	}
	p.lastPageOp = time.Now()
}

func (p *browserPool) ensureBrowserLocked() (*browser.Browser, error) {
	if p.conn != nil {
		return p.conn, nil
	}
	logrus.Infof("启动常驻浏览器实例 (headless=%v)...", configs.IsHeadless())
	p.conn = newBrowser()
	logrus.Infof("常驻浏览器实例已就绪")
	return p.conn, nil
}

func (p *browserPool) healthCheckLocked(b *browser.Browser) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("browser 健康检查失败: %v", r)
		}
	}()
	page, err := b.NewPageSafe()
	if err != nil {
		return err
	}
	page.Close()
	return nil
}

func (p *browserPool) registerLongLived(page *rod.Page) {
	p.longLivedMu.Lock()
	defer p.longLivedMu.Unlock()
	if page == p.warmupPage {
		return
	}
	p.longLivedExtras = append(p.longLivedExtras, page)
}

// closeLongLivedPages 关闭 warmup/扫码等 long-lived tab，返回关闭数量。调用方需已持 lifecycleMu。
func (p *browserPool) closeLongLivedPages() int {
	p.longLivedMu.Lock()
	defer p.longLivedMu.Unlock()
	closed := 0
	if p.warmupPage != nil {
		p.warmupPage.Close()
		p.warmupPage = nil
		closed++
	}
	for _, pg := range p.longLivedExtras {
		pg.Close()
		closed++
	}
	p.longLivedExtras = nil
	if closed > 0 && p.inflight >= closed {
		p.inflight -= closed
	} else if closed > 0 && p.inflight > 0 {
		p.inflight = 0
	}
	return closed
}

func (p *browserPool) closeConnLocked(b *browser.Browser) {
	if b == nil {
		return
	}
	b.CloseWithTimeout(closeBrowserTimeout)
	logrus.Infof("常驻浏览器实例已关闭")
}

func (p *browserPool) decInflight() {
	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()
	if p.inflight > 0 {
		p.inflight--
	}
}

func (p *browserPool) openPage() (page *rod.Page, err error) {
	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()

	b, err := p.ensureBrowserLocked()
	if err != nil {
		return nil, err
	}

	page, err = b.NewPageSafe()
	if err != nil {
		if !isConnDeadErr(err) {
			return nil, err
		}
		logrus.Warnf("打开页面失败，尝试重建常驻浏览器: %v", err)
		p.closeLongLivedPages()
		p.closeConnLocked(p.conn)
		p.conn = nil
		p.gen++

		b, err = p.ensureBrowserLocked()
		if err != nil {
			return nil, err
		}
		page, err = b.NewPageSafe()
		if err != nil {
			p.closeConnLocked(p.conn)
			p.conn = nil
			return nil, err
		}
	}

	p.inflight++
	return page, nil
}

func releasePageInflight() {
	sharedPool.decInflight()
}

// withSharedPageCtx 在常驻 browser 上开 tab 执行 fn；最多 3 个 tab 并发，fn 在窄锁外执行。
func withSharedPageCtx(ctx context.Context, class tabTimeoutClass, fn func(page *rod.Page) error) error {
	if ctx == nil {
		ctx = context.Background()
	}

	waitMs, err := sharedPool.waitForSlot(ctx)
	if err != nil {
		return err
	}

	released := false
	release := func() {
		if !released {
			released = true
			sharedPool.releaseSlot()
		}
	}
	defer release()

	sharedPool.pace()

	page, err := sharedPool.openPage()
	if err != nil {
		return err
	}
	defer func() {
		page.Close()
		sharedPool.decInflight()
	}()

	tabTimeout := timeoutFor(class)
	opCtx, cancel := context.WithTimeout(ctx, tabTimeout)
	defer cancel()

	start := time.Now()
	err = fn(page.Context(opCtx))
	tabMs := time.Since(start).Milliseconds()

	fields := logrus.Fields{
		"wait_slot_ms":    waitMs.Milliseconds(),
		"tab_duration_ms": tabMs,
		"timeout_class":   class,
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(opCtx.Err(), context.DeadlineExceeded) {
			logrus.WithFields(fields).Warnf("tab 操作超时(%v)", tabTimeout)
			return fmt.Errorf("%w: exceeded %v", ErrTabTimeout, tabTimeout)
		}
		logrus.WithFields(fields).Debugf("tab 操作失败: %v", err)
		return err
	}
	logrus.WithFields(fields).Debug("tab 操作完成")
	return nil
}

// acquireLongLivedPage 开 long-lived tab（扫码登录等），不占 3 槽位并发额度。
func acquireLongLivedPage(ctx context.Context) (page *rod.Page, err error) {
	if ctx == nil {
		ctx = context.Background()
	}

	waitMs, err := sharedPool.waitForSlot(ctx)
	if err != nil {
		return nil, err
	}
	// 开完 page 立即释放槽位，后台 goroutine 持有 tab 不再阻塞其他请求。
	defer sharedPool.releaseSlot()

	sharedPool.pace()

	page, err = sharedPool.openPage()
	if err != nil {
		return nil, err
	}

	// long-lived page 计入 inflight，关闭时由调用方 decInflight。
	sharedPool.registerLongLived(page)
	logrus.WithField("wait_slot_ms", waitMs.Milliseconds()).Debug("long-lived page 已创建")
	return page, nil
}

func closeSharedBrowser() {
	sharedPool.lifecycleMu.Lock()
	defer sharedPool.lifecycleMu.Unlock()
	sharedPool.closeLongLivedPages()
	sharedPool.closeConnLocked(sharedPool.conn)
	sharedPool.conn = nil
	sharedPool.gen++
}

// WarmupSharedBrowser 预启动常驻浏览器实例并自动打开小红书首页。
func WarmupSharedBrowser() {
	go func() {
		wp := warmupBrowserLocked()
		if wp == nil {
			return
		}

		defer func() {
			if r := recover(); r != nil {
				logrus.Warnf("自动打开小红书页面时出错（不影响使用）: %v", r)
			}
		}()
		logrus.Infof("自动打开小红书首页，请扫码登录...")
		wp.Timeout(15 * time.Second).MustNavigate("https://www.xiaohongshu.com/explore")
		logrus.Infof("小红书首页已打开，请在浏览器窗口中扫码登录")
	}()
}

func warmupBrowserLocked() (wp *rod.Page) {
	sharedPool.lifecycleMu.Lock()
	defer sharedPool.lifecycleMu.Unlock()
	defer func() {
		if r := recover(); r != nil {
			logrus.Warnf("浏览器预热失败: %v，将在首次使用时重建", r)
			sharedPool.closeLongLivedPages()
			sharedPool.closeConnLocked(sharedPool.conn)
			sharedPool.conn = nil
			wp = nil
		}
	}()

	if sharedPool.conn != nil {
		return nil
	}

	logrus.Infof("预热常驻浏览器实例 (headless=%v)...", configs.IsHeadless())
	b, err := sharedPool.ensureBrowserLocked()
	if err != nil {
		return nil
	}
	if err := sharedPool.healthCheckLocked(b); err != nil {
		logrus.Warnf("浏览器预热探活失败: %v，将在首次使用时重建", err)
		sharedPool.closeConnLocked(sharedPool.conn)
		sharedPool.conn = nil
		return nil
	}

	if !configs.IsHeadless() {
		page, err := b.NewPageSafe()
		if err != nil {
			return nil
		}
		sharedPool.warmupPage = page
		sharedPool.inflight++
		return page
	}

	logrus.Infof("常驻浏览器实例预热完成")
	return nil
}
