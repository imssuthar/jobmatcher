// Package evalkit holds the golden fixtures and scoring used by acceptance
// tests and the model evaluation, so both measure quality the same way.
package evalkit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// SearchProbe is a query whose top results must mention Expect.
type SearchProbe struct {
	Query  string `json:"query"`
	Expect string `json:"expect"`
}

// Fixture is one fictional resume and its expected extraction.
type Fixture struct {
	ID        string        `json:"id"`
	Source    string        `json:"source"`
	Upload    string        `json:"upload"`
	Name      string        `json:"name"`
	Email     string        `json:"email"`
	Skills    []string      `json:"skills"`
	Companies []string      `json:"companies"`
	FactsMin  int           `json:"facts_min"`
	FactsMax  int           `json:"facts_max"`
	Search    []SearchProbe `json:"search"`
}

// Golden is the fixtures manifest.
type Golden struct {
	Dir      string    `json:"-"`
	Fixtures []Fixture `json:"fixtures"`
}

// Load reads golden.json from dir.
func Load(dir string) (*Golden, error) {
	b, err := os.ReadFile(filepath.Join(dir, "golden.json"))
	if err != nil {
		return nil, err
	}
	g := &Golden{Dir: dir}
	return g, json.Unmarshal(b, g)
}

// SourceText returns the plain-text version of a fixture.
func (g *Golden) SourceText(f Fixture) (string, error) {
	b, err := os.ReadFile(filepath.Join(g.Dir, f.Source))
	return string(b), err
}

// UploadPath is the file uploaded in acceptance tests.
func (g *Golden) UploadPath(f Fixture) string { return filepath.Join(g.Dir, f.Upload) }

var nonAlnum = regexp.MustCompile(`[^a-z0-9+#]+`)

// Norm lowercases and strips punctuation: "Apache Spark" -> "apache spark".
func Norm(s string) string {
	return strings.TrimSpace(nonAlnum.ReplaceAllString(strings.ToLower(s), " "))
}

// Matches is a lenient comparison: "Spark" matches "Apache Spark" and
// "Spring Boot" matches "spring boot 3".
func Matches(expected, got string) bool {
	e, g := Norm(expected), Norm(got)
	if e == "" || g == "" {
		return false
	}
	return e == g || containsWord(g, e) || containsWord(e, g)
}

func containsWord(haystack, needle string) bool {
	return strings.Contains(" "+haystack+" ", " "+needle+" ")
}

// Recall is the fraction of expected items found in got, plus the misses.
func Recall(expected, got []string) (float64, []string) {
	if len(expected) == 0 {
		return 1, nil
	}
	var missing []string
	for _, e := range expected {
		found := false
		for _, g := range got {
			if Matches(e, g) {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, e)
		}
	}
	return float64(len(expected)-len(missing)) / float64(len(expected)), missing
}

// GroundedRate is the fraction of items that literally appear in the source
// text (a proxy for "not hallucinated").
func GroundedRate(source string, items []string) (float64, []string) {
	if len(items) == 0 {
		return 1, nil
	}
	src := " " + Norm(source) + " "
	var ungrounded []string
	for _, it := range items {
		if !strings.Contains(src, " "+Norm(it)+" ") {
			ungrounded = append(ungrounded, it)
		}
	}
	return float64(len(items)-len(ungrounded)) / float64(len(items)), ungrounded
}
