package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/sirupsen/logrus"
	"github.com/xpzouying/xiaohongshu-mcp/browser"
	"github.com/xpzouying/xiaohongshu-mcp/configs"
)

const (
	closeBrowserTimeout  = 5 * time.Second
	tabTimeoutRead       = 30 * time.Second
	tabTimeoutReadLong   = 3 * time.Minute
	tabTimeoutWrite      = 5 * time.Minute
	tabTimeoutWriteVideo = 10 * time.Minute

	// openPageTimeout 兜住"开 tab"阶段本身。历史 bug：openPage()/NewPageSafe() 在硬超时 timer 之外、
	// 且持 lifecycleMu 同步执行——一旦 Chrome/Rod 在开页时卡死，会无限挂起并连带冻结整个 pool（含本该自愈
	// 的重建路径）。这个界把"无限挂起"降级为"有界失败+重建"，直接满足"不能卡死"。
	openPageTimeout = 15 * time.Second

	// 连续超时阈值：达到后强制重建浏览器，避免挂起拖死服务
	consecutiveTimeoutThreshold = 2
)

// Pacing / concurrency — env-tunable so the operator can dial 抓取节奏 WITHOUT rebuilding the binary.
// This is "slow down, but NEVER stop": there is no cooldown/budget/gate anywhere — a walled note is a
// per-note skip, never a global stop (per user decision 2026-07-08: the wall is transient/per-post).
//
//	XHS_MAX_CONCURRENT_TABS  default 1   — serialize one tab at a time on the single 子账号. Clamped to
//	                                       >=1 (0 would deadlock the unbuffered semaphore).
//	XHS_MIN_GAP_MS           default 800 — base floor between op-STARTS.
//	XHS_GAP_JITTER_MS        default 800 — uniform random added to the floor, so the cadence is
//	                                       non-deterministic (a fixed metronome is itself a bot signal).
//
// => successive read ops start ~0.8–1.6s apart. Retune live via env, no rebuild.
var (
	maxConcurrentTabs = atLeast(envInt("XHS_MAX_CONCURRENT_TABS", 1), 1)
	minInterOpGap     = time.Duration(envInt("XHS_MIN_GAP_MS", 800)) * time.Millisecond
	gapJitter         = time.Duration(envInt("XHS_GAP_JITTER_MS", 800)) * time.Millisecond
)

// envInt reads a non-negative int from env, falling back to def on missing/blank/invalid/negative.
func envInt(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		logrus.Warnf("env %s=%q invalid (want non-negative int); using default %d", key, v, def)
		return def
	}
	return n
}

func atLeast(n, lo int) int {
	if n < lo {
		return lo
	}
	return n
}

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

	// 连续超时计数（受 lifecycleMu 保护），用于触发 browser 重建
	consecutiveTimeouts int
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
	// Randomized target = base floor + uniform jitter, so op-starts are both slower AND non-deterministic.
	target := minInterOpGap
	if gapJitter > 0 {
		target += time.Duration(rand.Int63n(int64(gapJitter) + 1))
	}
	if gap := time.Since(p.lastPageOp); gap < target {
		time.Sleep(target - gap)
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
			err = fmt.Errorf("browser health check failed: %v", r)
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

// noteHardTimeout 记录一次硬超时；连续达到阈值则立即销毁当前 browser 并标记重建。
// 必须在持有生命周期锁的场景或内部自行加锁。
func (p *browserPool) noteHardTimeout() {
	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()

	p.consecutiveTimeouts++
	logrus.Warnf("连续 tab 超时计数: %d/%d", p.consecutiveTimeouts, consecutiveTimeoutThreshold)

	if p.consecutiveTimeouts >= consecutiveTimeoutThreshold {
		logrus.Warnf("连续超时达到阈值，强制重建常驻浏览器以恢复服务")
		p.closeLongLivedPages()
		p.closeConnLocked(p.conn)
		p.conn = nil
		p.gen++
		p.consecutiveTimeouts = 0
	}
}

// noteSuccess 操作成功，重置连续超时计数。
func (p *browserPool) noteSuccess() {
	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()
	if p.consecutiveTimeouts > 0 {
		p.consecutiveTimeouts = 0
	}
}

// callWithTimeout 在独立 goroutine 里跑 fn 并用 timer 兜底：即便 fn（如卡死的 Chrome 开页）永不返回，
// 调用方也必定在 timeout 后拿到 ErrTabTimeout 返回。卡住的 goroutine 被丢弃——它不持有任何 pool 锁，
// 晚到的 page 会随后续 browser 重建一起被销毁，不会泄漏进 pool。fn 内的 Must* panic 被 recover 成 error。
func callWithTimeout(timeout time.Duration, fn func() (*rod.Page, error)) (*rod.Page, error) {
	type res struct {
		page *rod.Page
		err  error
	}
	ch := make(chan res, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- res{nil, fmt.Errorf("page-open panic: %v", r)}
			}
		}()
		pg, e := fn()
		ch <- res{pg, e}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case r := <-ch:
		return r.page, r.err
	case <-timer.C:
		return nil, fmt.Errorf("%w: page-open exceeded %v", ErrTabTimeout, timeout)
	}
}

