package main

import "github.com/xpzouying/xiaohongshu-mcp/xiaohongshu"

// SearchFeedsArgs 搜索内容的参数。
type SearchFeedsArgs struct {
	Keyword string                   `json:"keyword" jsonschema:"搜索关键词"`
	Filters xiaohongshu.FilterOption `json:"filters,omitempty" jsonschema:"筛选选项"`
}

// FeedDetailArgs 获取 Feed 详情的参数。
type FeedDetailArgs struct {
	FeedID           string `json:"feed_id" jsonschema:"小红书笔记 ID，从 Feed 列表获取"`
	XsecToken        string `json:"xsec_token" jsonschema:"访问令牌，从 Feed 列表的 xsecToken 字段获取"`
	LoadAllComments  bool   `json:"load_all_comments,omitempty" jsonschema:"是否加载全部评论。false 仅返回前 10 条一级评论（默认），true 滚动加载更多评论"`
	Limit            int    `json:"limit,omitempty" jsonschema:"仅当 load_all_comments 为 true 时生效，限制加载的一级评论数量，默认 20"`
	ClickMoreReplies bool   `json:"click_more_replies,omitempty" jsonschema:"仅当 load_all_comments 为 true 时生效，是否展开二级回复"`
	ReplyLimit       int    `json:"reply_limit,omitempty" jsonschema:"仅当 click_more_replies 为 true 时生效，跳过回复数过多的评论，默认 10"`
	ScrollSpeed      string `json:"scroll_speed,omitempty" jsonschema:"仅当 load_all_comments 为 true 时生效，slow、normal 或 fast"`
}

// UserProfileArgs 获取用户主页的参数。
type UserProfileArgs struct {
	UserID    string `json:"user_id" jsonschema:"小红书用户 ID，从 Feed 列表获取"`
	XsecToken string `json:"xsec_token" jsonschema:"访问令牌，从 Feed 列表的 xsecToken 字段获取"`
	Tab       string `json:"tab,omitempty" jsonschema:"主页 tab：note（笔记，默认）、fav（收藏）或 liked（点赞）"`
}

// MyProfileArgs 获取当前登录用户主页的参数。
type MyProfileArgs struct {
	Tab string `json:"tab,omitempty" jsonschema:"主页 tab：note（笔记，默认）、fav（收藏）或 liked（点赞）"`
}

// MCPToolResult 是应用层使用的 MCP 工具结果。
type MCPToolResult struct {
	Content []MCPContent `json:"content"`
	IsError bool         `json:"isError,omitempty"`
}

// MCPContent 是应用层使用的 MCP 内容块。
type MCPContent struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	MIMEType string `json:"mimeType"`
	Data     string `json:"data"`
}
