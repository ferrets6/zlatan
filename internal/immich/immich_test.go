// SPDX-License-Identifier: AGPL-3.0-or-later

package immich

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewRefusesEmptyURL(t *testing.T) {
	if _, err := New("", 0); err == nil {
		t.Error("New should refuse an empty URL")
	}
}

func TestValidateReturnsTheAccount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/users/me" {
			t.Errorf("path = %q, want /api/users/me", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "the-key" {
			t.Errorf("x-api-key = %q, want the-key", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"abc","email":"marco@example.com","name":"Marco","isAdmin":true}`))
	}))
	defer srv.Close()

	c, err := New(srv.URL, 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	me, err := c.Validate(context.Background(), "the-key")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if me.Email != "marco@example.com" || me.Name != "Marco" {
		t.Errorf("me = %+v, want Marco", me)
	}
}

func TestValidateRejectsABadKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c, _ := New(srv.URL, 0)
	if _, err := c.Validate(context.Background(), "wrong"); err == nil {
		t.Error("Validate should reject a key Immich refuses")
	}
}

func TestValidateRefusesAnEmptyKeyWithoutCallingImmich(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	c, _ := New(srv.URL, 0)
	if _, err := c.Validate(context.Background(), "   "); err == nil {
		t.Error("Validate should refuse an empty key")
	}
	if called {
		t.Error("an empty key should not reach Immich")
	}
}

func TestValidateRefusesAKeyThatNamesNoAccount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"abc","email":""}`))
	}))
	defer srv.Close()

	c, _ := New(srv.URL, 0)
	if _, err := c.Validate(context.Background(), "the-key"); err == nil {
		t.Error("a key with no account cannot file an import, so it is refused")
	}
}

func TestCredentialsStringRedactsTheKey(t *testing.T) {
	c := Credentials{APIKey: "super-secret-key", Email: "marco@example.com"}
	if got := c.String(); got == "" || strings.Contains(got, "super-secret-key") {
		t.Errorf("String() = %q, must not contain the key", got)
	}
}

func TestSealOpenRoundTrip(t *testing.T) {
	// A tiny in-package sealer: the point is that the credentials survive the
	// encode/seal path, not the cipher itself (tested in core).
	s := fakeSealer{}
	sealed, err := SealCredentials(s, Credentials{APIKey: "k", Email: "e@example.com"})
	if err != nil {
		t.Fatalf("SealCredentials: %v", err)
	}
	got, err := OpenCredentials(s, sealed)
	if err != nil {
		t.Fatalf("OpenCredentials: %v", err)
	}
	if got.APIKey != "k" || got.Email != "e@example.com" {
		t.Errorf("round trip = %+v", got)
	}
}

type fakeSealer struct{}

func (fakeSealer) Seal(b []byte) ([]byte, error) { return append([]byte(nil), b...), nil }
func (fakeSealer) Open(b []byte) ([]byte, error) { return append([]byte(nil), b...), nil }
