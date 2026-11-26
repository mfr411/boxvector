package web

import "strings"

// DiffLine is one line of a unified-style diff.
type DiffLine struct {
	Op   byte // ' ', '+', '-', '@' (hunk separator)
	Text string
}

// Diff returns a line diff of a -> b with ctx lines of context (LCS based;
// config files are small so O(n*m) is fine, capped to keep memory bounded).
func Diff(a, b string, ctx int) []DiffLine {
	x, y := strings.Split(a, "\n"), strings.Split(b, "\n")
	if len(x)*len(y) > 25_000_000 {
		return []DiffLine{{'@', "files too large to diff"}}
	}
	n, m := len(x), len(y)
	lcs := make([][]int32, n+1)
	for i := range lcs {
		lcs[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if x[i] == y[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	var all []DiffLine
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && x[i] == y[j]:
			all = append(all, DiffLine{' ', x[i]})
			i++
			j++
		case j < m && (i == n || lcs[i][j+1] > lcs[i+1][j]): // deletions first, like diff -u
			all = append(all, DiffLine{'+', y[j]})
			j++
		default:
			all = append(all, DiffLine{'-', x[i]})
			i++
		}
	}
	// keep only changed lines plus context
	keep := make([]bool, len(all))
	changed := false
	for k, l := range all {
		if l.Op != ' ' {
			changed = true
			for c := k - ctx; c <= k+ctx; c++ {
				if c >= 0 && c < len(all) {
					keep[c] = true
				}
			}
		}
	}
	if !changed {
		return nil
	}
	var out []DiffLine
	gap := false
	for k, l := range all {
		if keep[k] {
			if gap && len(out) > 0 {
				out = append(out, DiffLine{'@', "…"})
			}
			out = append(out, l)
			gap = false
		} else {
			gap = true
		}
	}
	return out
}
