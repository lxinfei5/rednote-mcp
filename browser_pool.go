package main

import (
	"fmt"
	"sync"

	"github.com/go-rod/rod"
	"github.com/sirupsen/logrus"
	"github.com/xpzouying/headless_browser"
	"github.com/xpzouying/xiaohongshu-mcp/configs"
)

// browserPool 维护一个进程级常驻 browser，避免每次操作都 spawn 一个全新的
// Chrome 进程（在非 headless 模式下，每次新进程的窗口都会抢夺 macOS 前台焦点）。
//
// 行为：首次需要浏览器时懒加载 spawn 一次，之后所有调用复用同一个 browser，
// 每次只在它内部 NewPage() 开一个 tab，用完关闭 tab，browser 自身常驻直到服务关闭。
// 这样 Chrome 只在启动时弹一次窗，后续搜索不再触发应用前台切换。
//
// 并发：headless_browser/rod 的 NewPage 不是并发安全的，多请求并发时通过
// poolMu 串行化 page 的获取与关闭，避免并发开 tab 冲突。代价是浏览器操作串行化，
// 对单 agent 串行调用无影响。
var (
	poolMu     sync.Mutex
	sharedConn *headless_browser.Browser
)

// getBrowserLocked 返回常驻 browser 单例，必要时懒加载启动。
// 调用约定：调用方必须已持有 poolMu。返回的 browser 健康性由调用方探活。
func getBrowserLocked() *headless_browser.Browser {
	if sharedConn != nil {
		return sharedConn
	}

	logrus.Infof("启动常驻浏览器实例 (headless=%v)...", configs.IsHeadless())
	sharedConn = newBrowser()
	logrus.Infof("常驻浏览器实例已就绪")
	return sharedConn
}

// healthCheckLocked 确认 browser 底层连接存活：开一个 page 再关掉。
// headless_browser 不导出内部 *rod.Browser，只能间接探活。
// 若连接已断，NewPage 会 panic；用 recover 兜底转成 error。
func healthCheckLocked(b *headless_browser.Browser) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("browser 健康检查失败: %v", r)
		}
	}()
	page := b.NewPage()
	page.Close()
	return nil
}

// closeSharedBrowserLocked 关闭常驻 browser 并清空引用。调用方必须已持有 poolMu。
func closeSharedBrowserLocked() {
	if sharedConn != nil {
		sharedConn.Close()
		sharedConn = nil
		logrus.Infof("常驻浏览器实例已关闭")
	}
}

// withSharedPage 在常驻 browser 中开一个新 tab 执行 fn，结束后关闭 tab。
// poolMu 保证同一时刻只有一个请求在操作常驻 browser（串行化）。
// 若常驻 browser 已断开（外部被杀），则自愈：关闭重建一次后再试。
func withSharedPage(fn func(page *rod.Page) error) error {
	poolMu.Lock()
	defer poolMu.Unlock()

	b := getBrowserLocked()

	// 首次或重试：探活，失败则清理重建一次（自愈）。
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

// closeSharedBrowser 关闭常驻 browser（服务优雅关闭时调用），避免僵尸进程。
func closeSharedBrowser() {
	poolMu.Lock()
	defer poolMu.Unlock()
	closeSharedBrowserLocked()
}
