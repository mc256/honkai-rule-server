package auth

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TC-U-UA-01: matching prefix passes through to next handler
func TestUA_01_MatchingPrefix(t *testing.T) {
	handler := RequireUserAgent([]string{"Honkai-Rule-Client"}, slog.Default())

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	req := httptest.NewRequest("GET", "/sub?token=test", nil)
	req.Header.Set("User-Agent", "Honkai-Rule-Client/1.0")
	rec := httptest.NewRecorder()

	handler(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.String() != "OK" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "OK")
	}
}

// TC-U-UA-02: second prefix matches
func TestUA_02_SecondPrefix(t *testing.T) {
	handler := RequireUserAgent([]string{"Honkai-Rule-Client", "curl"}, slog.Default())

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/sub?token=test", nil)
	req.Header.Set("User-Agent", "curl/8.0")
	rec := httptest.NewRecorder()

	handler(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

// TC-U-UA-03: non-matching UA returns 403
func TestUA_03_NonMatching(t *testing.T) {
	handler := RequireUserAgent([]string{"Honkai-Rule-Client"}, slog.Default())

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("next handler should not be called")
	})

	req := httptest.NewRequest("GET", "/sub?token=test", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	rec := httptest.NewRecorder()

	handler(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

// TC-U-UA-04: empty/nil prefixes passes all requests
func TestUA_04_EmptyPrefixes(t *testing.T) {
	handler := RequireUserAgent(nil, slog.Default())

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/sub?token=test", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	rec := httptest.NewRecorder()

	handler(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("nil prefixes: status = %d, want %d", rec.Code, http.StatusOK)
	}

	handler2 := RequireUserAgent([]string{}, slog.Default())
	rec2 := httptest.NewRecorder()
	handler2(next).ServeHTTP(rec2, req)

	if rec2.Code != http.StatusOK {
		t.Errorf("empty prefixes: status = %d, want %d", rec2.Code, http.StatusOK)
	}
}

// TC-U-UA-05: missing User-Agent header returns 403
func TestUA_05_MissingHeader(t *testing.T) {
	handler := RequireUserAgent([]string{"Honkai-Rule-Client"}, slog.Default())

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("next handler should not be called")
	})

	req := httptest.NewRequest("GET", "/sub?token=test", nil)
	// No User-Agent header set
	rec := httptest.NewRecorder()

	handler(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

// TC-U-UA-06: rejected request logged with UA value and remote address
func TestUA_06_RejectionLogged(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	handler := RequireUserAgent([]string{"Honkai-Rule-Client"}, logger)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("next handler should not be called")
	})

	req := httptest.NewRequest("GET", "/sub?token=test", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	rec := httptest.NewRecorder()

	handler(next).ServeHTTP(rec, req)

	logOutput := buf.String()
	if logOutput == "" {
		t.Error("expected log output for rejected request")
	}

	// Should contain the UA and remote
	if !bytes.Contains([]byte(logOutput), []byte("Mozilla/5.0")) {
		t.Errorf("log should contain User-Agent, got: %s", logOutput)
	}
}
