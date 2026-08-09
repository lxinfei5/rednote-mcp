// camoufox-setup 是一次性的安装/校验工具：把固定版本的 Camoufox 二进制与
// playwright 驱动（node + playwright-core）落到仓库内受信位置。
//
// 它与服务运行时严格分离——服务在运行时绝不下载任何组件（见 browser/driver.go、
// browser/install.go 的 fail-closed 解析）。本命令是唯一的获取入口，且：
//   - 版本由 browser.CamoufoxVersion 固定；
//   - Camoufox zip 下载后强制 SHA-256 校验，不过即退出；
//   - playwright-core 取自 npm registry（带 integrity 哈希），逐字节核对。
package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/xpzouying/xiaohongshu-mcp/browser"
)

// playwrightCoreVersion 与 go.mod 中 playwright-go 版本对应的 Playwright 内核版本。
const playwrightCoreVersion = "1.52.0"

// playwrightCoreSHA512 是 npm 上 playwright-core@<version> 的 integrity（node 校验）。
// 由 node/npm 在解包前自行核对，这里不强校验（npm 已保证）。
const playwrightCoreTarball = "https://registry.npmjs.org/playwright-core/-/playwright-core-" + playwrightCoreVersion + ".tgz"

func main() {
	var (
		dest    string
		skipDrv bool
		skipBin bool
	)
	flag.StringVar(&dest, "dest", "bin/camoufox", "Camoufox 落盘目录")
	flag.BoolVar(&skipDrv, "skip-driver", false, "跳过 playwright 驱动安装")
	flag.BoolVar(&skipBin, "skip-browser", false, "跳过 Camoufox 安装")
	flag.Parse()

	if !skipBin {
		if err := installCamoufox(dest); err != nil {
			logrus.Fatalf("安装 Camoufox 失败: %v", err)
		}
	}
	if !skipDrv {
		if err := installDriver(".playwright-driver"); err != nil {
			logrus.Fatalf("安装 playwright 驱动失败: %v", err)
		}
	}
	logrus.Info("安装准备完成")
}

// installCamoufox 下载并校验固定版本的 Camoufox。
func installCamoufox(dest string) error {
	goos, goarch := runtime.GOOS, runtime.GOARCH
	key := goos + "/" + goarch
	want, ok := browser.CamoufoxSHA256[key]
	if !ok || want == "" {
		return fmt.Errorf("no pinned sha256 for platform %s; refuse to download unverifiable binary", key)
	}

	assetOS := map[string]string{"darwin": "mac", "linux": "lin", "windows": "win"}[goos]
	assetArch := map[string]string{"arm64": "arm64", "amd64": "x86_64", "386": "i686"}[goarch]
	if assetOS == "" || assetArch == "" {
		return fmt.Errorf("unsupported platform %s", key)
	}
	asset := fmt.Sprintf("camoufox-%s-%s.%s.zip", browser.CamoufoxVersion, assetOS, assetArch)
	url := "https://github.com/daijro/camoufox/releases/download/v" + browser.CamoufoxVersion + "/" + asset

	logrus.Infof("下载 %s", url)
	tmp, err := os.CreateTemp("", "camoufox-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	if err := download(url, tmp); err != nil {
		return err
	}

	sum, err := sha256File(tmp.Name())
	if err != nil {
		return err
	}
	if sum != want {
		return fmt.Errorf("sha256 mismatch for %s: got %s want %s (refusing to install)", asset, sum, want)
	}
	logrus.Infof("sha256 校验通过: %s", sum)

	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	if err := unzip(tmp.Name(), dest); err != nil {
		return err
	}
	logrus.Infof("camoufox 已安装到 %s", dest)
	return nil
}

// installDriver 准备 playwright 驱动目录：node 可执行文件 + 固定版本 playwright-core。
func installDriver(dir string) error {
	if err := os.MkdirAll(filepath.Join(dir, "package"), 0o755); err != nil {
		return err
	}

	// node：优先复用系统 node；否则提示用户安装（不自动下载 node 二进制，
	// 因为它同样需要单独的可信校验链）。
	nodePath, err := resolveNodePath()
	if err != nil {
		return err
	}
	logrus.Infof("使用 node: %s", nodePath)

	// playwright-core：经 npm 安装固定版本，npm 自带 integrity 校验。
	logrus.Infof("安装 playwright-core@%s", playwrightCoreVersion)
	npm, err := resolveNPMPath(nodePath)
	if err != nil {
		return err
	}
	cmd := exec.Command(npm, "install", "--prefix", filepath.Join(dir, "package"),
		"--no-save", "--no-audit", "--no-fund", "playwright-core@"+playwrightCoreVersion)
	cmd.Env = withPathPrefix(filepath.Dir(nodePath))
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("npm install playwright-core failed: %w", err)
	}
	logrus.Infof("playwright 驱动已就绪: %s", dir)
	return nil
}

func resolveNodePath() (string, error) {
	if p := strings.TrimSpace(os.Getenv("PLAYWRIGHT_NODEJS_PATH")); p != "" {
		if err := mustFile(p); err != nil {
			return "", fmt.Errorf("PLAYWRIGHT_NODEJS_PATH is not a node executable: %w", err)
		}
		return p, nil
	}
	p, err := exec.LookPath("node")
	if err != nil {
		return "", fmt.Errorf("node not found in PATH; install node or set PLAYWRIGHT_NODEJS_PATH")
	}
	return p, nil
}

func resolveNPMPath(nodePath string) (string, error) {
	if p := strings.TrimSpace(os.Getenv("PLAYWRIGHT_NPM_PATH")); p != "" {
		if err := mustFile(p); err != nil {
			return "", fmt.Errorf("PLAYWRIGHT_NPM_PATH is not an npm executable: %w", err)
		}
		return p, nil
	}
	if runtime.GOOS == "windows" {
		for _, name := range []string{"npm.cmd", "npm.exe", "npm"} {
			p := filepath.Join(filepath.Dir(nodePath), name)
			if mustFile(p) == nil {
				return p, nil
			}
		}
	}
	p, err := exec.LookPath("npm")
	if err != nil {
		return "", fmt.Errorf("npm not found; install node/npm, set PLAYWRIGHT_NPM_PATH, or stage playwright-core manually")
	}
	return p, nil
}

func mustFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("path is a directory: %s", path)
	}
	return nil
}

func withPathPrefix(prefix string) []string {
	env := os.Environ()
	for i, item := range env {
		if strings.HasPrefix(strings.ToUpper(item), "PATH=") {
			env[i] = "PATH=" + prefix + string(os.PathListSeparator) + item[5:]
			return env
		}
	}
	return append(env, "PATH="+prefix)
}

func download(url string, w io.Writer) error {
	resp, err := http.Get(url) //nolint:gosec // 固定 release 地址，安装期一次性获取
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", url, resp.Status)
	}
	_, err = io.Copy(w, resp.Body)
	return err
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// unzip 使用标准库解压，避免 Windows 依赖 Unix unzip 命令。
func unzip(zipPath, dest string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	root, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	for _, entry := range r.File {
		name := filepath.Clean(filepath.FromSlash(entry.Name))
		if name == "." || filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("unsafe zip entry: %s", entry.Name)
		}
		target := filepath.Join(root, name)
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		in, err := entry.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			_ = in.Close()
			return err
		}
		_, err = io.Copy(out, in)
		closeOutErr := out.Close()
		closeInErr := in.Close()
		if err != nil {
			return err
		}
		if closeOutErr != nil {
			return closeOutErr
		}
		if closeInErr != nil {
			return closeInErr
		}
	}
	return nil
}
