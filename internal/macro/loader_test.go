package macro

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadMacros tests YAML parsing via LoadDir, including the timeout_seconds field.
func TestLoadMacros(t *testing.T) {
	t.Run("valid macro without timeout_seconds", func(t *testing.T) {
		dir := t.TempDir()
		writeYAML(t, dir, "basic.yaml", `
name: basic_macro
description: A basic macro
steps:
  - id: step1
    internal: read_file
    args:
      path: /var/log/syslog
`)
		macros := LoadDir(dir)
		if len(macros) != 1 {
			t.Fatalf("expected 1 macro, got %d", len(macros))
		}
		m := macros[0]
		if m.Name != "basic_macro" {
			t.Errorf("name: got %q, want %q", m.Name, "basic_macro")
		}
		if m.TimeoutSeconds != 0 {
			t.Errorf("timeout_seconds: got %d, want 0", m.TimeoutSeconds)
		}
		if len(m.Steps) != 1 {
			t.Fatalf("expected 1 step, got %d", len(m.Steps))
		}
		if m.Steps[0].ID != "step1" {
			t.Errorf("step id: got %q, want %q", m.Steps[0].ID, "step1")
		}
		if m.Steps[0].Internal != "read_file" {
			t.Errorf("step internal: got %q, want %q", m.Steps[0].Internal, "read_file")
		}
	})

	t.Run("valid macro with timeout_seconds", func(t *testing.T) {
		dir := t.TempDir()
		writeYAML(t, dir, "timed.yaml", `
name: timed_macro
timeout_seconds: 120
steps:
  - id: s1
    internal: journalctl
`)
		macros := LoadDir(dir)
		if len(macros) != 1 {
			t.Fatalf("expected 1 macro, got %d", len(macros))
		}
		if macros[0].TimeoutSeconds != 120 {
			t.Errorf("timeout_seconds: got %d, want 120", macros[0].TimeoutSeconds)
		}
	})

	t.Run("invalid macro is skipped", func(t *testing.T) {
		dir := t.TempDir()
		// Valid file
		writeYAML(t, dir, "good.yaml", `
name: good_macro
steps:
  - id: s1
    internal: read_file
`)
		// Invalid file — missing name
		writeYAML(t, dir, "bad.yaml", `
steps:
  - id: s1
    internal: read_file
`)
		macros := LoadDir(dir)
		if len(macros) != 1 {
			t.Fatalf("expected 1 macro (bad skipped), got %d", len(macros))
		}
		if macros[0].Name != "good_macro" {
			t.Errorf("name: got %q, want %q", macros[0].Name, "good_macro")
		}
	})

	t.Run("non-YAML files are ignored", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("ignore me"), 0644); err != nil {
			t.Fatal(err)
		}
		writeYAML(t, dir, "valid.yaml", `
name: only_macro
steps:
  - id: s1
    internal: read_file
`)
		macros := LoadDir(dir)
		if len(macros) != 1 {
			t.Fatalf("expected 1 macro, got %d", len(macros))
		}
	})

	t.Run("empty directory returns nil", func(t *testing.T) {
		dir := t.TempDir()
		macros := LoadDir(dir)
		if len(macros) != 0 {
			t.Errorf("expected 0 macros, got %d", len(macros))
		}
	})

	t.Run("non-existent directory returns nil", func(t *testing.T) {
		macros := LoadDir("/tmp/logmcp-nonexistent-dir-xyz")
		if len(macros) != 0 {
			t.Errorf("expected 0 macros, got %d", len(macros))
		}
	})

	t.Run("empty dir argument returns nil", func(t *testing.T) {
		macros := LoadDir("")
		if len(macros) != 0 {
			t.Errorf("expected 0 macros, got %d", len(macros))
		}
	})

	t.Run("unparseable YAML is skipped", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "broken.yaml"), []byte("{ not: valid: yaml: ["), 0644); err != nil {
			t.Fatal(err)
		}
		macros := LoadDir(dir)
		if len(macros) != 0 {
			t.Errorf("expected 0 macros (broken YAML skipped), got %d", len(macros))
		}
	})
}

// TestValidateMacro tests what validate() checks in loader.go.
func TestValidateMacro(t *testing.T) {
	t.Run("missing name returns error", func(t *testing.T) {
		m := MacroDef{
			Steps: []StepDef{{ID: "s1", Internal: "read_file"}},
		}
		if err := m.validate(); err == nil {
			t.Error("expected error for missing name, got nil")
		}
	})

	t.Run("empty steps returns error", func(t *testing.T) {
		m := MacroDef{
			Name:  "test_macro",
			Steps: []StepDef{},
		}
		if err := m.validate(); err == nil {
			t.Error("expected error for empty steps, got nil")
		}
	})

	t.Run("nil steps returns error", func(t *testing.T) {
		m := MacroDef{
			Name: "test_macro",
		}
		if err := m.validate(); err == nil {
			t.Error("expected error for nil steps, got nil")
		}
	})

	t.Run("step missing internal returns error", func(t *testing.T) {
		m := MacroDef{
			Name:  "test_macro",
			Steps: []StepDef{{ID: "s1"}},
		}
		if err := m.validate(); err == nil {
			t.Error("expected error for step missing internal, got nil")
		}
	})

	t.Run("step missing id returns error", func(t *testing.T) {
		m := MacroDef{
			Name:  "test_macro",
			Steps: []StepDef{{Internal: "read_file"}},
		}
		if err := m.validate(); err == nil {
			t.Error("expected error for step missing id, got nil")
		}
	})

	t.Run("valid definition returns no error", func(t *testing.T) {
		m := MacroDef{
			Name: "test_macro",
			Steps: []StepDef{
				{ID: "s1", Internal: "read_file"},
				{ID: "s2", Internal: "journalctl"},
			},
		}
		if err := m.validate(); err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
	})
}

// writeYAML is a helper that writes content to filename in dir.
func writeYAML(t *testing.T, dir, filename, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(content), 0644); err != nil {
		t.Fatalf("writeYAML: %v", err)
	}
}
