package macro

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ParamDef describes a single macro parameter.
type ParamDef struct {
	Type        string `yaml:"type"`
	Optional    bool   `yaml:"optional"`
	Description string `yaml:"description"`
}

// StepDef describes one step within a macro.
type StepDef struct {
	// Internal is the name of an internal step type (read_file, journalctl).
	Internal string `yaml:"internal"`
	// ID is the unique identifier for this step; used as key in the result object.
	ID string `yaml:"id"`
	// Args holds step-specific parameters as parsed from YAML; concrete types depend on the step kind.
	Args map[string]any `yaml:"args"`
}

// MacroDef is the parsed representation of a macro YAML file.
type MacroDef struct {
	// Name is the MCP tool name this macro is registered as.
	Name        string               `yaml:"name"`
	Description string               `yaml:"description"`
	Parameters  map[string]ParamDef  `yaml:"parameters"`
	Steps       []StepDef            `yaml:"steps"`
	// TimeoutSeconds overrides the per-step timeout for this macro.
	// 0 means "use the default" (30 seconds).
	TimeoutSeconds int `yaml:"timeout_seconds"`
}

// validate checks that a MacroDef has all required fields and that each step
// has the minimum required attributes.
func (m *MacroDef) validate() error {
	if m.Name == "" {
		return fmt.Errorf("missing required field 'name'")
	}
	if len(m.Steps) == 0 {
		return fmt.Errorf("missing required field 'steps' (must not be empty)")
	}
	for i, s := range m.Steps {
		if s.Internal == "" {
			return fmt.Errorf("steps[%d]: missing 'internal' field", i)
		}
		if s.ID == "" {
			return fmt.Errorf("steps[%d]: missing 'id' field", i)
		}
	}
	return nil
}

// LoadDir reads all *.yaml files from dir and parses them as MacroDefs.
// Files that fail to parse are logged to stderr and skipped; the function
// returns all successfully parsed macros and never returns an error itself.
// If dir is empty or does not exist, an empty slice is returned.
func LoadDir(dir string) []MacroDef {
	if dir == "" {
		return nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "logmcp macro: reading macros dir %q: %v\n", dir, err)
		}
		return nil
	}

	var macros []MacroDef
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if filepath.Ext(name) != ".yaml" && filepath.Ext(name) != ".yml" {
			continue
		}

		path := filepath.Join(dir, name)
		m, err := loadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "logmcp macro: skipping %s: %v\n", path, err)
			continue
		}
		macros = append(macros, *m)
	}
	return macros
}

// loadFile parses a single macro YAML file.
func loadFile(path string) (*MacroDef, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}

	var m MacroDef
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing YAML: %w", err)
	}

	if err := m.validate(); err != nil {
		return nil, err
	}

	return &m, nil
}
