package recall

import (
	"regexp"

	"github.com/a6n-ai/inscribe/model"
)

// A prompt that asks about past work gets a wider recall: candidates too, a
// lower threshold and more lines, since it rarely shares words with a memory.
const (
	intentThreshold = 0.3
	intentMaxLines  = 4
)

// Bare "before" is left out: "check cpu before pushing" is not about the past.
var intentPattern = regexp.MustCompile(`(?i)\b(recall|remember|last time|earlier|previous(ly)?|did we|have we|we (did|tried|decided|discussed|talked)|old (logs?|sessions?|chats?)|past sessions?|(like|as|from) before)\b`)

// Intent reports whether a prompt asks to recall past work.
func Intent(text string) bool { return intentPattern.MatchString(text) }

// dropCochangeCandidates removes unconfirmed file pairs. They are most of the
// candidates, and every one reads "X changes with Y", so any prompt that says
// "changes" would fill its lines with them.
func dropCochangeCandidates(hits []hit) []hit {
	out := hits[:0]
	for _, h := range hits {
		if !h.Unconfirmed || h.Type != model.Cochange {
			out = append(out, h)
		}
	}
	return out
}
