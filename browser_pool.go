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

// WarmupSharedBrowser 预启动常驻浏览器实例并自动打开小红书首页。
// 有头模式下，启动后自动导航到小红书登录页，方便用户扫码登录；
// 无头模式下不自动打开页面，仅做健康检查。
func WarmupSharedBrowser() {
	go func() {
		poolMu.Lock()
		if sharedConn != nil {
			poolMu.Unlock()
			return
		}

		logrus.Infof("预热常驻浏览器实例 (headless=%v)...", configs.IsHeadless())
		b := getBrowserLocked()
		if err := healthCheckLocked(b); err != nil {
			logrus.Warnf("浏览器预热探活失败: %v，将在首次使用时重建", err)
			closeSharedBrowserLocked()
			poolMu.Unlock()
			return
		}

		if !configs.IsHeadless() {
			wp := b.NewPage()
			warmupPage = wp
			poolMu.Unlock()

			logrus.Infof("自动打开小红书首页，请扫码登录...")
			go func() {
				defer func() {
					if r := recover(); r != nil {
						logrus.Warnf("自动打开小红书页面时出错（不影响使用）: %v", r)
					}
				}()
				wp.Timeout(15 * time.Second).MustNavigate("https://www.xiaohongshu.com/explore")
				logrus.Infof("小红书首页已打开，请在浏览器窗口中扫码登录")
			}()
			return
		}

		poolMu.Unlock()
		logrus.Infof("常驻浏览器实例预热完成")
	}()
}
