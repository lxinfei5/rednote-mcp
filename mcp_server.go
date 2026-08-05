package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"runtime/debug"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sirupsen/logrus"
)

// InitMCPServer 初始化 MCP Server
func InitMCPServer(appServer *AppServer) *mcp.Server {
	// 创建 MCP Server
	server := mcp.NewServer(
		&mcp.Implementation{
			Name:    "xiaohongshu-mcp",
			Version: "2.0.0",
		},
		nil,
	)

	// 注册所有工具
	registerTools(server, appServer)

	logrus.Info("MCP Server 已使用官方 SDK 初始化")

	return server
}

func withPanicRecovery[T any](
	toolName string,
	handler func(context.Context, *mcp.CallToolRequest, T) (*mcp.CallToolResult, any, error),
) func(context.Context, *mcp.CallToolRequest, T) (*mcp.CallToolResult, any, error) {

	return func(ctx context.Context, req *mcp.CallToolRequest, args T) (result *mcp.CallToolResult, resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				logrus.WithFields(logrus.Fields{
					"tool":  toolName,
					"panic": r,
				}).Error("MCP 工具处理函数发生 panic")

				logrus.Errorf("堆栈信息:\n%s", debug.Stack())

				result = &mcp.CallToolResult{
					Content: []mcp.Content{
						&mcp.TextContent{
							Text: fmt.Sprintf("工具 %s 执行时发生内部错误: %v\n\n请查看服务端日志获取详细信息。", toolName, r),
						},
					},
					IsError: true,
				}
				resp = nil
				err = nil
			}
		}()

		return handler(ctx, req, args)
	}
}

