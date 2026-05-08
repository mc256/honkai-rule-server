package customrules

import (
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Load reads all *.yaml files from folder, parses each as a CustomRuleSet,
// applies defaults (name from filename, priority 1000), and returns them
// sorted by (Priority ascending, Name ascending).
// Returns empty slice (not error) for nonexistent or empty folder.
func Load(folder string) ([]CustomRuleSet, error) {
	entries, err := os.ReadDir(folder)
	if err != nil {
		slog.Warn("custom-rules-folder-missing",
			"event", "custom-rules-folder-missing",
			"folder", folder,
		)
		return nil, nil
	}

	var sets []CustomRuleSet

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") && !strings.HasSuffix(e.Name(), ".yml") {
			continue
		}

		path := filepath.Join(folder, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			slog.Error("custom-rules-read-error",
				"event", "custom-rules-read-error",
				"file", path,
				"error", err.Error(),
			)
			continue
			}

		var f customRuleSetFile
		if err := yaml.Unmarshal(data, &f); err != nil {
			slog.Error("custom-rules-parse-error",
				"event", "custom-rules-parse-error",
				"file", path,
				"error", err.Error(),
			)
			continue
		}

		rs := CustomRuleSet{
			Priority: 1000,
			Rules:    f.Rules,
		}
		if f.Name != nil && *f.Name != "" {
			rs.Name = *f.Name
		} else {
			// Strip .yaml or .yml extension for default name
			base := e.Name()
			rs.Name = strings.TrimSuffix(base, ".yaml")
			rs.Name = strings.TrimSuffix(rs.Name, ".yml")
		}

		if f.Priority != nil {
			rs.Priority = *f.Priority
			if rs.Priority < 0 {
				slog.Error("custom-rules-negative-priority",
					"event", "custom-rules-negative-priority",
					"file", path,
					"priority", rs.Priority,
				)
				continue
			}
		}

		if f.Rules == nil {
			rs.Rules = []string{}
		}

		sets = append(sets, rs)
	}

	sort.Slice(sets, func(i, j int) bool {
		if sets[i].Priority != sets[j].Priority {
			return sets[i].Priority < sets[j].Priority
		}
		return sets[i].Name < sets[j].Name
	})

	return sets, nil
}
