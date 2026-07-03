package main

import (
	"net"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// isLoopbackHost 判断 host（可带端口）是否为本机回环地址。
func isLoopbackHost(host string) bool {
	if host == "" {
		return false
	}
	h := host
	if hostOnly, _, err := net.SplitHostPort(host); err == nil {
		h = hostOnly
	}
	h = trimIPv6Brackets(h)
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func trimIPv6Brackets(h string) string {
	if len(h) >= 2 && h[0] == '[' && h[len(h)-1] == ']' {
		return h[1 : len(h)-1]
	}
	return h
}

// originAllowed 仅放行本机回环来源的 Origin；空 Origin（MCP/CLI 等非浏览器客户端）放行。
func originAllowed(origin string) bool {
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return isLoopbackHost(u.Host)
}

// securityMiddleware 防止本机上恶意网页跨源驱动写操作（CSRF）与 DNS rebinding：
//   - 仅允许回环 Host 访问（拦截 DNS rebinding）
//   - 拒绝非本机来源的浏览器请求（浏览器跨源请求必带 Origin，即使 text/plain 无预检也带）
//   - CORS 只回显被允许的本机 Origin，不再使用通配符
func securityMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !isLoopbackHost(c.Request.Host) {
			logrus.Warnf("拒绝非回环 Host 请求: host=%s path=%s", c.Request.Host, c.Request.URL.Path)
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden host"})
			return
		}

		origin := c.GetHeader("Origin")
		if !originAllowed(origin) {
			logrus.Warnf("拒绝跨源请求: origin=%s path=%s", origin, c.Request.URL.Path)
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "cross-origin request forbidden"})
			return
		}

		if origin != "" {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")
		}

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

// errorHandlingMiddleware 错误处理中间件
func errorHandlingMiddleware() gin.HandlerFunc {
	return gin.CustomRecovery(func(c *gin.Context, recovered any) {
		logrus.Errorf("服务器内部错误: %v, path: %s", recovered, c.Request.URL.Path)

		respondError(c, http.StatusInternalServerError, "INTERNAL_ERROR",
			"服务器内部错误", recovered)
	})
}
