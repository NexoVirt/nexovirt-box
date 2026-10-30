package catalog

// Stacks: several catalog apps installed together as one bundle (for example a media stack).
// They are written by hand in catalog/stacks.json and copied into the signed index.json by
// WriteDir, so a Box only ever installs stacks NexoVirt signed.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type StackApp struct {
	ID      string            `json:"id"`
	Answers map[string]string `json:"answers,omitempty"` // install answers the stack sets (no secrets)
}

type Stack struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Notes       string     `json:"notes,omitempty"` // how the apps are wired together (shown after the install)
	Apps        []StackApp `json:"apps"`
}

var stackKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

// ValidateStacks checks the stacks against the apps of the catalog.
func ValidateStacks(stacks []Stack, known func(id string) bool) error {
	seen := map[string]bool{}
	for _, s := range stacks {
		if !IDRe.MatchString(s.ID) || seen[s.ID] || strings.TrimSpace(s.Name) == "" || len(s.Name) > 60 || len(s.Description) > 400 || len(s.Notes) > 4000 {
			return fmt.Errorf("stack %q: bad id, name or text", s.ID)
		}
		seen[s.ID] = true
		if len(s.Apps) < 2 || len(s.Apps) > 12 {
			return fmt.Errorf("stack %s: 2 to 12 apps", s.ID)
		}
		for _, a := range s.Apps {
			if !known(a.ID) {
				return fmt.Errorf("stack %s: app %s is not in the catalog", s.ID, a.ID)
			}
			for k, v := range a.Answers {
				if !stackKeyRe.MatchString(k) || strings.ContainsAny(v, "'\r\n\x00") || len(v) > 200 {
					return fmt.Errorf("stack %s: bad answer %s for %s", s.ID, k, a.ID)
				}
			}
		}
	}
	return nil
}

// keepKnown drops stack apps the catalog no longer has (an image check removed them) and stacks
// that are left with fewer than two apps.
func keepKnown(stacks []Stack, have map[string]bool) []Stack {
	var out []Stack
	for _, s := range stacks {
		var apps []StackApp
		for _, a := range s.Apps {
			if have[a.ID] {
				apps = append(apps, a)
			} else {
				fmt.Fprintf(os.Stderr, "stack %s: %s is no longer in the catalog, left out\n", s.ID, a.ID)
			}
		}
		if len(apps) >= 2 {
			s.Apps = apps
			out = append(out, s)
		}
	}
	return out
}

// readStacks loads out/stacks.json (optional).
func readStacks(out string) ([]Stack, error) {
	b, err := os.ReadFile(filepath.Join(out, "stacks.json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s []Stack
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("stacks.json: %w", err)
	}
	return s, nil
}

// Stacks returns the signed stacks of the catalog.
func (c *Catalog) Stacks() []Stack { return c.Index.Stacks }