// registerTools 注册所有 MCP 工具
func registerTools(server *mcp.Server, appServer *AppServer) {
	// 工具 1: 检查登录状态
	mcp.AddTool(server,
		&mcp.Tool{
			Name:        "check_login_status",
			Description: "检查小红书登录状态",
			Annotations: &mcp.ToolAnnotations{
				Title:        "Check Login Status",
				ReadOnlyHint: true,
			},
		},
		withPanicRecovery("check_login_status", func(ctx context.Context, _ *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, any, error) {
			result := appServer.handleCheckLoginStatus(ctx)
			return convertToMCPResult(result), nil, nil
		}),
	)

	// 工具 2: 获取登录二维码
	mcp.AddTool(server,
		&mcp.Tool{
			Name:        "get_login_qrcode",
			Description: "获取登录二维码（返回 Base64 图片和超时时间；扫码成功后保存登录 Cookie 以初始化只读会话）",
			Annotations: &mcp.ToolAnnotations{
				Title: "Get Login QR Code",
			},
		},
		withPanicRecovery("get_login_qrcode", func(ctx context.Context, _ *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, any, error) {
			result := appServer.handleGetLoginQRCode(ctx)
			return convertToMCPResult(result), nil, nil
		}),
	)

	// 工具 3: 获取Feed列表
	mcp.AddTool(server,
		&mcp.Tool{
			Name:        "list_feeds",
			Description: "获取首页笔记列表。每条笔记返回 note_id 和 xsec_token，可直接传给 get_feed_detail；列表键仍为 feeds。",
			Annotations: &mcp.ToolAnnotations{
				Title:        "List Feeds",
				ReadOnlyHint: true,
			},
		},
		withPanicRecovery("list_feeds", func(ctx context.Context, _ *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, any, error) {
			result := appServer.handleListFeeds(ctx)
			return convertToMCPResult(result), nil, nil
		}),
	)

	// 工具 4: 搜索内容
	mcp.AddTool(server,
		&mcp.Tool{
			Name:        "search_feeds",
			Description: "搜索小红书笔记（需要已登录）。每条结果返回 note_id 和 xsec_token，可直接传给 get_feed_detail；结果列表键为 feeds。",
			Annotations: &mcp.ToolAnnotations{
				Title:        "Search Feeds",
				ReadOnlyHint: true,
			},
		},
		withPanicRecovery("search_feeds", func(ctx context.Context, _ *mcp.CallToolRequest, args SearchFeedsArgs) (*mcp.CallToolResult, any, error) {
			result := appServer.handleSearchFeeds(ctx, args)
			return convertToMCPResult(result), nil, nil
		}),
	)

	// 工具 5: 获取Feed详情
	mcp.AddTool(server,
		&mcp.Tool{
			Name:        "get_feed_detail",
			Description: "获取小红书笔记详情。使用 note_id 和 xsec_token；旧参数 feed_id 仍兼容但新调用请使用 note_id。返回笔记内容、图片、作者信息、互动数据（点赞/收藏/分享数）及评论列表，结果中的笔记标识统一为 note_id，令牌统一为 xsec_token。视频笔记额外返回 video 字段，含各编码档位的视频直链与字幕地址（均带签名、有时效）。默认返回前10条一级评论，如需更多评论请设置load_all_comments=true",
			Annotations: &mcp.ToolAnnotations{
				Title:        "Get Feed Detail",
				ReadOnlyHint: true,
			},
		},
		withPanicRecovery("get_feed_detail", func(ctx context.Context, _ *mcp.CallToolRequest, args FeedDetailArgs) (*mcp.CallToolResult, any, error) {
			result := appServer.handleGetFeedDetail(ctx, args)
			return convertToMCPResult(result), nil, nil
		}),
	)

	// 工具 6: 获取用户主页
	mcp.AddTool(server,
		&mcp.Tool{
			Name:        "user_profile",
			Description: "获取指定的小红书用户主页，返回用户基本信息，关注、粉丝、获赞量，以及指定 tab 下的内容。输入令牌使用 xsec_token，返回的笔记使用 note_id 和 xsec_token。tab 可选 note(笔记,默认)、fav(收藏)、liked(点赞)，后两者可能被对方设为不公开",
			Annotations: &mcp.ToolAnnotations{
				Title:        "User Profile",
				ReadOnlyHint: true,
			},
		},
		withPanicRecovery("user_profile", func(ctx context.Context, _ *mcp.CallToolRequest, args UserProfileArgs) (*mcp.CallToolResult, any, error) {
			result := appServer.handleUserProfile(ctx, args)
			return convertToMCPResult(result), nil, nil
		}),
	)

	// 工具 7: 获取我的主页
	mcp.AddTool(server,
		&mcp.Tool{
			Name:        "get_my_profile",
			Description: "获取当前登录用户的主页，返回用户基本信息，关注、粉丝、获赞量，以及指定 tab 下的内容。返回的笔记使用 note_id 和 xsec_token。tab 可选 note(自己发的笔记,默认)、fav(自己收藏的)、liked(自己点赞的)",
			Annotations: &mcp.ToolAnnotations{
				Title:        "Get My Profile",
				ReadOnlyHint: true,
			},
		},
		withPanicRecovery("get_my_profile", func(ctx context.Context, _ *mcp.CallToolRequest, args MyProfileArgs) (*mcp.CallToolResult, any, error) {
			result := appServer.handleGetMyProfile(ctx, args.Tab)
			return convertToMCPResult(result), nil, nil
		}),
	)

	logrus.Info("MCP 工具注册完成")
}

// convertToMCPResult 将自定义的 MCPToolResult 转换为官方 SDK 的格式
func convertToMCPResult(result *MCPToolResult) *mcp.CallToolResult {
	var contents []mcp.Content
	for _, c := range result.Content {
		switch c.Type {
		case "text":
			contents = append(contents, &mcp.TextContent{Text: c.Text})
		case "image":
			// 解码 base64 字符串为 []byte
			imageData, err := base64.StdEncoding.DecodeString(c.Data)
			if err != nil {
				logrus.WithError(err).Error("图片 base64 数据解码失败")
				// 如果解码失败，添加错误文本
				contents = append(contents, &mcp.TextContent{
					Text: "图片数据解码失败: " + err.Error(),
				})
			} else {
				contents = append(contents, &mcp.ImageContent{
					Data:     imageData,
					MIMEType: c.MIMEType,
				})
			}
		}
	}

	return &mcp.CallToolResult{
		Content: contents,
		IsError: result.IsError,
	}
}
