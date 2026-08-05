package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCanonicalNoteID(t *testing.T) {
	noteID, err := canonicalNoteID("new-note", "new-note")
	require.NoError(t, err)
	assert.Equal(t, "new-note", noteID)

	noteID, err = canonicalNoteID("", "legacy-feed")
	require.NoError(t, err)
	assert.Equal(t, "legacy-feed", noteID)

	noteID, err = canonicalNoteID("", "")
	require.NoError(t, err)
	assert.Empty(t, noteID)

	_, err = canonicalNoteID("new-note", "different-note")
	assert.EqualError(t, err, "note_id and feed_id must match")
}
