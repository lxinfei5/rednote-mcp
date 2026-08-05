package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/xpzouying/xiaohongshu-mcp/xiaohongshu"
)

// MCP 工具处理函数

// handleCheckLoginStatus 处理检查登录状态
func (s *AppServer) handleCheckLoginStatus(ctx context.Context) *MCPToolResult {
	logrus.Info("MCP: 检查登录状态")

	status, err := s.xiaohongshuService.CheckLoginStatus(ctx)
	if err != nil {
		return &MCPToolResult{
			Content: []MCPContent{{
				Type: "text",
				Text: "检查登录状态失败: " + err.Error(),
			}},
			IsError: true,
		}
	}

	var resultText string
	if status.IsLoggedIn {
		resultText = fmt.Sprintf("✅ 已登录\n用户名: %s\n\n你可以使用其他功能了。", status.Username)
	} else {
		resultText = "❌ 未登录\n\n请使用 get_login_qrcode 工具获取二维码进行登录。"
	}

	return &MCPToolResult{
		Content: []MCPContent{{
			Type: "text",
			Text: resultText,
		}},
	}
}

// handleGetLoginQRCode 处理获取登录二维码请求。
// 返回二维码图片的 Base64 编码和超时时间，供前端展示扫码登录。
func (s *AppServer) handleGetLoginQRCode(ctx context.Context) *MCPToolResult {
	logrus.Info("MCP: 获取登录扫码图片")

	result, err := s.xiaohongshuService.GetLoginQRCode(ctx)
	if err != nil {
		return &MCPToolResult{
			Content: []MCPContent{{Type: "text", Text: "获取登录扫码图片失败: " + err.Error()}},
			IsError: true,
		}
	}

	if result.IsLoggedIn {
		return &MCPToolResult{
			Content: []MCPContent{{Type: "text", Text: "你当前已处于登录状态"}},
		}
	}

	now := time.Now()
	deadline := func() string {
		d, err := time.ParseDuration(result.Timeout)
		if err != nil {
			return now.Format("2006-01-02 15:04:05")
		}
		return now.Add(d).Format("2006-01-02 15:04:05")
	}()

	contents := []MCPContent{
		{Type: "text", Text: "请用小红书 App 在 " + deadline + " 前扫码登录 👇"},
		{
			Type:     "image",
			MIMEType: "image/png",
			Data:     strings.TrimPrefix(result.Image, "data:image/png;base64,"),
		},
	}
	return &MCPToolResult{Content: contents}
}

// handleListFeeds 处理获取 Feeds 列表
func (s *AppServer) handleListFeeds(ctx context.Context) *MCPToolResult {
	logrus.Info("MCP: 获取Feeds列表")

	result, err := s.xiaohongshuService.ListFeeds(ctx)
	if err != nil {
		return &MCPToolResult{
			Content: []MCPContent{{
				Type: "text",
				Text: "获取Feeds列表失败: " + err.Error(),
			}},
			IsError: true,
		}
	}

	jsonData, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return &MCPToolResult{
			Content: []MCPContent{{
				Type: "text",
				Text: fmt.Sprintf("获取Feeds列表成功，但序列化失败: %v", err),
			}},
			IsError: true,
		}
	}

	return &MCPToolResult{
		Content: []MCPContent{{
			Type: "text",
			Text: string(jsonData),
		}},
	}
}

// handleSearchFeeds 处理搜索 Feeds
func (s *AppServer) handleSearchFeeds(ctx context.Context, args SearchFeedsArgs) *MCPToolResult {
	logrus.Info("MCP: 搜索Feeds")

	if args.Keyword == "" {
		return &MCPToolResult{
			Content: []MCPContent{{
				Type: "text",
				Text: "搜索Feeds失败: 缺少关键词参数",
			}},
			IsError: true,
		}
	}

	logrus.Infof("MCP: 搜索Feeds - 关键词: %s", args.Keyword)

	result, err := s.xiaohongshuService.SearchFeeds(ctx, args.Keyword, args.Filters)
	if err != nil {
		return &MCPToolResult{
			Content: []MCPContent{{
				Type: "text",
				Text: "搜索Feeds失败: " + err.Error(),
			}},
			IsError: true,
		}
	}

	jsonData, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return &MCPToolResult{
			Content: []MCPContent{{
				Type: "text",
				Text: fmt.Sprintf("搜索Feeds成功，但序列化失败: %v", err),
			}},
			IsError: true,
		}
	}

	return &MCPToolResult{
		Content: []MCPContent{{
			Type: "text",
			Text: string(jsonData),
		}},
	}
}

