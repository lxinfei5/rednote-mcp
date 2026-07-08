package downloader

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/h2non/filetype"
	"github.com/pkg/errors"
)

// ImageDownloader 图片下载器
type ImageDownloader struct {
	savePath   string
	httpClient *http.Client
}

// NewImageDownloader 创建图片下载器
func NewImageDownloader(savePath string) *ImageDownloader {
	// 确保保存目录存在
	if err := os.MkdirAll(savePath, 0755); err != nil {
		panic(fmt.Sprintf("failed to create save path: %v", err))
	}

	return &ImageDownloader{
		savePath: savePath,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			// 自定义 DialContext 拦截指向私网/环回/链路本地地址的连接，
			// 防止通过图片 URL 或重定向发起 SSRF（含云元数据 169.254.169.254）。
			Transport: &http.Transport{
				DialContext:         safeDialContext,
				TLSHandshakeTimeout: 10 * time.Second,
			},
		},
	}
}

// isBlockedIP 判断 IP 是否属于不允许外发请求的内部网段。
func isBlockedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast()
}

// ipBlocked 是实际生效的拦截判断，默认为 isBlockedIP；单测可覆盖以放行回环 httptest。
var ipBlocked = isBlockedIP

// safeDialContext 解析目标域名，若任一解析 IP 命中内部网段则拒绝连接；
// 直接拨号已校验的 IP，避免解析-连接之间的 DNS rebinding。
func safeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}

	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, ip := range ips {
		if ipBlocked(ip.IP) {
			return nil, fmt.Errorf("refused connection to internal address: %s", ip.IP)
		}
	}

	d := &net.Dialer{Timeout: 10 * time.Second}
	return d.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
}

// DownloadImage 下载图片
// 返回本地文件路径
func (d *ImageDownloader) DownloadImage(imageURL string) (string, error) {
	// 验证URL格式
	if !d.isValidImageURL(imageURL) {
		return "", errors.New("invalid image URL format")
	}

	// 创建请求并设置请求头
	req, err := http.NewRequest("GET", imageURL, nil)
	if err != nil {
		return "", errors.Wrap(err, "failed to create request")
	}

	// 设置 User-Agent，模拟浏览器请求
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	// 设置 Referer，使用图片 URL 的域名
	parsedURL, _ := url.Parse(imageURL)
	if parsedURL != nil {
		req.Header.Set("Referer", fmt.Sprintf("%s://%s/", parsedURL.Scheme, parsedURL.Host))
	}

	// 下载图片数据
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return "", errors.Wrapf(err, "failed to download image from %s", imageURL)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed with status %d for URL: %s", resp.StatusCode, imageURL)
	}

	// 读取图片数据
	imageData, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", errors.Wrap(err, "failed to read image data")
	}

	// 检测图片格式
	kind, err := filetype.Match(imageData)
	if err != nil {
		return "", errors.Wrap(err, "failed to detect file type")
	}

	if !filetype.IsImage(imageData) {
		return "", errors.New("downloaded file is not a valid image")
	}

	// 生成唯一文件名
	fileName := d.generateFileName(imageURL, kind.Extension)
	filePath := filepath.Join(d.savePath, fileName)

	// 如果文件已存在，直接返回路径
	if _, err := os.Stat(filePath); err == nil {
		return filePath, nil
	}

	// 保存到文件
	if err := os.WriteFile(filePath, imageData, 0644); err != nil {
		return "", errors.Wrap(err, "failed to save image")
	}

	return filePath, nil
}

// DownloadImages 批量下载图片
func (d *ImageDownloader) DownloadImages(imageURLs []string) ([]string, error) {
	var localPaths []string
	var errs []error

	for _, imageURL := range imageURLs {
		localPath, err := d.DownloadImage(imageURL)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to download %s: %w", imageURL, err))
			continue
		}
		localPaths = append(localPaths, localPath)
	}

	if len(errs) > 0 {
		return localPaths, fmt.Errorf("download errors occurred: %v", errs)
	}

	return localPaths, nil
}

// isValidImageURL 检查是否为有效的图片URL
func (d *ImageDownloader) isValidImageURL(rawURL string) bool {
	// 检查是否以http/https开头
	if !strings.HasPrefix(strings.ToLower(rawURL), "http://") &&
		!strings.HasPrefix(strings.ToLower(rawURL), "https://") {
		return false
	}

	// 检查URL格式
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return false
	}

	return parsedURL.Scheme != "" && parsedURL.Host != ""
}

// generateFileName 生成唯一的文件名
func (d *ImageDownloader) generateFileName(imageURL, extension string) string {
	// 使用URL的SHA256哈希作为文件名，确保唯一性
	hash := sha256.Sum256([]byte(imageURL))
	hashStr := fmt.Sprintf("%x", hash)

	// 取前16位哈希值作为文件名
	shortHash := hashStr[:16]

	// 添加时间戳确保更好的唯一性
	timestamp := time.Now().Unix()

	return fmt.Sprintf("img_%s_%d.%s", shortHash, timestamp, extension)
}

// IsImageURL 判断字符串是否为图片URL
func IsImageURL(path string) bool {
	return strings.HasPrefix(strings.ToLower(path), "http://") ||
		strings.HasPrefix(strings.ToLower(path), "https://")
}
