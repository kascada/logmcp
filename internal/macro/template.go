package macro

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// placeholderRe matches {{ identifier }} and {{ identifier.field }} patterns.
var placeholderRe = regexp.MustCompile(`\{\{\s*([a-zA-Z_][a-zA-Z0-9_]*)(?:\.([a-zA-Z_][a-zA-Z0-9_]*))?\s*\}\}`)

// interpolateString replaces all {{ param }} and {{ step_id.field }} placeholders
// in s with string values from params and stepResults.
//
// Resolution rules:
//   - {{ name }}         → params["name"] if present; else stepResults["name"] as a whole JSON value
//   - {{ step_id.field }} → look up stepResults["step_id"], extract .field;
//     if the result is an array, use the first element (array-of-one pattern);
//     if not found or null, replace with ""
//
// This function is used for non-SQL args. For db_query SQL args, use
// interpolateForSQL which returns (query, bindArgs) instead.
func interpolateString(s string, params map[string]string, stepResults map[string]any) string {
	return placeholderRe.ReplaceAllStringFunc(s, func(match string) string {
		sub := placeholderRe.FindStringSubmatch(match)
		name := sub[1]
		field := sub[2]

		if field == "" {
			// {{ name }} — check params first, then step results.
			if v, ok := params[name]; ok {
				return v
			}
			if v, ok := stepResults[name]; ok {
				return anyToString(v)
			}
			return ""
		}

		// {{ step_id.field }} — look up step result.
		raw, ok := stepResults[name]
		if !ok {
			return ""
		}
		return extractField(raw, field)
	})
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

// interpolateForSQL processes a SQL query string and extracts bind parameters.
// Every {{ ... }} placeholder in the query is replaced with a single "?" and its
// resolved value is collected into the returned args slice — preserving order.
//
// The sql string itself is NOT modified for injection; only the placeholder
// tokens are replaced with "?" and the values bound as parameters.
// This prevents SQL injection regardless of parameter content.
func interpolateForSQL(sql string, params map[string]string, stepResults map[string]any) (query string, bindArgs []any) {
	// We must iterate in order, replacing each placeholder one at a time.
	remaining := sql
	var queryBuilder strings.Builder

	for {
		loc := placeholderRe.FindStringIndex(remaining)
		if loc == nil {
			queryBuilder.WriteString(remaining)
			break
		}

		// Text before the placeholder.
		queryBuilder.WriteString(remaining[:loc[0]])
		// Replace placeholder with "?".
		queryBuilder.WriteString("?")

		// Extract the matched placeholder and resolve its value.
		matchStr := remaining[loc[0]:loc[1]]
		sub := placeholderRe.FindStringSubmatch(matchStr)
		name := sub[1]
		field := sub[2]

		var resolved any
		if field == "" {
			// {{ name }} — check params first (as string), then step results.
			if v, ok := params[name]; ok {
				resolved = v
			} else if v, ok := stepResults[name]; ok {
				resolved = v
			} else {
				resolved = ""
			}
		} else {
			// {{ step_id.field }}
			raw, ok := stepResults[name]
			if !ok {
				resolved = ""
			} else {
				resolved = extractField(raw, field)
			}
		}
		bindArgs = append(bindArgs, resolved)

		remaining = remaining[loc[1]:]
	}

	return queryBuilder.String(), bindArgs
}

// extractField retrieves a named field from a step result value.
// If v is an array, the first element is used (array-of-one CDR pattern).
// Returns "" if the field does not exist or the value is null/empty.
func extractField(v any, field string) string {
	// Unwrap array: use first element.
	// db_query returns []map[string]any; both slice types must be handled.
	switch typed := v.(type) {
	case []any:
		if len(typed) == 0 {
			return ""
		}
		v = typed[0]
	case []map[string]any:
		if len(typed) == 0 {
			return ""
		}
		v = typed[0]
	}

	// v should now be a map.
	switch m := v.(type) {
	case map[string]any:
		val, ok := m[field]
		if !ok || val == nil {
			return ""
		}
		return anyToString(val)
	}

	return ""
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
