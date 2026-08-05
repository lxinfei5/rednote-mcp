package xiaohongshu

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOnlyNotes 固定「搜索与列表只返回笔记」这条约束。
//
// 取值取自真机走查：搜「露营」+ 图文，23 条里混着 3 条 live_v2 和 2 条 hot_query；
// 换成视频筛选，20 条笔记的 modelType 同样是 note，只是 noteCard.type 变成 video。
func TestOnlyNotes(t *testing.T) {
	t.Run("滤掉直播卡片与搜索热词", func(t *testing.T) {
		feeds := []Feed{
			{NoteID: "1", ModelType: "note", NoteCard: NoteCard{Type: "normal", DisplayTitle: "山顶露营"}},
			{NoteID: "2", ModelType: "live_v2"},
			{NoteID: "3", ModelType: "hot_query"},
			{NoteID: "4", ModelType: "note", NoteCard: NoteCard{Type: "normal", DisplayTitle: "露营日记"}},
		}

		got := onlyNotes(feeds)

		assert.Len(t, got, 2)
		assert.Equal(t, "1", got[0].NoteID)
		assert.Equal(t, "4", got[1].NoteID)
	})

	t.Run("视频笔记不被误伤", func(t *testing.T) {
		feeds := []Feed{
			{NoteID: "1", ModelType: "note", NoteCard: NoteCard{Type: "video"}},
			{NoteID: "2", ModelType: "note", NoteCard: NoteCard{Type: "normal"}},
		}

		assert.Len(t, onlyNotes(feeds), 2, "视频与图文的 modelType 同为 note，都要保留")
	})

	t.Run("无标题的笔记要保留", func(t *testing.T) {
		// 平台允许笔记没有标题，这类条目 displayTitle 为空但确实是笔记，
		// 不能跟着非笔记条目一起滤掉
		feeds := []Feed{{NoteID: "1", ModelType: "note", NoteCard: NoteCard{Type: "normal"}}}

		assert.Len(t, onlyNotes(feeds), 1)
	})

	t.Run("全是非笔记时返回空而不是 nil", func(t *testing.T) {
		got := onlyNotes([]Feed{{ModelType: "hot_query"}})

		assert.NotNil(t, got, "返回空切片，避免调用方拿到 nil 再判空")
		assert.Empty(t, got)
	})
}

func TestPublicNoteFieldNames(t *testing.T) {
	t.Run("feed accepts platform names and emits public names", func(t *testing.T) {
		var feed Feed
		require.NoError(t, json.Unmarshal([]byte(`{
			"id": "note-1",
			"xsecToken": "token-1",
			"modelType": "note",
			"noteCard": {"type": "normal"}
		}`), &feed))

		assert.Equal(t, "note-1", feed.NoteID)
		assert.Equal(t, "token-1", feed.XsecToken)

		encoded, err := json.Marshal(feed)
		require.NoError(t, err)
		assert.Contains(t, string(encoded), `"note_id":"note-1"`)
		assert.Contains(t, string(encoded), `"xsec_token":"token-1"`)
		assert.NotContains(t, string(encoded), `"id":`)
		assert.NotContains(t, string(encoded), `"xsecToken":`)
	})

	t.Run("detail and comment accept platform note ids and emit public names", func(t *testing.T) {
		var detail FeedDetail
		require.NoError(t, json.Unmarshal([]byte(`{"noteId":"note-1","xsecToken":"token-1"}`), &detail))
		assert.Equal(t, "note-1", detail.NoteID)
		assert.Equal(t, "token-1", detail.XsecToken)

		var comment Comment
		require.NoError(t, json.Unmarshal([]byte(`{"id":"comment-1","noteId":"note-1"}`), &comment))
		assert.Equal(t, "note-1", comment.NoteID)

		detailJSON, err := json.Marshal(detail)
		require.NoError(t, err)
		assert.Contains(t, string(detailJSON), `"note_id":"note-1"`)
		assert.Contains(t, string(detailJSON), `"xsec_token":"token-1"`)
		assert.NotContains(t, string(detailJSON), `"noteId":`)
		assert.NotContains(t, string(detailJSON), `"xsecToken":`)

		commentJSON, err := json.Marshal(comment)
		require.NoError(t, err)
		assert.Contains(t, string(commentJSON), `"note_id":"note-1"`)
		assert.NotContains(t, string(commentJSON), `"noteId":`)
	})
}
