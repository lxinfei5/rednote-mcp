package main

import (
	"context"

	"github.com/xpzouying/xiaohongshu-mcp/xiaohongshu"
)

// LoginStatusResponse 登录状态响应。
type LoginStatusResponse struct {
	IsLoggedIn bool   `json:"is_logged_in"`
	Username   string `json:"username,omitempty"`
	UserID     string `json:"user_id,omitempty"`
}

// LoginQRCodeResponse 登录扫码二维码响应。
type LoginQRCodeResponse struct {
	Timeout    string `json:"timeout"`
	IsLoggedIn bool   `json:"is_logged_in"`
	Image      string `json:"img,omitempty"`
}

// FeedsListResponse Feed 列表响应。
type FeedsListResponse struct {
	Feeds []xiaohongshu.Feed `json:"feeds"`
	Count int                `json:"count"`
}

// FeedDetailResponse Feed 详情响应。
type FeedDetailResponse struct {
	FeedID string `json:"feed_id"`
	Data   any    `json:"data"`
}

// UserProfileResponse 用户主页响应。
type UserProfileResponse struct {
	UserBasicInfo xiaohongshu.UserBasicInfo      `json:"userBasicInfo"`
	Interactions  []xiaohongshu.UserInteractions `json:"interactions"`
	Feeds         []xiaohongshu.Feed             `json:"feeds"`
}

// ReadService 是 HTTP 和 MCP 层依赖的只读业务接口。
type ReadService interface {
	CheckLoginStatus(ctx context.Context) (*LoginStatusResponse, error)
	GetLoginQRCode(ctx context.Context) (*LoginQRCodeResponse, error)
	ListFeeds(ctx context.Context) (*FeedsListResponse, error)
	SearchFeeds(ctx context.Context, keyword string, filters ...xiaohongshu.FilterOption) (*FeedsListResponse, error)
	GetFeedDetail(ctx context.Context, feedID, xsecToken string, loadAllComments bool) (*FeedDetailResponse, error)
	GetFeedDetailWithConfig(ctx context.Context, feedID, xsecToken string, loadAllComments bool, config xiaohongshu.CommentLoadConfig) (*FeedDetailResponse, error)
	UserProfile(ctx context.Context, userID, xsecToken, tab string) (*UserProfileResponse, error)
	GetMyProfile(ctx context.Context, tab string) (*UserProfileResponse, error)
}
