package main

import (
	"context"
	"flag"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/xpzouying/xiaohongshu-mcp/browser"
	"github.com/xpzouying/xiaohongshu-mcp/configs"
	"github.com/xpzouying/xiaohongshu-mcp/cookies"
	"github.com/xpzouying/xiaohongshu-mcp/xiaohongshu"
)

// 扫码登录（有头）：登录成功后把 cookie 落盘到 cookies.json，供常驻服务复用。
//
// 未登录是正常的初始状态：用 FetchQRCodeImage 区分「已登录/未登录」，
// 未登录时在窗口展示二维码等待扫码，成功后写 cookie。
func main() {
	flag.Parse()

	store := cookies.NewCookieStore(cookies.GetCookiesFilePath())

	b, err := browser.NewBrowser(false,
		browser.WithFingerprintSeed(configs.ResolveFingerprintSeed(store)),
		browser.WithProxy(configs.ProxyFromEnv()),
	)
	if err != nil {
		logrus.Fatalf("启动 Camoufox 失败: %v", err)
	}
	defer b.Close()

	page, err := b.NewPage()
	if err != nil {
		logrus.Fatalf("新建页面失败: %v", err)
	}
	defer page.Close()

	action := xiaohongshu.NewLogin(page)

	// 已登录则直接结束；未登录则拿到二维码图片。
	img, loggedIn, err := action.FetchQRCodeImage(context.Background())
	if err != nil {
		logrus.Fatalf("获取登录状态或二维码失败: %v", err)
	}
	if loggedIn {
		logrus.Info("当前已登录，无需重新扫码")
		return
	}
	logrus.Infof("未登录，浏览器窗口已展示二维码，请用小红书 App 扫码（qr src: %s）", img)

	// 等待扫码成功（最长 4 分钟）
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	if !action.WaitForLogin(ctx) {
		logrus.Fatalf("扫码超时或未检测到登录")
	}

	// 登录成功，导出 cookie 落盘
	data, err := b.Cookies()
	if err != nil {
		logrus.Fatalf("读取 cookies 失败: %v", err)
	}
	if err := store.SaveCookies(data); err != nil {
		logrus.Fatalf("保存 cookies 失败: %v", err)
	}

	// 读取当前账号信息，确认登录成功
	if user, err := action.CurrentUser(context.Background()); err == nil {
		logrus.Infof("登录成功！当前账号: %s (%s)，cookies 已保存", user.Nickname, user.UserID)
	} else {
		logrus.Warnf("登录成功但读取账号信息失败: %v", err)
		logrus.Info("登录成功，cookies 已保存")
	}
}
