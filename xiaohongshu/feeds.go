package xiaohongshu

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-rod/rod"
	"github.com/xpzouying/xiaohongshu-mcp/errors"
)

type FeedsListAction struct {
	page *rod.Page
}

func NewFeedsListAction(page *rod.Page) *FeedsListAction {
	pp := page.Timeout(30 * time.Second)

	pp.MustNavigate("https://www.xiaohongshu.com")
	// MustWaitDOMStable 等待 load 事件 + 网络空闲 + DOM 稳定，是 SPA 页面的正确等待方式。
	// MustWaitLoad 仅等 load 事件，此时 React 尚未水合，__INITIAL_STATE__ 大概率未就绪；
	// 后续高频 polling MustEval 会产生反 Bot 系统可检测的自动化特征。
	pp.MustWaitDOMStable()

	return &FeedsListAction{page: pp}
}

// GetFeedsList 获取页面的 Feed 列表数据
func (f *FeedsListAction) GetFeedsList(ctx context.Context) ([]Feed, error) {
	page := f.page.Context(ctx)

	// DOM 已稳定，大概率一次成功；仅失败时做有限重试（长间隔，模拟人类等待）
	result := page.MustEval(`() => {
		if (window.__INITIAL_STATE__ &&
		    window.__INITIAL_STATE__.feed &&
		    window.__INITIAL_STATE__.feed.feeds) {
			const feeds = window.__INITIAL_STATE__.feed.feeds;
			const feedsData = feeds.value !== undefined ? feeds.value : feeds._value;
			if (feedsData && feedsData.length > 0) {
				return JSON.stringify(feedsData);
			}
		}
		return "";
	}`).String()

	// 意外失败时做有限次重试（最多 2 次，间隔 ≥ 1.2s），避免高频 polling 触发风控
	for retry := 0; retry < 2 && result == ""; retry++ {
		time.Sleep(1200 * time.Millisecond)
		result = page.MustEval(`() => {
			if (window.__INITIAL_STATE__ &&
			    window.__INITIAL_STATE__.feed &&
			    window.__INITIAL_STATE__.feed.feeds) {
				const feeds = window.__INITIAL_STATE__.feed.feeds;
				const feedsData = feeds.value !== undefined ? feeds.value : feeds._value;
				if (feedsData && feedsData.length > 0) {
					return JSON.stringify(feedsData);
				}
			}
			return "";
		}`).String()
	}

	if result == "" {
		return nil, errors.ErrNoFeeds
	}

	var feeds []Feed
	if err := json.Unmarshal([]byte(result), &feeds); err != nil {
		return nil, fmt.Errorf("failed to unmarshal feeds: %w", err)
	}

	return feeds, nil
}
