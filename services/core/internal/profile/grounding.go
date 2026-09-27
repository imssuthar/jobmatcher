package profile

import (
	"regexp"
	"strings"
)

var numberRe = regexp.MustCompile(`\d+(?:[.,]\d+)*`)

// Grounding measures how much of the extracted content is traceable to the
// source text. It is a cheap hallucination detector used by tests and evals.
type Grounding struct {
	NumbersTotal    int      `json:"numbers_total"`
	NumbersGrounded int      `json:"numbers_grounded"`
	Ungrounded      []string `json:"ungrounded,omitempty"`
}

// Rate is the fraction of numbers found in the source (1 when there are none).
func (g Grounding) Rate() float64 {
	if g.NumbersTotal == 0 {
		return 1
	}
	return float64(g.NumbersGrounded) / float64(g.NumbersTotal)
}

// NumberGrounding checks that every number mentioned in the facts also
// appears in the resume. Invented metrics ("improved latency by 40%") are the
// most damaging hallucination for a resume tool.
func NumberGrounding(source string, facts []string) Grounding {
	src := normalizeDigits(source)
	var g Grounding
	for _, f := range facts {
		for _, n := range numberRe.FindAllString(f, -1) {
			g.NumbersTotal++
			if strings.Contains(src, normalizeDigits(n)) {
				g.NumbersGrounded++
			} else {
				g.Ungrounded = append(g.Ungrounded, n)
			}
		}
	}
	return g
}

// normalizeDigits removes thousands separators so "1,200" matches "1200".
func normalizeDigits(s string) string {
	return strings.ReplaceAll(s, ",", "")
}
