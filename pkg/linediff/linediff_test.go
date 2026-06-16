package linediff_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/open226bf/hivemind/pkg/linediff"
)

func ops(lines []linediff.Line) []linediff.Op {
	out := make([]linediff.Op, len(lines))
	for i, l := range lines {
		out[i] = l.Op
	}
	return out
}

func TestDiff_Identical(t *testing.T) {
	d := linediff.Diff("a\nb\nc", "a\nb\nc")
	assert.Equal(t, []linediff.Op{linediff.OpEqual, linediff.OpEqual, linediff.OpEqual}, ops(d))
}

func TestDiff_AddLine(t *testing.T) {
	d := linediff.Diff("a\nc", "a\nb\nc")
	assert.Equal(t, []linediff.Op{linediff.OpEqual, linediff.OpAdd, linediff.OpEqual}, ops(d))
	assert.Equal(t, "b", d[1].Text)
	assert.Equal(t, 2, d[1].NewLine)
	assert.Equal(t, 0, d[1].OldLine)
}

func TestDiff_DeleteLine(t *testing.T) {
	d := linediff.Diff("a\nb\nc", "a\nc")
	assert.Equal(t, []linediff.Op{linediff.OpEqual, linediff.OpDel, linediff.OpEqual}, ops(d))
	assert.Equal(t, "b", d[1].Text)
	assert.Equal(t, 2, d[1].OldLine)
}

func TestDiff_Replace(t *testing.T) {
	d := linediff.Diff("a\nold\nc", "a\nnew\nc")
	// A replace surfaces as a delete followed by an add.
	assert.Equal(t, []linediff.Op{linediff.OpEqual, linediff.OpDel, linediff.OpAdd, linediff.OpEqual}, ops(d))
}

func TestDiff_EmptyToContent(t *testing.T) {
	d := linediff.Diff("", "a\nb")
	assert.Equal(t, []linediff.Op{linediff.OpAdd, linediff.OpAdd}, ops(d))
}

func TestDiff_TrailingNewlineIgnored(t *testing.T) {
	d := linediff.Diff("a\nb\n", "a\nb")
	assert.Equal(t, []linediff.Op{linediff.OpEqual, linediff.OpEqual}, ops(d))
}
