package main

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"github.com/xpzouying/xiaohongshu-mcp/xiaohongshu"
)

// respondError 返回错误响应
func respondError(c *gin.Context, statusCode int, code, message string, details any) {
	response := ErrorResponse{
		Error:   message,
		Code:    code,
		Details: details,
	}

	logrus.Errorf("%s %s %d", c.Request.Method, c.Request.URL.Path, statusCode)

	c.JSON(statusCode, response)
}

// respondSuccess 返回成功响应
func respondSuccess(c *gin.Context, data any, message string) {
	response := SuccessResponse{
		Success: true,
		Data:    data,
		Message: message,
	}

	logrus.Infof("%s %s %d", c.Request.Method, c.Request.URL.Path, http.StatusOK)

	c.JSON(http.StatusOK, response)
}

func serviceErrorStatus(err error) int {
	switch {
	case errors.Is(err, xiaohongshu.ErrInvalidArgument):
		return http.StatusBadRequest
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout
	case errors.Is(err, context.Canceled):
		return http.StatusRequestTimeout
	default:
		return http.StatusInternalServerError
	}
}

// checkLoginStatusHandler 检查登录状态
func (s *AppServer) checkLoginStatusHandler(c *gin.Context) {
	status, err := s.xiaohongshuService.CheckLoginStatus(c.Request.Context())
	if err != nil {
		respondError(c, serviceErrorStatus(err), "STATUS_CHECK_FAILED",
			"检查登录状态失败", err.Error())
		return
	}

	respondSuccess(c, status, "检查登录状态成功")
}

// getLoginQRCodeHandler 处理 [GET /api/v1/login/qrcode] 请求。
// 用于生成并返回登录二维码（Base64 图片 + 超时时间），供前端展示给用户扫码登录。
func (s *AppServer) getLoginQRCodeHandler(c *gin.Context) {
	result, err := s.xiaohongshuService.GetLoginQRCode(c.Request.Context())
	if err != nil {
		respondError(c, serviceErrorStatus(err), "STATUS_CHECK_FAILED",
			"获取登录二维码失败", err.Error())
		return
	}

	respondSuccess(c, result, "获取登录二维码成功")
}

// listFeedsHandler 获取Feeds列表
func (s *AppServer) listFeedsHandler(c *gin.Context) {
	result, err := s.xiaohongshuService.ListFeeds(c.Request.Context())
	if err != nil {
		respondError(c, serviceErrorStatus(err), "LIST_FEEDS_FAILED",
			"获取Feeds列表失败", err.Error())
		return
	}

	respondSuccess(c, result, "获取Feeds列表成功")
}

// searchFeedsHandler 搜索Feeds
func (s *AppServer) searchFeedsHandler(c *gin.Context) {
	var keyword string
	var filters xiaohongshu.FilterOption

	switch c.Request.Method {
	case http.MethodPost:
		// 对于POST请求，从JSON中获取keyword
		var searchReq SearchFeedsRequest
		if err := c.ShouldBindJSON(&searchReq); err != nil {
			respondError(c, http.StatusBadRequest, "INVALID_REQUEST",
				"请求参数错误", err.Error())
			return
		}
		keyword = searchReq.Keyword
		filters = searchReq.Filters
	default:
		keyword = c.Query("keyword")
	}

	if keyword == "" {
		respondError(c, http.StatusBadRequest, "MISSING_KEYWORD",
			"缺少关键词参数", "keyword parameter is required")
		return
	}

	result, err := s.xiaohongshuService.SearchFeeds(c.Request.Context(), keyword, filters)
	if err != nil {
		respondError(c, serviceErrorStatus(err), "SEARCH_FEEDS_FAILED",
			"搜索Feeds失败", err.Error())
		return
	}

	respondSuccess(c, result, "搜索Feeds成功")
}

// getFeedDetailHandler 获取Feed详情
func (s *AppServer) getFeedDetailHandler(c *gin.Context) {
	var req FeedDetailRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "INVALID_REQUEST",
			"请求参数错误", err.Error())
		return
	}
	noteID, err := canonicalNoteID(req.NoteID, req.LegacyFeedID)
	if err != nil {
		respondError(c, http.StatusBadRequest, "INVALID_REQUEST",
			"请求参数错误", err.Error())
		return
	}
	if noteID == "" {
		respondError(c, http.StatusBadRequest, "INVALID_REQUEST",
			"请求参数错误", "note_id parameter is required")
		return
	}

	var result *FeedDetailResponse

	if req.CommentConfig != nil {
		result, err = s.xiaohongshuService.GetFeedDetailWithConfig(c.Request.Context(), noteID, req.XsecToken, req.LoadAllComments, *req.CommentConfig)
	} else {
		result, err = s.xiaohongshuService.GetFeedDetail(c.Request.Context(), noteID, req.XsecToken, req.LoadAllComments)
	}

	if err != nil {
		respondError(c, serviceErrorStatus(err), "GET_FEED_DETAIL_FAILED",
			"获取Feed详情失败", err.Error())
		return
	}

	respondSuccess(c, result, "获取Feed详情成功")
}

// userProfileHandler 用户主页
func (s *AppServer) userProfileHandler(c *gin.Context) {
	var req UserProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "INVALID_REQUEST",
			"请求参数错误", err.Error())
		return
	}

	result, err := s.xiaohongshuService.UserProfile(c.Request.Context(), req.UserID, req.XsecToken, req.Tab)
	if err != nil {
		respondError(c, serviceErrorStatus(err), "GET_USER_PROFILE_FAILED",
			"获取用户主页失败", err.Error())
		return
	}

	respondSuccess(c, result, "获取用户主页成功")
}

// healthHandler 健康检查
func healthHandler(c *gin.Context) {
	respondSuccess(c, map[string]any{
		"status":    "healthy",
		"service":   "xiaohongshu-mcp",
		"version":   version,
		"account":   "github.com/xpzouying/xiaohongshu-mcp",
		"timestamp": "now",
	}, "服务正常")
}

// myProfileHandler 我的信息
func (s *AppServer) myProfileHandler(c *gin.Context) {
	// 获取当前登录用户信息
	result, err := s.xiaohongshuService.GetMyProfile(c.Request.Context(), c.Query("tab"))
	if err != nil {
		respondError(c, serviceErrorStatus(err), "GET_MY_PROFILE_FAILED",
			"获取我的主页失败", err.Error())
		return
	}

	respondSuccess(c, result, "获取我的主页成功")
}
