# 贡献指南 | Contributing Guide

感谢你为 xiaohongshu-mcp 提交改进。

## 基本流程 | Basic Workflow

1. 从最新的 `main` 创建分支。
2. 一个 PR 只聚焦一个功能或修复。
3. 提交前运行格式化、测试和静态检查。
4. 在 PR 描述中说明行为变化、兼容性影响和验证方式。

## 浏览器自动化约定 | Browser Automation

项目使用 Camoufox 和 playwright-go（Juggler 协议）。请优先使用 Playwright 原生 API 操作页面，不要注入 stealth JS，也不要事后覆盖 UA 或 Client Hints。

读取页面内的 `__INITIAL_STATE__` 时可以使用受限的 `Evaluate`；其他场景应使用页面、元素、鼠标和键盘 API。

## 代码规范 | Code Style

- Go 代码必须通过 `gofmt`。
- 注释和日志使用中文，专业术语可用英文。
- 错误和 panic 文案使用小写开头的英文，不以句号结尾。
- 保持接口边界清晰，避免重复 DTO 和无必要的抽象。
- 不要提交账号 Cookie、代理凭证或其他敏感信息。

## 提交检查 | Checklist

- [ ] 代码已格式化。
- [ ] `go test ./...` 通过。
- [ ] `go vet ./...` 通过。
- [ ] 文档和环境变量说明与当前实现一致。
- [ ] 未提交敏感信息或无关的大型资源。
