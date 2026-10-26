// Package check grades typed answers.
package check

import (
	"strings"
	"unicode"
)

// Verdict of a typed answer.
type Verdict string

const (
	Correct Verdict = "correct"
	Close   Verdict = "close" // missing umlauts or a small typo
	Wrong   Verdict = "wrong"
)

// Result of checking an answer.
type Result struct {
	Verdict  Verdict `json:"verdict"`
	Expected string  `json:"expected"` // closest accepted answer
	Reason   string  `json:"reason,omitempty"`
}

// Answer compares given against the accepted answers. english enables leniency for
// English glosses (leading "to"/"a"/"the", parenthesised parts).
func Answer(given string, accepted []string, english bool) Result {
	g := normalize(given, english)
	best := Result{Verdict: Wrong}
	if len(accepted) > 0 {
		best.Expected = accepted[0]
	}
	if g == "" {
		best.Reason = "empty"
		return best
	}
	bestDist := -1
	for _, a := range accepted {
		for _, variant := range variants(a, english) {
			if g == variant {
				return Result{Verdict: Correct, Expected: a}
			}
			if fold(g) == fold(variant) {
				best = Result{Verdict: Close, Expected: a, Reason: "check the accents"}
				bestDist = 0
				continue
			}
			d := levenshtein(g, variant)
			if d <= tolerance(variant) && (bestDist < 0 || d < bestDist) {
				best = Result{Verdict: Close, Expected: a, Reason: "typo"}
				bestDist = d
			}
		}
	}
	return best
}

// Rating suggested for a verdict, on FSRS's 1–4 scale.
func (v Verdict) Rating() int {
	switch v {
	case Correct:
		return 3
	case Close:
		return 2
	}
	return 1
}

func tolerance(s string) int {
	n := len([]rune(s))
	switch {
	case n >= 14:
		return 2
	case n >= 5:
		return 1
	}
	return 0
}

func variants(answer string, english bool) []string {
	out := []string{normalize(answer, english)}
	if english && strings.Contains(answer, "(") {
		// "(to) take care" also accepts "take care" and "to take care".
		stripped := strings.NewReplacer("(", "", ")", "").Replace(answer)
		out = append(out, normalize(stripped, english), normalize(removeParens(answer), english))
	}
	return out
}

func removeParens(s string) string {
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch {
		case r == '(':
			depth++
		case r == ')':
			if depth > 0 {
				depth--
			}
		case depth == 0:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func normalize(s string, english bool) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.NewReplacer("’", "'", "‘", "'", "“", "\"", "”", "\"", "–", "-", "—", "-").Replace(s)
	s = strings.TrimRightFunc(s, func(r rune) bool { return r == '.' || r == '!' || r == '?' || unicode.IsSpace(r) })
	s = strings.Join(strings.Fields(strings.ReplaceAll(s, ",", " ,")), " ")
	s = strings.ReplaceAll(s, " ,", ",")
	if english {
		for _, p := range []string{"to ", "a ", "an ", "the "} {
			s = strings.TrimPrefix(s, p)
		}
	}
	return s
}

// accents maps letters with diacritics to their base letters, so "cafe" for "café" or
// "espanol" for "español" counts as almost right, in any Latin-script language.
var accents = strings.NewReplacer(
	"à", "a", "á", "a", "â", "a", "ã", "a", "ä", "a", "å", "a", "ā", "a", "ą", "a",
	"ç", "c", "ć", "c", "č", "c", "ď", "d", "đ", "d",
	"è", "e", "é", "e", "ê", "e", "ë", "e", "ē", "e", "ę", "e", "ě", "e",
	"ì", "i", "í", "i", "î", "i", "ï", "i", "ī", "i", "ł", "l", "ľ", "l",
	"ñ", "n", "ń", "n", "ň", "n",
	"ò", "o", "ó", "o", "ô", "o", "õ", "o", "ö", "o", "ø", "o", "ō", "o", "ő", "o",
	"ř", "r", "ś", "s", "š", "s", "ş", "s", "ß", "ss", "ť", "t",
	"ù", "u", "ú", "u", "û", "u", "ü", "u", "ū", "u", "ů", "u", "ű", "u",
	"ý", "y", "ÿ", "y", "ź", "z", "ż", "z", "ž", "z",
	"æ", "ae", "œ", "oe",
)

func fold(s string) string { return accents.Replace(s) }

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}
