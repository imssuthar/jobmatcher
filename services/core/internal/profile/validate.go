package profile

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

var (
	emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
	spaceRe = regexp.MustCompile(`\s+`)
)

// ValidationError lists every problem found, so the model can fix them all
// in one retry.
type ValidationError struct{ Problems []string }

func (e *ValidationError) Error() string {
	return "invalid output: " + strings.Join(e.Problems, "; ")
}

// Normalize cleans model output in place: trims strings, drops empties and
// de-duplicates skills case-insensitively.
func (p *ProfileData) Normalize() {
	p.Name = clean(p.Name)
	p.Email = strings.ToLower(clean(p.Email))
	p.Phone = clean(p.Phone)
	p.Location = clean(p.Location)
	p.Headline = clean(p.Headline)
	p.Summary = clean(p.Summary)
	p.Skills = dedupe(p.Skills)
	p.Links = dedupe(p.Links)
	exp := p.Experience[:0]
	for _, e := range p.Experience {
		e.Company, e.Title, e.Location = clean(e.Company), clean(e.Title), clean(e.Location)
		e.Start, e.End = clean(e.Start), clean(e.End)
		e.Highlights = dedupe(e.Highlights)
		if e.Company != "" || e.Title != "" {
			exp = append(exp, e)
		}
	}
	p.Experience = exp
	edu := p.Education[:0]
	for _, e := range p.Education {
		e.Institution, e.Degree, e.Field, e.Year = clean(e.Institution), clean(e.Degree), clean(e.Field), clean(e.Year)
		if e.Institution != "" || e.Degree != "" {
			edu = append(edu, e)
		}
	}
	p.Education = edu
}

// Validate checks the profile is complete and grounded in the source text:
// name and email must literally appear in the resume, which catches the most
// common hallucination (a made-up contact).
func (p *ProfileData) Validate(source string) error {
	var probs []string
	lower := strings.ToLower(source)
	switch {
	case len(p.Name) < 2 || len(p.Name) > 100:
		probs = append(probs, "name is required (2-100 characters)")
	case !containsAllWords(lower, p.Name):
		probs = append(probs, fmt.Sprintf("name %q does not appear in the resume text", p.Name))
	}
	if p.Email != "" {
		if !emailRe.MatchString(p.Email) {
			probs = append(probs, fmt.Sprintf("email %q is not a valid address", p.Email))
		} else if !strings.Contains(lower, p.Email) {
			probs = append(probs, fmt.Sprintf("email %q does not appear in the resume text; use \"\" if there is none", p.Email))
		}
	}
	if len(p.Skills) == 0 {
		probs = append(probs, "skills must list at least one skill from the resume")
	}
	if p.TotalYearsExperience < 0 || p.TotalYearsExperience > 60 {
		probs = append(probs, "total_years_experience must be between 0 and 60")
	}
	for i, e := range p.Experience {
		if e.Company == "" || e.Title == "" {
			probs = append(probs, fmt.Sprintf("experience[%d] needs both company and title", i))
		}
	}
	if len(probs) > 0 {
		return &ValidationError{Problems: probs}
	}
	return nil
}

// Normalize cleans facts in place and removes duplicates.
func (f *FactsData) Normalize() {
	seen := map[string]bool{}
	out := f.Facts[:0]
	for _, it := range f.Facts {
		it.Text = clean(it.Text)
		it.Category = strings.ToLower(clean(it.Category))
		it.Company = clean(it.Company)
		it.Metric = clean(it.Metric)
		it.Skills = dedupe(it.Skills)
		key := strings.ToLower(it.Text)
		if it.Text == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, it)
	}
	f.Facts = out
}

// Validate checks there are enough well-formed facts.
func (f *FactsData) Validate() error {
	var probs []string
	if len(f.Facts) < 3 {
		probs = append(probs, fmt.Sprintf("expected at least 3 facts, got %d", len(f.Facts)))
	}
	for i, it := range f.Facts {
		if n := len(it.Text); n < 15 || n > 500 {
			probs = append(probs, fmt.Sprintf("facts[%d].text must be 15-500 characters (got %d)", i, n))
		}
		if !slices.Contains(FactCategories, it.Category) {
			probs = append(probs, fmt.Sprintf("facts[%d].category %q must be one of %v", i, it.Category, FactCategories))
		}
	}
	if len(probs) > 0 {
		return &ValidationError{Problems: probs}
	}
	return nil
}

func clean(s string) string { return strings.TrimSpace(spaceRe.ReplaceAllString(s, " ")) }

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		s = clean(s)
		k := strings.ToLower(s)
		if s == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, s)
	}
	return out
}

func containsAllWords(lowerText, phrase string) bool {
	for _, w := range strings.Fields(strings.ToLower(phrase)) {
		if !strings.Contains(lowerText, strings.Trim(w, ".,")) {
			return false
		}
	}
	return true
}
