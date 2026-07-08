package browser

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"github.com/go-rod/stealth"
	"github.com/sirupsen/logrus"
	"github.com/xpzouying/xiaohongshu-mcp/cookies"
)

type Browser struct {
	browser  *rod.Browser
	launcher *launcher.Launcher
}

type browserConfig struct {
	binPath     string
	userDataDir string
}

type Option func(*browserConfig)

func WithBinPath(binPath string) Option {
	return func(c *browserConfig) {
		c.binPath = binPath
	}
}

func WithUserDataDir(dir string) Option {
	return func(c *browserConfig) {
		c.userDataDir = dir
	}
}

func maskProxyCredentials(proxyURL string) string {
	u, err := url.Parse(proxyURL)
	if err != nil || u.User == nil {
		return proxyURL
	}
	if _, hasPassword := u.User.Password(); hasPassword {
		u.User = url.UserPassword("***", "***")
	} else {
		u.User = url.User("***")
	}
	return u.String()
}

// NewBrowser 启动一个浏览器实例。
func NewBrowser(headless bool, options ...Option) *Browser {
	cfg := &browserConfig{}
	for _, opt := range options {
		opt(cfg)
	}

	l := launcher.New().
		HeadlessNew(headless).
		Delete("enable-automation").
		Delete("disable-background-networking").
		Delete("disable-features").
		Delete("disable-site-isolation-trials").
		Delete("disable-breakpad").
		Delete("disable-default-apps").
		Delete("disable-sync").
		Delete("metrics-recording-only").
		Delete("enable-features").
		Delete("no-startup-window").
		Set("no-first-run", "true").
		Set("no-default-browser-check", "true").
		// 从 Blink 层关闭自动化特征，navigator.webdriver 原生保持 false，无需依赖注入
		Set("disable-blink-features", "AutomationControlled")

	if cfg.binPath != "" {
		l = l.Bin(cfg.binPath)
		logrus.Infof("using Chrome binary: %s", cfg.binPath)
	} else {
		logrus.Infof("Chrome binary not specified, rod will auto-detect or download Chromium")
	}

	if cfg.userDataDir != "" {
		// profile 目录含完整登录态，用 0700 仅属主可访问
		if err := os.MkdirAll(cfg.userDataDir, 0700); err != nil {
			logrus.Warnf("failed to create user data dir %s: %v", cfg.userDataDir, err)
		} else {
			l = l.Set("user-data-dir", cfg.userDataDir)
			logrus.Infof("using Chrome profile directory: %s", cfg.userDataDir)
		}
	}

	if proxy := os.Getenv("XHS_PROXY"); proxy != "" {
		l = l.Proxy(proxy)
		logrus.Infof("Using proxy: %s", maskProxyCredentials(proxy))
	}

	url := l.MustLaunch()

	browser := rod.New().
		ControlURL(url).
		MustConnect()

	cookiePath := cookies.GetCookiesFilePath()
	cookieLoader := cookies.NewLoadCookie(cookiePath)

	if data, err := cookieLoader.LoadCookies(); err == nil {
		var cks []*proto.NetworkCookie
		if err := json.Unmarshal([]byte(data), &cks); err != nil {
			logrus.Warnf("failed to unmarshal cookies: %v", err)
		} else {
			browser.MustSetCookies(cks...)
			logrus.Debugf("loaded cookies from file successfully")
		}
	} else {
		logrus.Debugf("no legacy cookie file loaded (using Chrome profile cookies)")
	}

	return &Browser{
		browser:  browser,
		launcher: l,
	}
}

func (b *Browser) NewPage() *rod.Page {
	return stealth.MustPage(b.browser)
}

// NewPageSafe 开 tab，连接异常时返回 error 而非 panic。
func (b *Browser) NewPageSafe() (page *rod.Page, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("failed to open page: %v", r)
			page = nil
		}
	}()
	return stealth.Page(b.browser)
}

func (b *Browser) Close() {
	b.CloseWithTimeout(5 * time.Second)
}

// CloseWithTimeout 关闭浏览器；超时后 Kill 进程，避免死连接永久阻塞。
// 不调用 launcher.Cleanup()，防止误删持久化 Chrome profile。
func (b *Browser) CloseWithTimeout(timeout time.Duration) {
	if b == nil {
		return
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if b.browser != nil {
			_ = b.browser.Close()
		}
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		logrus.Warnf("browser close timeout after %v, killing process", timeout)
	}
	if b.launcher != nil {
		b.launcher.Kill()
	}
}
