# xiaohongshu-mcp

A read-only Xiaohongshu MCP service powered by Camoufox and playwright-go. It provides login initialization, Feed, note-detail, comment, and user-profile reads for AI assistants.

The current edition does not publish content or perform comment writes, likes, favorites, notification actions, or cookie deletion.

## Features

- Check login status and obtain a login QR code
- Read home Feed and search results
- Read note details, comments, and video metadata
- Read public user profiles and the current user's profile
- Expose tools through MCP Streamable HTTP at `/mcp`

## Quick start

### 1. Install Camoufox and the Playwright driver

The service never downloads browsers or drivers at runtime. Run the pinned setup command once:

```bash
go run ./cmd/camoufox-setup
```

See [`bin/README.md`](./bin/README.md) for paths and environment variables.

### 2. Log in with a QR code

```bash
go run ./cmd/login
```

After login, cookies are saved to `cookies.json` in the current directory by default.

### 3. Start the service

```bash
go run .
```

Common options:

```bash
go run . -headless=true
go run . -port 127.0.0.1:18060
```

The service listens on the loopback address by default. Runtime settings can be overridden with:

- `XHS_CAMOUFOX_BIN`: Camoufox executable path
- `PLAYWRIGHT_DRIVER_PATH`: Playwright driver directory
- `PLAYWRIGHT_NODEJS_PATH`: Node.js executable path
- `COOKIES_PATH`: cookie file path
- `XHS_PROXY`: proxy address

## Documentation

- [HTTP API and MCP tools](./docs/API.md)
- [Process management and runtime notes](./bin/README.md)
- [Contributing guide](./CONTRIBUTING.md)

## Development checks

```bash
gofmt -w $(rg --files -g '*.go')
go test ./...
go vet ./...
```

## License

This project is licensed under the [MIT License](./LICENSE).
