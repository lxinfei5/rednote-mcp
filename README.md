# xiaohongshu-mcp

一个基于 Camoufox 和 playwright-go 的小红书只读 MCP 服务，向 AI 助手提供登录初始化、Feed、笔记详情和用户主页读取能力。

当前版本只读，不提供发布、评论写入、点赞、收藏、通知和 Cookie 删除操作。

## 功能

- 检查登录状态、获取登录二维码
- 读取首页 Feed 和搜索结果
- 读取笔记详情、评论和视频信息
- 读取指定用户主页和当前用户主页
- 通过 MCP Streamable HTTP 暴露工具，默认端点为 `/mcp`

## 快速开始

### 1. 安装浏览器和 Playwright 驱动

运行时不会自动下载浏览器或驱动。首次使用前执行：

```bash
go run ./cmd/camoufox-setup
```

详细的路径和环境变量说明见 [`bin/README.md`](./bin/README.md)。

### 2. 扫码登录

```bash
go run ./cmd/login
```

登录成功后，Cookie 默认保存到当前目录的 `cookies.json`。

### 3. 启动服务

```bash
go run .
```

常用参数：

```bash
go run . -headless=true
go run . -port 127.0.0.1:18060
```

服务默认只监听回环地址。可通过以下环境变量覆盖运行时配置：

- `XHS_CAMOUFOX_BIN`：Camoufox 可执行文件路径
- `PLAYWRIGHT_DRIVER_PATH`：Playwright 驱动目录
- `PLAYWRIGHT_NODEJS_PATH`：Node.js 可执行文件路径
- `COOKIES_PATH`：Cookie 文件路径
- `XHS_PROXY`：代理地址

## 文档

- [HTTP API 和 MCP 工具说明](./docs/API.md)
- [进程管理和运行说明](./bin/README.md)
- [贡献指南](./CONTRIBUTING.md)

## 开发检查

```bash
gofmt -w $(rg --files -g '*.go')
go test ./...
go vet ./...
```

## 许可证

本项目遵循 [MIT License](./LICENSE)。
