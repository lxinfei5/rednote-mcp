package configs

import (
	"os"
	"path/filepath"
	"runtime"
)

var (
	useHeadless = false

	binPath = ""

	userDataDir = ""
)

func InitHeadless(h bool) {
	useHeadless = h
}

// IsHeadless 是否无头模式。
func IsHeadless() bool {
	return useHeadless
}

func SetBinPath(b string) {
	binPath = b
}

func GetBinPath() string {
	if binPath != "" {
		return binPath
	}
	return defaultChromeBinPath()
}

func SetUserDataDir(dir string) {
	userDataDir = dir
}

func GetUserDataDir() string {
	if userDataDir != "" {
		return userDataDir
	}
	return defaultUserDataDir()
}

func defaultChromeBinPath() string {
	if runtime.GOOS == "darwin" {
		if p := "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"; fileExists(p) {
			return p
		}
	}
	return ""
}

func defaultUserDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".xiaohongshu-mcp", "chrome-profile")
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
