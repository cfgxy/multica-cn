package legislation

// Line diff for the approval preview (dry-run patch 8): the owner must see
// the clause's full text and its placement diff before approving. The diff
// is computed on the carrier's full text so the preview cannot hide
// context. No external dependency — a bounded LCS over lines, falling back
// to "replace everything" past the cap to keep memory predictable.

// DiffLine is one rendered diff row.
type DiffLine struct {
	Kind string `json:"kind"` // "context" | "add" | "del"
	Text string `json:"text"`
}

// maxDiffLines caps the LCS table (lines × lines int32 cells). Prompt
// carriers are instruction documents — a few hundred lines typical, well
// under the cap. Past it the preview degrades to a full replace, which is
// honest (every old line deleted, every new line added) rather than wrong.
const maxDiffLines = 3000

// DiffLines computes the line diff between before and after.
func DiffLines(before, after string) []DiffLine {
	a := splitLines(before)
	b := splitLines(after)
	if len(a) > maxDiffLines || len(b) > maxDiffLines {
		out := make([]DiffLine, 0, len(a)+len(b))
		for _, l := range a {
			out = append(out, DiffLine{Kind: "del", Text: l})
		}
		for _, l := range b {
			out = append(out, DiffLine{Kind: "add", Text: l})
		}
		return out
	}

	// LCS table.
	lcs := make([][]int32, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int32, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	out := make([]DiffLine, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, DiffLine{Kind: "context", Text: a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, DiffLine{Kind: "del", Text: a[i]})
			i++
		default:
			out = append(out, DiffLine{Kind: "add", Text: b[j]})
			j++
		}
	}
	for ; i < len(a); i++ {
		out = append(out, DiffLine{Kind: "del", Text: a[i]})
	}
	for ; j < len(b); j++ {
		out = append(out, DiffLine{Kind: "add", Text: b[j]})
	}
	return out
}
