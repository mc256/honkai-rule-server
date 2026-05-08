package observability

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// TC-U-TOK-05: SanitizeToken produces sha256:<12-hex> from any token.
func TestSanitizeToken_Format(t *testing.T) {
	cases := []string{
		"valid-test-token-aaaaaaaaaaaaaaaaaaaaaaaaaaaaa1",
		"x",
		"some-other-token",
	}
	for _, in := range cases {
		got := SanitizeToken(in)
		if !strings.HasPrefix(got, "sha256:") {
			t.Errorf("SanitizeToken(%q) = %q, missing sha256: prefix", in, got)
		}
		// "sha256:" (7 chars) + 12 hex chars = 19 total
		if len(got) != 19 {
			t.Errorf("SanitizeToken(%q) length = %d, want 19", in, len(got))
		}
	}
}

func TestSanitizeToken_Deterministic(t *testing.T) {
	const tok = "valid-test-token-aaaaaaaaaaaaaaaaaaaaaaaaaaaaa1"
	a := SanitizeToken(tok)
	b := SanitizeToken(tok)
	if a != b {
		t.Errorf("SanitizeToken not deterministic: %q vs %q", a, b)
	}
}

func TestSanitizeToken_DifferentInputsDifferentOutputs(t *testing.T) {
	a := SanitizeToken("token-a")
	b := SanitizeToken("token-b")
	if a == b {
		t.Errorf("SanitizeToken collided: both inputs yielded %q", a)
	}
}

func TestSanitizeToken_Empty(t *testing.T) {
	got := SanitizeToken("")
	if got != "sha256:empty" {
		t.Errorf("SanitizeToken(\"\") = %q, want sha256:empty", got)
	}
}

// Sanity check: the logger doesn't accidentally include token plaintext when
// callers do the right thing (use SanitizeToken).
func TestNew_LoggerOutputDoesNotLeakToken(t *testing.T) {
	const tok = "supersecret-must-not-appear-in-logs"
	var buf bytes.Buffer
	log := New(slog.LevelDebug, &buf)
	log.Info("auth ok", "token_hash", SanitizeToken(tok))
	out := buf.String()
	if strings.Contains(out, tok) {
		t.Errorf("logger output leaked token plaintext: %s", out)
	}
	if !strings.Contains(out, "sha256:") {
		t.Errorf("logger output missing token_hash field: %s", out)
	}
}
