// Package linediff computes a line-by-line diff between two texts using the
// classic longest-common-subsequence algorithm. It powers the config version
// diff (F-V2-08) and is deliberately dependency-free.
package linediff

import "strings"

type Op string

const (
	OpEqual Op = "equal"
	OpAdd   Op = "add" // present only in the "to" text
	OpDel   Op = "del" // present only in the "from" text
)

// Line is one entry of a diff. OldLine/NewLine are 1-based positions in the
// respective texts, or 0 when the line is absent on that side.
type Line struct {
	Op      Op     `json:"op"`
	Text    string `json:"text"`
	OldLine int    `json:"old_line"`
	NewLine int    `json:"new_line"`
}

// Diff returns the ordered list of line operations turning "from" into "to".
func Diff(from, to string) []Line {
	a := splitLines(from)
	b := splitLines(to)

	// LCS length table: lcs[i][j] = LCS of a[i:] and b[j:].
	n, m := len(a), len(b)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	out := make([]Line, 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			out = append(out, Line{Op: OpEqual, Text: a[i], OldLine: i + 1, NewLine: j + 1})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, Line{Op: OpDel, Text: a[i], OldLine: i + 1})
			i++
		default:
			out = append(out, Line{Op: OpAdd, Text: b[j], NewLine: j + 1})
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, Line{Op: OpDel, Text: a[i], OldLine: i + 1})
	}
	for ; j < m; j++ {
		out = append(out, Line{Op: OpAdd, Text: b[j], NewLine: j + 1})
	}
	return out
}

// splitLines splits on "\n", dropping a single trailing empty line so that
// "a\nb\n" yields ["a","b"] rather than ["a","b",""]. Empty input yields no
// lines.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
