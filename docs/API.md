# 小红书 MCP HTTP API 文档（只读裁剪版）

本版本只保留登录初始化、Feed/笔记和用户信息读取能力。所有内容写入、评论/回复写入、点赞/收藏操作、通知和 Cookie 删除接口均已移除；访问这些旧路径会返回 404。Feed 详情中的评论仍可读取。

**Base URL**: `http://localhost:18060`

## 通用响应格式

成功响应：

```json
{
  "success": true,
  "data": {},
  "message": "操作成功消息"
}
```

错误响应：

```json
{
  "error": "错误消息",
  "code": "ERROR_CODE",
  "details": "详细错误信息"
}
```

## API 端点一览

| 方法 | 端点 | 描述 |
|------|------|------|
| GET | `/health` | 健康检查 |
| GET | `/api/v1/login/status` | 检查登录状态 |
| GET | `/api/v1/login/qrcode` | 获取登录二维码；扫码成功后保存登录 Cookie |
| GET | `/api/v1/feeds/list` | 获取首页 Feed 列表 |
| GET/POST | `/api/v1/feeds/search` | 搜索 Feed |
| POST | `/api/v1/feeds/detail` | 获取 Feed 详情、评论和视频信息 |
| POST | `/api/v1/user/profile` | 获取指定用户主页 |
| GET | `/api/v1/user/me` | 获取当前登录用户主页 |

## 1. 健康检查

```http
GET /health
```

## 2. 登录管理

### 2.1 检查登录状态

```http
GET /api/v1/login/status
```

示例响应：

```json
{
  "success": true,
  "data": {
    "is_logged_in": true,
    "username": "用户名",
    "user_id": "用户ID"
  },
  "message": "检查登录状态成功"
}
```

### 2.2 获取登录二维码

```http
GET /api/v1/login/qrcode
```

示例响应：

```json
{
  "success": true,
  "data": {
    "timeout": "4m0s",
    "is_logged_in": false,
    "img": "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAA..."
  },
  "message": "获取登录二维码成功"
}
```

`img` 是二维码 Base64 图片。二维码被扫描后，服务会把浏览器取得的登录 Cookie 保存到 Cookie 存储中；这是保留登录能力所必需的内部写入。当前没有 API 用于删除 Cookie。

## 3. Feed 读取

### 3.1 获取首页 Feed 列表

```http
GET /api/v1/feeds/list
```

### 3.2 搜索 Feed

只传关键词：

```http
GET /api/v1/feeds/search?keyword=旅行
```

需要筛选条件时使用 POST：

```http
POST /api/v1/feeds/search
Content-Type: application/json

{
  "keyword": "旅行",
  "filters": {
    "sort_by": "最新",
    "note_type": "不限",
    "publish_time": "不限",
    "search_scope": "不限",
    "location": "不限"
  }
}
```

筛选字段可用值：

- `sort_by`: `综合`、`最新`、`最多点赞`、`最多评论`、`最多收藏`
- `note_type`: `不限`、`视频`、`图文`
- `publish_time`: `不限`、`一天内`、`一周内`、`半年内`
- `search_scope`: `不限`、`已看过`、`未看过`、`已关注`
- `location`: `不限`、`同城`、`附近`

列表结果中的每条笔记统一使用以下字段，字段值可以直接串联到详情接口：

```json
{
  "note_id": "笔记ID",
  "xsec_token": "访问令牌"
}
```

`note_id` 是小红书笔记的唯一标识；`xsec_token` 是访问详情或指定用户主页所需的时效令牌。

### 3.3 获取 Feed 详情

```http
POST /api/v1/feeds/detail
Content-Type: application/json

{
  "note_id": "笔记ID",
  "xsec_token": "访问令牌",
  "load_all_comments": false,
  "comment_config": {
    "click_more_replies": false,
    "max_replies_threshold": 10,
    "max_comment_items": 20,
    "scroll_speed": "normal"
  }
}
```

`note_id` 和 `xsec_token` 通常直接从 Feed 列表或搜索结果中的同名字段获得。旧字段 `feed_id` 仍作为兼容别名接受，新调用请使用 `note_id`。详情结果中的笔记标识和令牌也统一为 `note_id`、`xsec_token`，并包含正文、图片、作者、互动数据、评论；视频笔记还会返回带时效签名的视频流和字幕信息。`comment_config` 仅在需要加载更多评论时使用。

## 4. 用户读取

### 4.1 获取指定用户主页

```http
POST /api/v1/user/profile
Content-Type: application/json

{
  "user_id": "用户ID",
  "xsec_token": "访问令牌",
  "tab": "note"
}
```

`tab` 可选：`note`（笔记，默认）、`fav`（收藏）、`liked`（点赞）。后两个页面可能被对方设为不公开。

### 4.2 获取当前登录用户主页

```http
GET /api/v1/user/me?tab=note
```

`tab` 同样可选 `note`、`fav`、`liked`。

## 5. MCP 协议支持

MCP 端点为 `/mcp` 和 `/mcp/*path`，使用 Streamable HTTP。当前注册的 7 个工具为：

- `check_login_status`
- `get_login_qrcode`
- `list_feeds`
- `search_feeds`
- `get_feed_detail`
- `user_profile`
- `get_my_profile`

`list_feeds`、`search_feeds`、`user_profile` 和 `get_my_profile` 返回的笔记条目统一使用 `note_id`、`xsec_token`。调用 `get_feed_detail` 时，将这两个字段原样传入即可；`feed_id` 仅作为旧 MCP/HTTP 调用的兼容别名。

## 6. 错误代码

| 错误代码 | HTTP 状态码 | 描述 |
|----------|-------------|------|
| `INVALID_REQUEST` | 400 | 请求参数错误或格式不正确 |
| `MISSING_KEYWORD` | 400 | 搜索时缺少关键词 |
| `STATUS_CHECK_FAILED` | 408/500/504 | 登录状态或二维码获取失败 |
| `LIST_FEEDS_FAILED` | 408/500/504 | 获取 Feed 列表失败 |
| `SEARCH_FEEDS_FAILED` | 400/408/500/504 | 搜索 Feed 失败或筛选参数无效 |
| `GET_FEED_DETAIL_FAILED` | 408/500/504 | 获取 Feed 详情失败 |
| `GET_USER_PROFILE_FAILED` | 400/408/500/504 | 获取用户主页失败或 tab 无效 |
| `GET_MY_PROFILE_FAILED` | 400/408/500/504 | 获取当前用户主页失败或 tab 无效 |
| `INTERNAL_ERROR` | 500 | 服务器内部错误 |

## 注意事项

1. 访问部分读取能力前需要先登录。
2. `note_id` 和 `xsec_token` 是笔记详情调用所需的字段，通常从 Feed 列表或搜索结果中原样传递。
3. 服务仍会在登录二维码扫码成功后保存 Cookie；除此之外，本版本不提供内容写入操作。
4. 所有接口都受本机 Host/Origin 安全中间件保护，并记录请求日志。
