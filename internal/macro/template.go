package macro

import (
	"encoding/json"
	"fmt"
	"regexp"
)

// placeholderRe matches {{ identifier }} and {{ identifier.field.subfield... }} patterns.
var placeholderRe = regexp.MustCompile(`\{\{\s*([a-zA-Z_][a-zA-Z0-9_.]*)?\s*\}\}`)

// interpolateString replaces all {{ param }} and {{ step_id.field.subfield }} placeholders
// in s with string values from params and stepResults.
//
// Resolution rules:
//   - {{ name }}                    → params["name"] if present; else stepResults["name"] as a whole JSON value
//   - {{ step_id.field.subfield }}  → look up stepResults["step_id"], traverse .field.subfield;
//     if the result is an array, use the first element (array-of-one pattern);
//     if not found or null, replace with ""
func interpolateString(s string, params map[string]string, stepResults map[string]any) string {
	return placeholderRe.ReplaceAllStringFunc(s, func(match string) string {
		sub := placeholderRe.FindStringSubmatch(match)
		if len(sub) < 2 || sub[1] == "" {
			return ""
		}
		parts := splitDot(sub[1])
		name := parts[0]
		fields := parts[1:]

		if len(fields) == 0 {
			// {{ name }} — check params first, then step results.
			if v, ok := params[name]; ok {
				return v
			}
			if v, ok := stepResults[name]; ok {
				return anyToString(v)
			}
			return ""
		}

		// {{ step_id.field... }} — look up step result and traverse fields.
		raw, ok := stepResults[name]
		if !ok {
			return ""
		}
		return extractField(raw, fields)
	})
}

// splitDot splits a dot-separated identifier into parts.
func splitDot(s string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	return append(parts, s[start:])
}

// interpolateArgs recursively interpolates all string values in an args map,
// returning a new map with resolved values. Non-string values are passed through.
// This is used for read_file and journalctl steps.
func interpolateArgs(args map[string]any, params map[string]string, stepResults map[string]any) map[string]any {
	result := make(map[string]any, len(args))
	for k, v := range args {
		switch val := v.(type) {
		case string:
			result[k] = interpolateString(val, params, stepResults)
		default:
			result[k] = v
		}
	}
	return result
}

// extractField traverses fields into a step result value, returning the
// string representation of the leaf. Supports nested paths like ["cdr", "started_at"].
// If v is an array at any level, the first element is used.
func extractField(v any, fields []string) string {
	for _, field := range fields {
		// Unwrap array: use first element.
		if typed, ok := v.([]any); ok {
			if len(typed) == 0 {
				return ""
			}
			v = typed[0]
		}
		m, ok := v.(map[string]any)
		if !ok {
			return ""
		}
		val, ok := m[field]
		if !ok || val == nil {
			return ""
		}
		v = val
	}
	return anyToString(v)
}

// anyToString converts an arbitrary value to its string representation.
// Strings are returned as-is; numbers and booleans use fmt.Sprint;
// complex values (maps, slices) are JSON-encoded.
func anyToString(v any) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case bool:
		if val {
			return "true"
		}
		return "false"
	case float64:
		// Avoid scientific notation for integers.
		if val == float64(int64(val)) {
			return fmt.Sprintf("%d", int64(val))
		}
		return fmt.Sprintf("%g", val)
	case json.Number:
		return val.String()
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(b)
	}
}
