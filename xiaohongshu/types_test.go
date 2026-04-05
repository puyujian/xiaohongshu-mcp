package xiaohongshu

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFeedUnmarshalStickyFromUserProfileNotes(t *testing.T) {
	raw := `[
		[
			{
				"id": "note_1",
				"index": 0,
				"xsecToken": "token_1",
				"noteCard": {
					"displayTitle": "置顶笔记",
					"interactInfo": {
						"liked": false,
						"likedCount": "12",
						"sticky": true
					}
				}
			},
			{
				"id": "note_2",
				"index": 1,
				"xsecToken": "token_2",
				"noteCard": {
					"displayTitle": "普通笔记",
					"interactInfo": {
						"liked": false,
						"likedCount": "8",
						"sticky": false
					}
				}
			}
		]
	]`

	var notes [][]Feed
	err := json.Unmarshal([]byte(raw), &notes)
	require.NoError(t, err)
	require.Len(t, notes, 1)
	require.Len(t, notes[0], 2)
	require.True(t, notes[0][0].NoteCard.InteractInfo.Sticky)
	require.False(t, notes[0][1].NoteCard.InteractInfo.Sticky)
}
