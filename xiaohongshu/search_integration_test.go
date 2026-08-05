//go:build integration

// 集成测试：起有头浏览器 + 触网 + 需登录态，默认 go test 不编译不运行。
// 手动跑：go test -tags integration ./xiaohongshu/ -run TestSearch
package xiaohongshu

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xpzouying/xiaohongshu-mcp/browser"
)

func TestSearch(t *testing.T) {
	b, err := browser.NewBrowser(false)
	require.NoError(t, err)
	defer b.Close()

	page, err := b.NewPage()
	require.NoError(t, err)
	defer func() {
		_ = page.Close()
	}()

	action := NewSearchAction(page)

	feeds, err := action.Search(context.Background(), "Kimi")
	require.NoError(t, err)
	require.NotEmpty(t, feeds, "feeds should not be empty")

	fmt.Printf("成功获取到 %d 个 Feed\n", len(feeds))

	for _, feed := range feeds {
		fmt.Printf("Note ID: %s\n", feed.NoteID)
		fmt.Printf("Feed Title: %s\n", feed.NoteCard.DisplayTitle)
	}
}

func TestSearchWithFilters(t *testing.T) {
	b, err := browser.NewBrowser(false)
	require.NoError(t, err)
	defer b.Close()

	page, err := b.NewPage()
	require.NoError(t, err)
	defer func() {
		_ = page.Close()
	}()

	action := NewSearchAction(page)

	filter := FilterOption{
		NoteType:    "图文",
		PublishTime: "一天内",
	}

	feeds, err := action.Search(context.Background(), "dn432", filter)
	require.NoError(t, err)
	require.NotEmpty(t, feeds, "feeds should not be empty")

	fmt.Printf("成功获取到 %d 个筛选后的 Feed\n", len(feeds))

	for _, feed := range feeds {
		fmt.Printf("Note ID: %s\n", feed.NoteID)
		fmt.Printf("Feed Title: %s\n", feed.NoteCard.DisplayTitle)
	}
}
