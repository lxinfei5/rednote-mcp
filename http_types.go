package main

import "github.com/xpzouying/xiaohongshu-mcp/xiaohongshu"

// ErrorResponse HTTP 错误响应。
type ErrorResponse struct {
	Error   string `json:"error"`
	Code    string `json:"code"`
	Details any    `json:"details,omitempty"`
}

// SuccessResponse HTTP 成功响应。
type SuccessResponse struct {
	Success bool   `json:"success"`
	Data    any    `json:"data"`
	Message string `json:"message,omitempty"`
}

// FeedDetailRequest Feed 详情请求。
type FeedDetailRequest struct {
	NoteID          string                         `json:"note_id,omitempty"`
	LegacyFeedID    string                         `json:"feed_id,omitempty"`
	XsecToken       string                         `json:"xsec_token" binding:"required"`
	LoadAllComments bool                           `json:"load_all_comments,omitempty"`
	CommentConfig   *xiaohongshu.CommentLoadConfig `json:"comment_config,omitempty"`
}

// SearchFeedsRequest Feed 搜索请求。
type SearchFeedsRequest struct {
	Keyword string                   `json:"keyword" binding:"required"`
	Filters xiaohongshu.FilterOption `json:"filters,omitempty"`
}

// UserProfileRequest 用户主页请求。
type UserProfileRequest struct {
	UserID    string `json:"user_id" binding:"required"`
	XsecToken string `json:"xsec_token" binding:"required"`
	Tab       string `json:"tab,omitempty"`
}
