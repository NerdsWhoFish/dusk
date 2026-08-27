// Package contextconfig parses the operator-owned session context profile.
package contextconfig

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

const (
	// Path is the reserved config-repository file holding this profile.
	Path = ".dusk/context.md"

	// DefaultBudget is the byte ceiling when no profile changes it.
	DefaultBudget = 8000

	// MaxBudget bounds how much every future session can be made to carry.
	MaxBudget = 32768
)

var sections = map[string]bool{
	"repository-notes":    true,
	"repository-entities": true,
	"estate-notes":        true,
	"inventory":           true,
}

var defaultFullNoteKinds = []string{"reference", "todo", "idea"}

// Profile controls the server-side assembly of dusk_context.
type Profile struct {
	Budget        int      `yaml:"budget,omitempty"`
	Sections      []string `yaml:"sections,omitempty"`
	Inventory     string   `yaml:"inventory,omitempty"`
	KindOrder     []string `yaml:"kind_order,omitempty"`
	FullNoteKinds []string `yaml:"full_note_kinds"`
	Instructions  string   `yaml:"-"`
}

type frontmatter struct {
	Dusk    string `yaml:"dusk"`
	Profile `yaml:",inline"`
}

// Default returns the policy used when the config repository declares none.
func Default() Profile {
	return Profile{
		Budget: DefaultBudget, Inventory: "full",
		FullNoteKinds: slices.Clone(defaultFullNoteKinds),
	}
}

// Format writes a complete profile in the same file shape Parse accepts.
func Format(profile Profile) ([]byte, error) {
	if profile.FullNoteKinds == nil {
		profile.FullNoteKinds = slices.Clone(defaultFullNoteKinds)
	}
	if err := validate(profile); err != nil {
		return nil, err
	}
	front, err := yaml.Marshal(frontmatter{Dusk: "context/v1", Profile: profile})
	if err != nil {
		return nil, fmt.Errorf("context: format frontmatter: %w", err)
	}

	var out strings.Builder
	out.WriteString("---\n")
	out.Write(front)
	out.WriteString("---\n")
	if instructions := strings.TrimSpace(profile.Instructions); instructions != "" {
		out.WriteString(instructions)
		out.WriteByte('\n')
	}
	return []byte(out.String()), nil
}

// Parse validates a complete context profile and returns its instructions.
func Parse(data []byte) (Profile, error) {
	if len(strings.TrimSpace(string(data))) == 0 {
		return Default(), nil
	}
	parts := strings.SplitN(string(data), "---", 3)
	if len(parts) != 3 || strings.TrimSpace(parts[0]) != "" {
		return Profile{}, errors.New("context: expected YAML frontmatter between --- lines")
	}
	profile, err := parseFrontmatter(parts[1])
	if err != nil {
		return Profile{}, err
	}
	profile.Instructions = strings.TrimSpace(parts[2])
	return profile, nil
}

func parseFrontmatter(data string) (Profile, error) {
	var raw frontmatter
	if err := yaml.Unmarshal([]byte(data), &raw); err != nil {
		return Profile{}, fmt.Errorf("context: frontmatter: %w", err)
	}
	if raw.Dusk != "context/v1" {
		return Profile{}, fmt.Errorf("context: dusk must be context/v1, got %q", raw.Dusk)
	}
	profile := raw.Profile
	if profile.Budget == 0 {
		profile.Budget = DefaultBudget
	}
	if profile.Inventory == "" {
		profile.Inventory = "full"
	}
	if profile.FullNoteKinds == nil {
		profile.FullNoteKinds = slices.Clone(defaultFullNoteKinds)
	}
	if err := validate(profile); err != nil {
		return Profile{}, err
	}
	return profile, nil
}

func validate(profile Profile) error {
	if profile.Budget < 1024 || profile.Budget > MaxBudget {
		return fmt.Errorf("context: budget must be between 1024 and %d bytes", MaxBudget)
	}
	if profile.Inventory != "full" && profile.Inventory != "counts" && profile.Inventory != "off" {
		return fmt.Errorf("context: inventory must be full, counts, or off, got %q", profile.Inventory)
	}
	seen := map[string]bool{}
	for _, section := range profile.Sections {
		if !sections[section] {
			return fmt.Errorf("context: unknown section %q", section)
		}
		if seen[section] {
			return fmt.Errorf("context: section %q is listed twice", section)
		}
		seen[section] = true
	}
	if err := validateFullNoteKinds(profile.FullNoteKinds); err != nil {
		return err
	}
	return nil
}

func validateFullNoteKinds(kinds []string) error {
	seen := map[string]bool{}
	for _, kind := range kinds {
		if kind == "" || kind != strings.TrimSpace(kind) {
			return fmt.Errorf("context: full note kind must be a non-empty trimmed name, got %q", kind)
		}
		if seen[kind] {
			return fmt.Errorf("context: full note kind %q is listed twice", kind)
		}
		seen[kind] = true
	}
	return nil
}