// handleGetFeedDetail 处理获取 Feed 详情
func (s *AppServer) handleGetFeedDetail(ctx context.Context, args FeedDetailArgs) *MCPToolResult {
	logrus.Info("MCP: 获取Feed详情")

	noteID, err := canonicalNoteID(args.NoteID, args.LegacyFeedID)
	if err != nil {
		return &MCPToolResult{
			Content: []MCPContent{{Type: "text", Text: "获取Feed详情失败: " + err.Error()}},
			IsError: true,
		}
	}
	if noteID == "" {
		return &MCPToolResult{
			Content: []MCPContent{{Type: "text", Text: "获取Feed详情失败: 缺少note_id参数"}},
			IsError: true,
		}
	}

	if args.XsecToken == "" {
		return &MCPToolResult{
			Content: []MCPContent{{Type: "text", Text: "获取Feed详情失败: 缺少xsec_token参数"}},
			IsError: true,
		}
	}

	config := xiaohongshu.DefaultCommentLoadConfig()
	if args.LoadAllComments {
		config.ClickMoreReplies = args.ClickMoreReplies
		if args.ReplyLimit > 0 {
			config.MaxRepliesThreshold = args.ReplyLimit
		}
		if args.Limit > 0 {
			config.MaxCommentItems = args.Limit
		}
		if args.ScrollSpeed != "" {
			config.ScrollSpeed = args.ScrollSpeed
		}
	}

	logrus.Infof("MCP: 获取Feed详情 - Note ID: %s, loadAllComments=%v, config=%+v", noteID, args.LoadAllComments, config)

	result, err := s.xiaohongshuService.GetFeedDetailWithConfig(ctx, noteID, args.XsecToken, args.LoadAllComments, config)
	if err != nil {
		return &MCPToolResult{
			Content: []MCPContent{{Type: "text", Text: "获取Feed详情失败: " + err.Error()}},
			IsError: true,
		}
	}

	jsonData, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return &MCPToolResult{
			Content: []MCPContent{{
				Type: "text",
				Text: fmt.Sprintf("获取Feed详情成功，但序列化失败: %v", err),
			}},
			IsError: true,
		}
	}

	return &MCPToolResult{
		Content: []MCPContent{{
			Type: "text",
			Text: string(jsonData),
		}},
	}
}

// handleUserProfile 获取用户主页
func (s *AppServer) handleUserProfile(ctx context.Context, args UserProfileArgs) *MCPToolResult {
	logrus.Info("MCP: 获取用户主页")

	if args.UserID == "" {
		return &MCPToolResult{
			Content: []MCPContent{{Type: "text", Text: "获取用户主页失败: 缺少user_id参数"}},
			IsError: true,
		}
	}

	if args.XsecToken == "" {
		return &MCPToolResult{
			Content: []MCPContent{{Type: "text", Text: "获取用户主页失败: 缺少xsec_token参数"}},
			IsError: true,
		}
	}

	logrus.Infof("MCP: 获取用户主页 - User ID: %s", args.UserID)

	result, err := s.xiaohongshuService.UserProfile(ctx, args.UserID, args.XsecToken, args.Tab)
	if err != nil {
		return &MCPToolResult{
			Content: []MCPContent{{Type: "text", Text: "获取用户主页失败: " + err.Error()}},
			IsError: true,
		}
	}

	jsonData, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return &MCPToolResult{
			Content: []MCPContent{{
				Type: "text",
				Text: fmt.Sprintf("获取用户主页，但序列化失败: %v", err),
			}},
			IsError: true,
		}
	}

	return &MCPToolResult{
		Content: []MCPContent{{
			Type: "text",
			Text: string(jsonData),
		}},
	}
}

// handleGetMyProfile 获取当前登录用户主页
func (s *AppServer) handleGetMyProfile(ctx context.Context, tab string) *MCPToolResult {
	logrus.Infof("MCP: 获取我的主页 tab=%s", tab)

	result, err := s.xiaohongshuService.GetMyProfile(ctx, tab)
	if err != nil {
		return &MCPToolResult{
			Content: []MCPContent{{Type: "text", Text: "获取我的主页失败: " + err.Error()}},
			IsError: true,
		}
	}

	jsonData, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return &MCPToolResult{
			Content: []MCPContent{{
				Type: "text",
				Text: fmt.Sprintf("获取我的主页成功，但序列化失败: %v", err),
			}},
			IsError: true,
		}
	}

	return &MCPToolResult{
		Content: []MCPContent{{
			Type: "text",
			Text: string(jsonData),
		}},
	}
}
