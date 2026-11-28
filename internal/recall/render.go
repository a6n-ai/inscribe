package recall

import "strings"

func renderLine(l Line) string {
	if l.Type == HoldoutType {
		return ""
	}
	s := "[" + string(l.Type) + "] "
	if l.Repo != "" {
		s += "(" + l.Repo + ") "
	}
	if l.Unconfirmed {
		s += "(unconfirmed) "
	}
	return s + l.Text
}

// Render formats lines for injection, one per line.
func Render(lines []Line) string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if s := renderLine(l); s != "" {
			out = append(out, s)
		}
	}
	return strings.Join(out, "\n")
}

// EstimateTokens approximates model tokens as characters / 4.
func EstimateTokens(s string) int { return (len(s) + 3) / 4 }
