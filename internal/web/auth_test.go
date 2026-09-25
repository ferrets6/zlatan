// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/marcodellemarche/migrate/internal/core"
)

func TestIdentityFromTrustedProxy(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "172.18.0.5:12345"
	r.Header.Set("Remote-User", "marco")
	r.Header.Set("Remote-Email", "marco@example.com")

	user, email, err := identityFrom(r, "172.18.0.0/16")
	if err != nil {
		t.Fatalf("identityFrom: %v", err)
	}
	if user != "marco" || email != "marco@example.com" {
		t.Fatalf("got %q / %q", user, email)
	}
}

// A header from outside the trusted network must not be believed: otherwise a
// container on the same Docker network could claim any identity.
func TestIdentityRefusedFromUntrustedNetwork(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.9:12345"
	r.Header.Set("Remote-User", "marco")

	if _, _, err := identityFrom(r, "172.18.0.0/16"); err == nil {
		t.Fatal("a header from an untrusted network was believed")
	}
}

// With no trusted proxy configured, nothing is believed.
func TestIdentityRefusedWhenNoTrustedProxy(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "172.18.0.5:12345"
	r.Header.Set("Remote-User", "marco")

	if _, _, err := identityFrom(r, ""); err == nil {
		t.Fatal("a header was believed with no trusted proxy configured")
	}
}

func TestIdentityRefusedWithoutHeader(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "172.18.0.5:12345"

	if _, _, err := identityFrom(r, "172.18.0.0/16"); err == nil {
		t.Fatal("a request with no Remote-User was authenticated")
	}
}

func TestFromTrustedProxyAcceptsSingleIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "172.18.0.1:5555"
	if !fromTrustedProxy(r, "172.18.0.1") {
		t.Fatal("a single-IP trusted proxy was not matched")
	}
}

func TestProxyGate(t *testing.T) {
	secret := core.Secret("shh")

	reached := false
	handler := proxyGate(secret, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))

	// Without the header: refused, and the handler is never reached.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusForbidden {
		t.Errorf("without the secret: code = %d, want 403", rec.Code)
	}
	if reached {
		t.Error("the handler ran without the proxy secret")
	}

	// With the header: allowed.
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Migrate-Proxy-Secret", "shh")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !reached {
		t.Errorf("with the secret: code = %d, reached = %v", rec.Code, reached)
	}
}

// With no secret configured the gate is a pass-through: a public bind without
// a secret is refused earlier, at config load.
func TestProxyGateWithoutSecretIsNoOp(t *testing.T) {
	handler := proxyGate("", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("code = %d, want 200", rec.Code)
	}
}
