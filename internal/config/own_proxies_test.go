package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixturesOwnProxiesYAML = "../integration/testdata/fixtures/own-proxies.yaml"

func writeOwnProxies(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "own.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TC-U-OWN-01: loads the committed fixture (2 proxies + 1 group).
func TestOWN_01_LoadsFixture(t *testing.T) {
	doc, err := LoadOwnProxies(fixturesOwnProxiesYAML)
	if err != nil {
		t.Fatalf("LoadOwnProxies: %v", err)
	}
	if len(doc.Proxies) != 2 {
		t.Errorf("Proxies count = %d, want 2", len(doc.Proxies))
	}
	if len(doc.ProxyGroups) != 1 {
		t.Errorf("ProxyGroups count = %d, want 1", len(doc.ProxyGroups))
	}
}

// TC-U-OWN-02: missing required field on a proxy → error naming the entry.
func TestOWN_02_MissingProxyField(t *testing.T) {
	cases := []struct {
		name        string
		yaml        string
		wantField   string
	}{
		{"missing name", "proxies:\n  - {type: trojan, server: a.test, port: 443}\nproxy-groups: []\n", "name"},
		{"missing type", "proxies:\n  - {name: x, server: a.test, port: 443}\nproxy-groups: []\n", "type"},
		{"missing server", "proxies:\n  - {name: x, type: trojan, port: 443}\nproxy-groups: []\n", "server"},
		{"port zero", "proxies:\n  - {name: x, type: trojan, server: a.test, port: 0}\nproxy-groups: []\n", "port"},
		{"port too large", "proxies:\n  - {name: x, type: trojan, server: a.test, port: 99999}\nproxy-groups: []\n", "port"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := writeOwnProxies(t, c.yaml)
			_, err := LoadOwnProxies(p)
			var vErr *OwnProxyValidationError
			if !errors.As(err, &vErr) {
				t.Fatalf("error type = %T, want *OwnProxyValidationError; err=%v", err, err)
			}
			if vErr.Field != c.wantField {
				t.Errorf("Field = %q, want %q", vErr.Field, c.wantField)
			}
		})
	}
}

// TC-U-OWN-03: duplicate proxy name within file → error.
func TestOWN_03_DuplicateProxyName(t *testing.T) {
	p := writeOwnProxies(t, `proxies:
  - {name: dup, type: trojan, server: a.test, port: 443}
  - {name: dup, type: vless, server: b.test, port: 443}
proxy-groups: []
`)
	_, err := LoadOwnProxies(p)
	var vErr *OwnProxyValidationError
	if !errors.As(err, &vErr) || vErr.Field != "name" {
		t.Fatalf("err = %v (Field=%q), want *OwnProxyValidationError on name", err, vErr.Field)
	}
	if !strings.Contains(vErr.Reason, "duplicate") {
		t.Errorf("Reason = %q, want contains 'duplicate'", vErr.Reason)
	}
}

// TC-U-OWN-04: group references an empty proxy name → error.
func TestOWN_04_GroupReferencesEmpty(t *testing.T) {
	p := writeOwnProxies(t, `proxies:
  - {name: A, type: trojan, server: a.test, port: 443}
proxy-groups:
  - {name: G, type: select, proxies: [A, ""]}
`)
	_, err := LoadOwnProxies(p)
	var vErr *OwnProxyValidationError
	if !errors.As(err, &vErr) || vErr.Field != "proxies" {
		t.Fatalf("err = %v, want validation error on group proxies field", err)
	}
}

// TC-U-OWN-05: empty arrays are valid (operator has no own-proxies yet).
func TestOWN_05_EmptyArraysValid(t *testing.T) {
	p := writeOwnProxies(t, "proxies: []\nproxy-groups: []\n")
	doc, err := LoadOwnProxies(p)
	if err != nil {
		t.Fatalf("LoadOwnProxies: %v", err)
	}
	if len(doc.Proxies) != 0 || len(doc.ProxyGroups) != 0 {
		t.Errorf("got %d proxies, %d groups, want 0/0", len(doc.Proxies), len(doc.ProxyGroups))
	}
}

func TestOwnProxies_DuplicateGroupName(t *testing.T) {
	p := writeOwnProxies(t, `proxies:
  - {name: A, type: trojan, server: a.test, port: 443}
proxy-groups:
  - {name: G, type: select, proxies: [A]}
  - {name: G, type: url-test, proxies: [A]}
`)
	_, err := LoadOwnProxies(p)
	var vErr *OwnProxyValidationError
	if !errors.As(err, &vErr) || vErr.Field != "name" {
		t.Fatalf("err = %v, want validation error on group name", err)
	}
}

func TestOwnProxies_MissingFile(t *testing.T) {
	_, err := LoadOwnProxies("/no/such/file.yaml")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestOwnProxies_MalformedYAML(t *testing.T) {
	p := writeOwnProxies(t, "not: [valid {yaml}")
	_, err := LoadOwnProxies(p)
	if err == nil {
		t.Fatal("expected parse error")
	}
}
