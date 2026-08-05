package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMCPStatelessSinglePost 固定「/mcp 接受不带 initialize 握手的单次 POST」这一契约。
//
// 契约由 routes.go 里一行 Stateless 支撑，丢掉它编译和其他单测都不会报错。
func TestMCPStatelessSinglePost(t *testing.T) {
	router := setupRoutes(NewAppServer(NewXiaohongshuService(sharedBrowser), nil))
	server := httptest.NewServer(router)
	defer server.Close()

	post := func(t *testing.T, body string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, server.URL+"/mcp", strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		return resp
	}

	// 只用 tools/list：它不碰浏览器，而契约丢失时它恰好就是失败点——
	// 有状态模式下无 session 调用 tools/list 会被回以「握手前不允许调用」。
	resp := post(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var result struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))

	require.Nil(t, result.Error, "无握手的 tools/list 不应报错")
	assert.NotEmpty(t, result.Result.Tools, "应返回已注册的工具")
}

// TestReadToolsRegistered 固定裁剪后的只读工具集合已注册到 MCP。
//
// 工具的注册各是 registerTools 里一段独立代码，漏掉任何一个编译都不会报错，
// 只有真正调用时才会发现工具不存在；同时用精确集合防止写工具回流。
func TestReadToolsRegistered(t *testing.T) {
	router := setupRoutes(NewAppServer(NewXiaohongshuService(sharedBrowser), nil))
	server := httptest.NewServer(router)
	defer server.Close()

	req, err := http.NewRequest(http.MethodPost, server.URL+"/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	var result struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))

	names := make(map[string]bool, len(result.Result.Tools))
	for _, tool := range result.Result.Tools {
		names[tool.Name] = true
	}

	wantTools := []string{
		"check_login_status",
		"get_login_qrcode",
		"list_feeds",
		"search_feeds",
		"get_feed_detail",
		"user_profile",
		"get_my_profile",
	}
	assert.Len(t, names, len(wantTools))
	for _, want := range wantTools {
		assert.True(t, names[want], "工具 %s 应已注册", want)
	}
}

func TestMCPNoteFieldContract(t *testing.T) {
	router := setupRoutes(NewAppServer(NewXiaohongshuService(sharedBrowser), nil))
	server := httptest.NewServer(router)
	defer server.Close()

	req, err := http.NewRequest(http.MethodPost, server.URL+"/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	type toolDefinition struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		InputSchema map[string]any `json:"inputSchema"`
	}
	var result struct {
		Result struct {
			Tools []toolDefinition `json:"tools"`
		} `json:"result"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))

	findTool := func(name string) (*toolDefinition, bool) {
		for i := range result.Result.Tools {
			tool := &result.Result.Tools[i]
			if tool.Name == name {
				return tool, true
			}
		}
		return nil, false
	}

	detail, ok := findTool("get_feed_detail")
	require.True(t, ok)
	assert.Contains(t, detail.Description, "note_id")
	assert.Contains(t, detail.Description, "xsec_token")
	detailProperties, ok := detail.InputSchema["properties"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, detailProperties, "note_id")
	assert.Contains(t, detailProperties, "feed_id")
	assert.Contains(t, detailProperties, "xsec_token")
	assert.Equal(t, []any{
		map[string]any{"required": []any{"note_id"}},
		map[string]any{"required": []any{"feed_id"}},
	}, detail.InputSchema["anyOf"])

	search, ok := findTool("search_feeds")
	require.True(t, ok)
	searchProperties, ok := search.InputSchema["properties"].(map[string]any)
	require.True(t, ok)
	filters, ok := searchProperties["filters"].(map[string]any)
	require.True(t, ok)
	filterProperties, ok := filters["properties"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, filterProperties, "sort_by")
	assert.Contains(t, filterProperties, "note_type")
}

// TestReadRoutesRegistered 固定裁剪后的 HTTP 路由集合。
//
// 读路由表而不是发请求：这些 handler 会真的起浏览器访问小红书，
// 单测里不能碰。
func TestReadRoutesRegistered(t *testing.T) {
	router := setupRoutes(NewAppServer(NewXiaohongshuService(sharedBrowser), nil))

	registered := make(map[string]bool)
	for _, r := range router.Routes() {
		registered[r.Method+" "+r.Path] = true
	}

	for _, want := range []string{
		"GET /api/v1/login/status",
		"GET /api/v1/login/qrcode",
		"GET /api/v1/feeds/list",
		"GET /api/v1/feeds/search",
		"POST /api/v1/feeds/search",
		"POST /api/v1/feeds/detail",
		"POST /api/v1/user/profile",
		"GET /api/v1/user/me",
	} {
		assert.True(t, registered[want], "路由 %s 应已注册", want)
	}

	for _, removed := range []string{
		"DELETE /api/v1/login/cookies",
		"POST /api/v1/publish",
		"POST /api/v1/publish_video",
		"POST /api/v1/feeds/comment",
		"POST /api/v1/feeds/comment/reply",
		"POST /api/v1/feeds/like",
		"POST /api/v1/feeds/favorite",
		"GET /api/v1/notifications/unread",
		"GET /api/v1/notifications/list",
		"POST /api/v1/notifications/list",
		"POST /api/v1/notifications/reply",
		"POST /api/v1/notifications/like",
	} {
		assert.False(t, registered[removed], "路由 %s 不应再注册", removed)
	}
}
