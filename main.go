package main

import (
	"flag"
	"os"

	"github.com/sirupsen/logrus"
	"github.com/xpzouying/xiaohongshu-mcp/configs"
)

func main() {
	var (
		headless bool
		binPath  string // 浏览器二进制文件路径
		port     string
	)
	flag.BoolVar(&headless, "headless", true, "是否无头模式")
	flag.StringVar(&binPath, "bin", "", "浏览器二进制文件路径")
	// 默认仅绑定回环地址，避免把可代用户执行写操作的服务暴露到局域网；
	// 需要全网卡监听（如 Docker 端口映射）时显式传 --port :18060。
	flag.StringVar(&port, "port", "127.0.0.1:18060", "监听地址，如 127.0.0.1:18060 或 :18060")
	flag.Parse()

	if len(binPath) == 0 {
		binPath = os.Getenv("ROD_BROWSER_BIN")
	}
	if binPath != "" {
		logrus.Infof("using browser binary: %s", binPath)
	} else {
		logrus.Infof("browser binary is not configured; rod will auto-detect or download Chromium")
	}

	configs.InitHeadless(headless)
	configs.SetBinPath(binPath)

	// 初始化服务
	xiaohongshuService := NewXiaohongshuService()

	// 创建并启动应用服务器
	appServer := NewAppServer(xiaohongshuService)
	if err := appServer.Start(port); err != nil {
		logrus.Fatalf("failed to run server: %v", err)
	}
}
