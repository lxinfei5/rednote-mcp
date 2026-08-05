package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sirupsen/logrus"
)

// AppServer 应用服务器结构体，封装所有服务和处理器
type AppServer struct {
	xiaohongshuService ReadService
	mcpServer          *mcp.Server
	router             *gin.Engine
	httpServer         *http.Server
	closeResources     func()
}

// NewAppServer 创建新的应用服务器实例
func NewAppServer(xiaohongshuService ReadService, closeResources func()) *AppServer {
	if closeResources == nil {
		closeResources = func() {}
	}

	appServer := &AppServer{
		xiaohongshuService: xiaohongshuService,
		closeResources:     closeResources,
	}

	// 初始化 MCP Server（需要在创建 appServer 之后，因为工具注册需要访问 appServer）
	appServer.mcpServer = InitMCPServer(appServer)

	return appServer
}

// Start 启动服务器
func (s *AppServer) Start(port string) error {
	defer s.closeResources()

	s.router = setupRoutes(s)

	s.httpServer = &http.Server{
		Addr:    port,
		Handler: s.router,
	}

	serverErr := make(chan error, 1)
	go func() {
		logrus.Infof("启动 HTTP 服务器: %s", port)
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	// 等待中断信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(quit)

	select {
	case err := <-serverErr:
		return fmt.Errorf("http server failed: %w", err)
	case <-quit:
	}

	logrus.Infof("正在关闭服务器...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.httpServer.Shutdown(ctx); err != nil {
		logrus.Warnf("等待连接关闭超时，强制退出: %v", err)
		return fmt.Errorf("shutdown http server failed: %w", err)
	} else {
		logrus.Infof("服务器已优雅关闭")
	}

	return nil
}
