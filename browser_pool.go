package main

import (
	"fmt"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/sirupsen/logrus"
	"github.com/xpzouying/xiaohongshu-mcp/browser"
	"github.com/xpzouying/xiaohongshu-mcp/configs"
)

const minInterOpGap = 800 * time.Millisecond

var (
	poolMu     sync.Mutex
	sharedConn *browser.Browser
	warmupPage *rod.Page
	lastPageOp time.Time
)

func getBrowserLocked() *browser.Browser {
	if sharedConn != nil {
		return sharedConn
	}

	logrus.Infof("启动常驻浏览器实例 (headless=%v)...", configs.IsHeadless())
	sharedConn = newBrowser()
	logrus.Infof("常驻浏览器实例已就绪")
	return sharedConn
}

func healthCheckLocked(b *browser.Browser) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("browser 健康检查失败: %v", r)
		}
	}()
	page := b.NewPage()
	page.Close()
	return nil
}

func closeSharedBrowserLocked() {
	if warmupPage != nil {
		warmupPage.Close()
		warmupPage = nil
	}
	if sharedConn != nil {
		sharedConn.Close()
		sharedConn = nil
		logrus.Infof("常驻浏览器实例已关闭")
	}
}

func withSharedPage(fn func(page *rod.Page) error) error {
	poolMu.Lock()
	defer poolMu.Unlock()

	if gap := time.Since(lastPageOp); gap < minInterOpGap {
		time.Sleep(minInterOpGap - gap)
	}
	defer func() { lastPageOp = time.Now() }()

	b := getBrowserLocked()

	if err := healthCheckLocked(b); err != nil {
		logrus.Warnf("常驻浏览器不可用，尝试重建: %v", err)
		closeSharedBrowserLocked()
		b = getBrowserLocked()
		if err := healthCheckLocked(b); err != nil {
			closeSharedBrowserLocked()
			return err
		}
	}

	page := b.NewPage()
	defer page.Close()

	return fn(page)
}

func closeSharedBrowser() {
	poolMu.Lock()
	defer poolMu.Unlock()
	closeSharedBrowserLocked()
}

// newSharedBrowserPage 在常驻 browser 上打开一个由调用方管理生命周期的 page，
// 用于需要长时间保留页面的流程（如扫码登录等待）。复用常驻 Chrome，不再另起
// 第二个进程争用同一 profile（Chrome 单例锁会导致第二实例启动失败）。
// 调用方负责在用完后 page.Close()。
func newSharedBrowserPage() (page *rod.Page, err error) {
	poolMu.Lock()
	defer poolMu.Unlock()
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("打开浏览器页面失败: %v", r)
			page = nil
		}
	}()

	b := getBrowserLocked()
	if e := healthCheckLocked(b); e != nil {
		logrus.Warnf("常驻浏览器不可用，尝试重建: %v", e)
		closeSharedBrowserLocked()
		b = getBrowserLocked()
	}
	page = b.NewPage()
	return page, nil
}

// WarmupSharedBrowser 预启动常驻浏览器实例并自动打开小红书首页。
// 有头模式下，启动后自动导航到小红书登录页，方便用户扫码登录；
// 无头模式下不自动打开页面，仅做健康检查。
func WarmupSharedBrowser() {
	go func() {
		wp := warmupBrowserLocked()
		if wp == nil {
			return
		}

		// 导航在锁外进行，避免长时间持锁；使用局部 page 句柄，失败（含被并发关闭）
		// 时 recover 兜底，不影响后续使用。
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

// warmupBrowserLocked 在持锁下预热常驻浏览器：有头模式返回待导航的 warmupPage，否则 nil。
// 任何 panic（Chrome 启动失败、profile 被占用等）都被 recover 并降级为 warn，
// 不会让后台 goroutine 崩溃整个进程；锁通过 defer 保证释放。
func warmupBrowserLocked() (wp *rod.Page) {
	poolMu.Lock()
	defer poolMu.Unlock()
	defer func() {
		if r := recover(); r != nil {
			logrus.Warnf("浏览器预热失败: %v，将在首次使用时重建", r)
			closeSharedBrowserLocked()
			wp = nil
		}
	}()

	if sharedConn != nil {
		return nil
	}

	logrus.Infof("预热常驻浏览器实例 (headless=%v)...", configs.IsHeadless())
	b := getBrowserLocked()
	if err := healthCheckLocked(b); err != nil {
		logrus.Warnf("浏览器预热探活失败: %v，将在首次使用时重建", err)
		closeSharedBrowserLocked()
		return nil
	}

	if !configs.IsHeadless() {
		warmupPage = b.NewPage()
		return warmupPage
	}

	logrus.Infof("常驻浏览器实例预热完成")
	return nil
}
