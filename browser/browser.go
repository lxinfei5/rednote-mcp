package browser

import (
	"encoding/json"
	"net/url"
	"os"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"github.com/go-rod/stealth"
	"github.com/sirupsen/logrus"
	"github.com/xpzouying/xiaohongshu-mcp/cookies"
)

// Browser 封装一个常驻的 rod 浏览器实例及其启动器。
// 启动 flag 在此处直接用 rod 的 launcher 组装，不再依赖外部封装，
// 以便精确控制自动化痕迹（详见 NewBrowser）。
type Browser struct {
	browser  *rod.Browser
	launcher *launcher.Launcher
}

type browserConfig struct {
	binPath string
}

type Option func(*browserConfig)

func WithBinPath(binPath string) Option {
	return func(c *browserConfig) {
		c.binPath = binPath
	}
}

// maskProxyCredentials masks username and password in proxy URL for safe logging.
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
//
// 启动配置刻意贴近真实 Chrome，降低被风控识别为自动化的概率：
//
//   - HeadlessNew 旧版 --headless 的指纹与正常 Chrome 差异显著，这里改用
//     Chrome 的新版无头 --headless=new，指纹更接近正常有头浏览器。
//     headless=false（如扫码登录需要可见窗口）时该 flag 会被移除，正常有头运行。
//   - Delete("enable-automation") rod launcher 默认会带上 --enable-automation，
//     它会注入 navigator.webdriver 等明显的自动化痕迹，这里显式删除。
//   - 不再 Set("--no-sandbox")：原封装无条件加了该 flag，而它是容器/自动化环境
//     的强信号。改为交给 rod 的容器检测——仅在被检测到运行在容器内时才自动添加，
//     Docker 环境仍正常，本机/常规环境不再带此 flag。
//   - 不设置 user-agent：让本机真实 Chrome 报其版本正确、平台真实的原生 UA，
//     避免写死的 UA（如 Chrome/124）随真实版本升级而失配。--headless=new 模式下
//     UA 中不会出现 HeadlessChrome 字样，无需篡改。
//
// stealth 反检测 JS 注入（navigator.webdriver→undefined、plugins、languages 等）
// 在 NewPage 中保留。
func NewBrowser(headless bool, options ...Option) *Browser {
	cfg := &browserConfig{}
	for _, opt := range options {
		opt(cfg)
	}

	l := launcher.New().
		HeadlessNew(headless).    // --headless=new，指纹更接近正常浏览器
		Delete("enable-automation") // 去掉自动化痕迹

	// 指定 Chrome 二进制路径（未指定则 rod 自动检测或下载 Chromium）
	if cfg.binPath != "" {
		l = l.Bin(cfg.binPath)
	}

	// 从环境变量读取代理
	if proxy := os.Getenv("XHS_PROXY"); proxy != "" {
		l = l.Proxy(proxy)
		logrus.Infof("Using proxy: %s", maskProxyCredentials(proxy))
	}

	url := l.MustLaunch()

	browser := rod.New().
		ControlURL(url).
		MustConnect()

	// 加载 cookies
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
		logrus.Warnf("failed to load cookies: %v", err)
	}

	return &Browser{
		browser:  browser,
		launcher: l,
	}
}

// NewPage 创建一个带 stealth 反检测注入的页面（puppeteer-extra-stealth 全套：
// navigator.webdriver→undefined、plugins、languages 等）。
func (b *Browser) NewPage() *rod.Page {
	return stealth.MustPage(b.browser)
}

// Close 关闭浏览器并清理启动器资源。
func (b *Browser) Close() {
	b.browser.MustClose()
	b.launcher.Cleanup()
}
