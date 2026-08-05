// 打开一个有头 Camoufox 浏览器（带登录态），供人眼检查页面/入口/登录态。
// 复用生产路径：登录 cookie + 固定指纹 seed + 有头。保持窗口打开，按 Enter 或 Ctrl+C 退出。
// 调试工具：排查登录态、检查页面入口、人工复核 UI 时使用。
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/playwright-community/playwright-go"
	"github.com/sirupsen/logrus"
	"github.com/xpzouying/xiaohongshu-mcp/browser"
	"github.com/xpzouying/xiaohongshu-mcp/configs"
	"github.com/xpzouying/xiaohongshu-mcp/cookies"
)

func main() {
	url := flag.String("url", "https://www.xiaohongshu.com/explore", "初始 URL")
	flag.Parse()

	store := cookies.NewCookieStore(cookies.GetCookiesFilePath())
	b, err := browser.NewBrowser(false, // 第一个参数是 headless；false = 有头（否则窗口不会出现）
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

	if _, err := page.Goto(*url, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(60_000),
	}); err != nil {
		logrus.Warnf("导航失败（浏览器仍可用）: %v", err)
	}
	// 把标签页带到最前（浏览器窗口内部），并确保窗口已显示
	_ = page.BringToFront()

	logrus.Infof("已打开带登录态的浏览器: %s", *url)
	logrus.Infof("窗口应已弹出。退出方式：交互终端按 Enter；后台运行请发 Ctrl+C 或让我关闭。")

	// 真实 Enter 才退出；stdin 关闭（后台运行）时不得因此退出——那是 EOF，不是用户输入。
	done := make(chan struct{})
	go func() {
		r := bufio.NewReader(os.Stdin)
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				if err == io.EOF {
					return // 无 stdin：退化为只等信号
				}
				return
			}
			if line == "\n" {
				close(done)
				return
			}
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-done:
		fmt.Println("\n退出")
	case <-sig:
		fmt.Println("\n退出")
	}
}
