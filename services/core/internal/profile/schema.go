package profile

// JSON schemas passed to the model as response_format. Strict mode requires
// every property to be listed in "required" and additionalProperties=false.

func str() map[string]any { return map[string]any{"type": "string"} }

func strList() map[string]any {
	return map[string]any{"type": "array", "items": str()}
}

func object(props map[string]any) map[string]any {
	required := make([]string, 0, len(props))
	for k := range props {
		required = append(required, k)
	}
	return map[string]any{
		"type":                 "object",
		"properties":           props,
		"required":             required,
		"additionalProperties": false,
	}
}

// ProfileSchema describes ProfileData.
func ProfileSchema() map[string]any {
	return object(map[string]any{
		"name":                   str(),
		"email":                  str(),
		"phone":                  str(),
		"location":               str(),
		"headline":               str(),
		"summary":                str(),
		"total_years_experience": map[string]any{"type": "number"},
		"skills":                 strList(),
		"links":                  strList(),
		"experience": map[string]any{"type": "array", "items": object(map[string]any{
			"company":    str(),
			"title":      str(),
			"location":   str(),
			"start":      str(),
			"end":        str(),
			"highlights": strList(),
		})},
		"education": map[string]any{"type": "array", "items": object(map[string]any{
			"institution": str(),
			"degree":      str(),
			"field":       str(),
			"year":        str(),
		})},
	})
}

// FactsSchema describes FactsData.
func FactsSchema() map[string]any {
	categories := make([]any, len(FactCategories))
	for i, c := range FactCategories {
		categories[i] = c
	}
	return object(map[string]any{
		"facts": map[string]any{"type": "array", "items": object(map[string]any{
			"text":     str(),
			"category": map[string]any{"type": "string", "enum": categories},
			"skills":   strList(),
			"company":  str(),
			"metric":   str(),
		})},
	})
}