// newPageSafeBounded 给 b.NewPageSafe() 套上 openPageTimeout 硬界，杜绝开页阶段无限挂起。
func newPageSafeBounded(b *browser.Browser) (*rod.Page, error) {
	return callWithTimeout(openPageTimeout, func() (*rod.Page, error) {
		return b.NewPageSafe()
	})
}

func (p *browserPool) openPage() (page *rod.Page, err error) {
	p.lifecycleMu.Lock()
	defer p.lifecycleMu.Unlock()

	b, err := p.ensureBrowserLocked()
	if err != nil {
		return nil, err
	}

	page, err = newPageSafeBounded(b)
	if err != nil {
		// 死连接(EOF/session lost) 或 开页硬超时(browser 卡死) 都视为 browser 不健康：重建一次再试。
		// 关键：newPageSafeBounded 保证这里必定有界返回，重建路径不会被一个永不返回的 NewPageSafe 堵死。
		if !isConnDeadErr(err) && !isTabTimeoutErr(err) {
			return nil, err
		}
		logrus.Warnf("打开页面失败/超时，尝试重建常驻浏览器: %v", err)
		p.closeLongLivedPages()
		p.closeConnLocked(p.conn)
		p.conn = nil
		p.gen++

		b, err = p.ensureBrowserLocked()
		if err != nil {
			return nil, err
		}
		page, err = newPageSafeBounded(b)
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

// withSharedPageCtx 在常驻 browser 上开 tab 执行 fn；最多 2 个 tab 并发，fn 在窄锁外执行。
// 多层硬超时保护：
// - 软超时：context 传给 page（部分非 Must* 有效）
// - 硬超时：goroutine + timer 兜底，Must* 卡死也必定返回 ErrTabTimeout
// - 连续超时达到阈值后，强制重建 browser
func withSharedPageCtx(ctx context.Context, class tabTimeoutClass, fn func(page *rod.Page) error) error {
	if ctx == nil {
		ctx = context.Background()
	}

	waitMs, slotErr := sharedPool.waitForSlot(ctx)
	if slotErr != nil {
		return slotErr
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

	page, openErr := sharedPool.openPage()
	if openErr != nil {
		return openErr
	}
	defer func() {
		page.Close()
		sharedPool.decInflight()
	}()

	tabTimeout := timeoutFor(class)

	// 结果通道：goroutine 内执行 fn
	type execResult struct {
		err error
	}
	resultCh := make(chan execResult, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				// Must* API panic 转 error 返回
				resultCh <- execResult{err: fmt.Errorf("rod operation panic: %v", r)}
			}
		}()
		// 仍尝试传 context（对非 Must 路径有帮助）
		opCtx, cancel := context.WithTimeout(ctx, tabTimeout)
		defer cancel()
		resultCh <- execResult{err: fn(page.Context(opCtx))}
	}()

	start := time.Now()

	// 硬超时层：timer 保证必定返回，不依赖 Rod 是否尊重 context
	timer := time.NewTimer(tabTimeout)
	defer timer.Stop()

	var execErr error
	select {
	case res := <-resultCh:
		execErr = res.err
	case <-timer.C:
		// 硬超时：立即关闭卡住的 page，强制让调用方返回
		logrus.WithField("timeout_class", class).Warnf("硬超时：强制关闭卡住的 tab (>%v)", tabTimeout)
		_ = page.Close() // 触发内部等待解除，后续 Must* 会失败/ panic（由 recover 处理）
		// 尝试快速收割晚到的结果，避免 goroutine 永远挂，但不阻塞主流程
		go func() {
			select {
			case <-resultCh:
			case <-time.After(1500 * time.Millisecond):
			}
		}()
		execErr = fmt.Errorf("%w: hard-timeout after %v", ErrTabTimeout, tabTimeout)
	}

	tabMs := time.Since(start).Milliseconds()

	fields := logrus.Fields{
		"wait_slot_ms":    waitMs.Milliseconds(),
		"tab_duration_ms": tabMs,
		"timeout_class":   class,
	}

	if execErr != nil {
		if isTabTimeoutErr(execErr) {
			logrus.WithFields(fields).Warnf("tab 操作超时(%v)", tabTimeout)
			sharedPool.noteHardTimeout()
			return fmt.Errorf("%w: exceeded %v", ErrTabTimeout, tabTimeout)
		}
		logrus.WithFields(fields).Debugf("tab 操作失败: %v", execErr)
		return execErr
	}

	logrus.WithFields(fields).Debug("tab 操作完成")
	sharedPool.noteSuccess()
	return nil
}

// isTabTimeoutErr 判断是否为 tab 超时类错误
func isTabTimeoutErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrTabTimeout) {
		return true
	}
	s := err.Error()
	// 硬超时我们自己产生的包装
	return strings.Contains(s, "hard-timeout")
}

// acquireLongLivedPage 开 long-lived tab（扫码登录等），不占 2 槽位并发额度。
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
