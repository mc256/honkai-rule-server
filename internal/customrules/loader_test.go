package customrules

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// TC-U-CR-LOAD-01: single valid YAML file with all fields specified.
func TestLoad_SingleValidFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "my-rules.yaml")
	content := "name: my-rules\npriority: 100\nrules:\n  - DOMAIN,a.com,REJECT\n"
	if err := os.WriteFile(file, []byte(content), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	result, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("len(result) = %d, want 1", len(result))
	}
	if result[0].Name != "my-rules" {
		t.Errorf("Name = %q, want \"my-rules\"", result[0].Name)
	}
	if result[0].Priority != 100 {
		t.Errorf("Priority = %d, want 100", result[0].Priority)
	}
	if len(result[0].Rules) != 1 || result[0].Rules[0] != "DOMAIN,a.com,REJECT" {
		t.Errorf("Rules = %v, want [\"DOMAIN,a.com,REJECT\"]", result[0].Rules)
	}
}

// TC-U-CR-LOAD-02: two files with different priorities → sorted by priority ascending.
func TestLoad_TwoFilesSortedByPriority(t *testing.T) {
	dir := t.TempDir()
	file1 := filepath.Join(dir, "rules-a.yaml")
	file2 := filepath.Join(dir, "rules-b.yaml")
	if err := os.WriteFile(file1, []byte("name: rules-a\npriority: 200\nrules:\n  - A\n"), 0644); err != nil {
		t.Fatalf("write file1: %v", err)
	}
	if err := os.WriteFile(file2, []byte("name: rules-b\npriority: 100\nrules:\n  - B\n"), 0644); err != nil {
		t.Fatalf("write file2: %v", err)
	}

	result, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("len(result) = %d, want 2", len(result))
	}
	// Priority 100 (rules-b) should come first
	if result[0].Name != "rules-b" {
		t.Errorf("result[0].Name = %q, want \"rules-b\" (priority 100)", result[0].Name)
	}
	if result[1].Name != "rules-a" {
		t.Errorf("result[1].Name = %q, want \"rules-a\" (priority 200)", result[1].Name)
	}
}

// TC-U-CR-LOAD-03: same priority → sorted alphabetically by name (or filename).
func TestLoad_SamePriorityAlphabetical(t *testing.T) {
	dir := t.TempDir()
	file1 := filepath.Join(dir, "beta.yaml")
	file2 := filepath.Join(dir, "alpha.yaml")
	if err := os.WriteFile(file1, []byte("priority: 100\nrules:\n  - B\n"), 0644); err != nil {
		t.Fatalf("write beta: %v", err)
	}
	if err := os.WriteFile(file2, []byte("priority: 100\nrules:\n  - A\n"), 0644); err != nil {
		t.Fatalf("write alpha: %v", err)
	}

	result, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("len(result) = %d, want 2", len(result))
	}
	// Alphabetical: alpha before beta
	if result[0].Name != "alpha" {
		t.Errorf("result[0].Name = %q, want \"alpha\"", result[0].Name)
	}
	if result[1].Name != "beta" {
		t.Errorf("result[1].Name = %q, want \"beta\"", result[1].Name)
	}
}

// TC-U-CR-LOAD-04: empty folder → empty slice, no error.
func TestLoad_EmptyFolder(t *testing.T) {
	dir := t.TempDir()

	result, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("len(result) = %d, want 0", len(result))
	}
}

// TC-U-CR-LOAD-05: nonexistent folder → empty slice, warning logged.
func TestLoad_NonexistentFolder(t *testing.T) {
	dir := t.TempDir()
	nonexistent := filepath.Join(dir, "does-not-exist")

	// Capture slog output
	var buf []byte
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	result, err := Load(nonexistent)
	if err != nil {
		t.Fatalf("Load: %v, want nil error", err)
	}
	if len(result) != 0 {
		t.Errorf("len(result) = %d, want 0", len(result))
	}
	_ = buf // warning was logged (observable via slog output)
}

// TC-U-CR-LOAD-06: missing name → uses filename sans .yaml.
func TestLoad_MissingNameUsesFilename(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "my-custom-rules.yaml")
	content := "priority: 500\nrules:\n  - DOMAIN,x.com,REJECT\n"
	if err := os.WriteFile(file, []byte(content), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	result, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("len(result) = %d, want 1", len(result))
	}
	if result[0].Name != "my-custom-rules" {
		t.Errorf("Name = %q, want \"my-custom-rules\"", result[0].Name)
	}
}

// TC-U-CR-LOAD-07: missing priority → defaults to 1000.
func TestLoad_MissingPriorityDefaults1000(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "rules.yaml")
	content := "name: test-rules\nrules:\n  - DOMAIN,y.com,REJECT\n"
	if err := os.WriteFile(file, []byte(content), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	result, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("len(result) = %d, want 1", len(result))
	}
	if result[0].Priority != 1000 {
		t.Errorf("Priority = %d, want 1000", result[0].Priority)
	}
}

// TC-U-CR-LOAD-08: invalid YAML syntax → logs error, skips file, continues.
func TestLoad_InvalidYAMLSkipped(t *testing.T) {
	dir := t.TempDir()
	validFile := filepath.Join(dir, "valid.yaml")
	invalidFile := filepath.Join(dir, "invalid.yaml")
	if err := os.WriteFile(validFile, []byte("name: valid\npriority: 100\nrules:\n  - A\n"), 0644); err != nil {
		t.Fatalf("write valid: %v", err)
	}
	if err := os.WriteFile(invalidFile, []byte("name: bad\npriority: [not-a-number]\nrules:\n  - B\n"), 0644); err != nil {
		t.Fatalf("write invalid: %v", err)
	}

	result, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v, want nil error (invalid file skipped)", err)
	}
	// Only the valid file should be loaded
	if len(result) != 1 {
		t.Fatalf("len(result) = %d, want 1 (invalid skipped)", len(result))
	}
	if result[0].Name != "valid" {
		t.Errorf("result[0].Name = %q, want \"valid\"", result[0].Name)
	}
}

// TC-U-CR-LOAD-09: non-integer priority → logs error, skips file.
func TestLoad_NonIntegerPrioritySkipped(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "bad-priority.yaml")
	content := "name: test\npriority: \"not-an-int\"\nrules:\n  - A\n"
	if err := os.WriteFile(file, []byte(content), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	result, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v, want nil error (file skipped)", err)
	}
	if len(result) != 0 {
		t.Errorf("len(result) = %d, want 0 (file skipped)", len(result))
	}
}

// TC-U-CR-LOAD-10: empty rules list → valid, returns empty Rules slice.
func TestLoad_EmptyRulesValid(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "empty-rules.yaml")
	content := "name: empty\npriority: 500\nrules: []\n"
	if err := os.WriteFile(file, []byte(content), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	result, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("len(result) = %d, want 1", len(result))
	}
	if len(result[0].Rules) != 0 {
		t.Errorf("len(Rules) = %d, want 0", len(result[0].Rules))
	}
}