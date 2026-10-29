package box

import (
	"regexp"
	"strings"
	"unicode"
)

// Appearance per user: a preset plus the owner's tweaks, and named themes the owner saved.
// Only known values pass; colours must be #rrggbb. The UI turns this into CSS tokens.

type themeCore struct {
	Preset  string `json:"preset"`  // nexovirt, graphite, midnight, forest, sand, rose, contrast
	Accent  string `json:"accent"`  // #rrggbb, "" = the preset's accent
	Neutral string `json:"neutral"` // cool, neutral, warm
	Surface string `json:"surface"` // solid, glass
	Radius  string `json:"radius"`  // small, medium, large
	Density string `json:"density"` // comfortable, compact
	Scale   int    `json:"scale"`   // font size in percent, 85..125
}

type namedTheme struct {
	Name string `json:"name"`
	themeCore
}

type themePrefs struct {
	themeCore
	Saved []namedTheme `json:"saved"`
}

var (
	hexColour  = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	themeEnums = map[string]map[string]bool{
		"preset":  {"nexovirt": true, "graphite": true, "midnight": true, "forest": true, "sand": true, "rose": true, "contrast": true},
		"neutral": {"cool": true, "neutral": true, "warm": true},
		"surface": {"solid": true, "glass": true},
		"radius":  {"small": true, "medium": true, "large": true},
		"density": {"comfortable": true, "compact": true},
	}
)

func (t themeCore) validate() error {
	if !themeEnums["preset"][t.Preset] || !themeEnums["neutral"][t.Neutral] || !themeEnums["surface"][t.Surface] ||
		!themeEnums["radius"][t.Radius] || !themeEnums["density"][t.Density] {
		return userError{"The theme has an unknown value."}
	}
	if t.Accent != "" && !hexColour.MatchString(t.Accent) {
		return userError{"The accent colour must look like #2563eb."}
	}
	if t.Scale < 85 || t.Scale > 125 {
		return userError{"The text size goes from 85 to 125 %."}
	}
	return nil
}

func (t *themePrefs) validate() error {
	if err := t.themeCore.validate(); err != nil {
		return err
	}
	if len(t.Saved) > 20 {
		return userError{"You can keep up to 20 own themes."}
	}
	names := map[string]bool{}
	for _, n := range t.Saved {
		name := strings.TrimSpace(n.Name)
		if name == "" || len([]rune(name)) > 40 || name != n.Name || strings.IndexFunc(name, unicode.IsControl) >= 0 {
			return userError{"A theme name has 1 to 40 characters."}
		}
		if names[strings.ToLower(name)] {
			return userError{"Two themes have the same name: " + name}
		}
		names[strings.ToLower(name)] = true
		if err := n.themeCore.validate(); err != nil {
			return err
		}
	}
	return nil
}
