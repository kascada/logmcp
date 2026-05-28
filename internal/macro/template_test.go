package macro

import (
	"strings"
	"testing"
)

// TestInterpolateString covers {{ param }} and {{ step_id.field }} substitution,
// missing keys, and empty input.
func TestInterpolateString(t *testing.T) {
	params := map[string]string{
		"name": "alice",
		"env":  "prod",
	}
	stepResults := map[string]any{
		"step1": map[string]any{
			"host": "db.example.com",
			"port": float64(5432),
		},
		"plain": "just a string",
	}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple param substitution",
			input:    "hello {{ name }}",
			expected: "hello alice",
		},
		{
			name:     "multiple params",
			input:    "{{ name }} in {{ env }}",
			expected: "alice in prod",
		},
		{
			name:     "step field substitution",
			input:    "host={{ step1.host }}",
			expected: "host=db.example.com",
		},
		{
			name:     "step field numeric value",
			input:    "port={{ step1.port }}",
			expected: "port=5432",
		},
		{
			name:     "step result as plain string",
			input:    "val={{ plain }}",
			expected: "val=just a string",
		},
		{
			name:     "missing param and no step result",
			input:    "{{ missing }}",
			expected: "",
		},
		{
			name:     "missing step field",
			input:    "{{ step1.nonexistent }}",
			expected: "",
		},
		{
			name:     "missing step entirely",
			input:    "{{ nostep.field }}",
			expected: "",
		},
		{
			name:     "empty input",
			input:    "",
			expected: "",
		},
		{
			name:     "no placeholders",
			input:    "plain text",
			expected: "plain text",
		},
		{
			name:     "empty placeholder",
			input:    "{{ }}",
			expected: "",
		},
		{
			name:     "param takes priority over step result",
			input:    "{{ plain }}",
			expected: "just a string",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := interpolateString(tc.input, params, stepResults)
			if got != tc.expected {
				t.Errorf("interpolateString(%q) = %q; want %q", tc.input, got, tc.expected)
			}
		})
	}
}

// TestInterpolateStringInjection verifies that malicious param values are substituted
// literally — the returned string contains the attack string verbatim without any
// escaping or interpretation.
func TestInterpolateStringInjection(t *testing.T) {
	attacks := []struct {
		name  string
		value string
	}{
		{
			name:  "SQL injection via single quote",
			value: "'; DROP TABLE logs; --",
		},
		{
			name:  "SQL injection via OR",
			value: `" OR 1=1 --`,
		},
		{
			name:  "shell injection",
			value: "$(rm -rf /)",
		},
		{
			name:  "path traversal",
			value: "../../etc/passwd",
		},
	}

	for _, tc := range attacks {
		t.Run(tc.name, func(t *testing.T) {
			params := map[string]string{"input": tc.value}
			template := "SELECT * FROM logs WHERE name = '{{ input }}'"
			got := interpolateString(template, params, nil)

			// The attack string must appear verbatim in the output.
			if !strings.Contains(got, tc.value) {
				t.Errorf("expected output to contain literal attack string %q, got %q", tc.value, got)
			}

			// The placeholder itself must be gone.
			if strings.Contains(got, "{{ input }}") {
				t.Errorf("placeholder was not replaced in output %q", got)
			}
		})
	}
}

// TestInterpolateArgs verifies that map string values are interpolated and
// non-string values are passed through unchanged.
func TestInterpolateArgs(t *testing.T) {
	params := map[string]string{
		"filename": "/var/log/app.log",
	}
	stepResults := map[string]any{
		"meta": map[string]any{
			"count": float64(42),
		},
	}

	args := map[string]any{
		"path":    "{{ filename }}",
		"lines":   float64(100),
		"enabled": true,
		"label":   "count={{ meta.count }}",
		"nothing": "{{ missing }}",
	}

	got := interpolateArgs(args, params, stepResults)

	if got["path"] != "/var/log/app.log" {
		t.Errorf("path: got %q, want %q", got["path"], "/var/log/app.log")
	}
	if got["lines"] != float64(100) {
		t.Errorf("lines: got %v, want 100", got["lines"])
	}
	if got["enabled"] != true {
		t.Errorf("enabled: got %v, want true", got["enabled"])
	}
	if got["label"] != "count=42" {
		t.Errorf("label: got %q, want %q", got["label"], "count=42")
	}
	if got["nothing"] != "" {
		t.Errorf("nothing: got %q, want empty string", got["nothing"])
	}
}
