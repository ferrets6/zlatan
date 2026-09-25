// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/marcodellemarche/migrate/internal/core"
	"github.com/marcodellemarche/migrate/internal/oauth"
)

func TestStateSignerRoundTrip(t *testing.T) {
	s, err := newStateSigner(core.Secret("token-key"))
	if err != nil {
		t.Fatalf("newStateSigner: %v", err)
	}
	state := s.sign("marco")

	user, err := s.verify(state)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if user != "marco" {
		t.Fatalf("user = %q, want marco", user)
	}
}

func TestStateSignerRejectsTamperedState(t *testing.T) {
	s, _ := newStateSigner(core.Secret("token-key"))

	// Change the user in the payload but keep the original signature.
	state := s.sign("marco")
	tampered := strings.Replace(state, "marco", "federico", 1)
	if _, err := s.verify(tampered); err == nil {
		t.Fatal("a tampered state was accepted")
	}
}

func TestStateSignerRejectsAnotherKey(t *testing.T) {
	a, _ := newStateSigner(core.Secret("key-a"))
	b, _ := newStateSigner(core.Secret("key-b"))

	state := a.sign("marco")
	if _, err := b.verify(state); err == nil {
		t.Fatal("a state signed with another key was accepted")
	}
}

func TestStateSignerRejectsExpired(t *testing.T) {
	s, _ := newStateSigner(core.Secret("token-key"))
	// Freeze time at signing, then verify well past the TTL.
	s.now = func() time.Time { return time.Now().Add(-time.Hour) }
	state := s.sign("marco")

	s.now = time.Now
	if _, err := s.verify(state); err == nil {
		t.Fatal("an expired state was accepted")
	}
}

func TestStateSignerRejectsGarbage(t *testing.T) {
	s, _ := newStateSigner(core.Secret("token-key"))
	for _, bad := range []string{"", "nodots", "a.b", "a.b.c.d"} {
		if _, err := s.verify(bad); err == nil {
			t.Errorf("garbage state %q was accepted", bad)
		}
	}
}

func TestNewStateSignerRefusesEmptyKey(t *testing.T) {
	if _, err := newStateSigner(""); err == nil {
		t.Fatal("an empty key must be refused")
	}
}

// fakeGoogle is a GoogleFlow that returns canned tokens.
type fakeGoogle struct {
	tokens oauth.Tokens
	err    error
}

func (f *fakeGoogle) AuthCodeURL(state string) string {
	return "https://accounts.google.com/o/oauth2/auth?state=" + state
}

func (f *fakeGoogle) Exchange(_ context.Context, _ string) (oauth.Tokens, error) {
	return f.tokens, f.err
}

// fakeTokenStore records what was stored.
type fakeTokenStore struct {
	tokens map[string]core.Token
}

func newFakeTokenStore() *fakeTokenStore {
	return &fakeTokenStore{tokens: map[string]core.Token{}}
}

func (f *fakeTokenStore) PutToken(_ context.Context, t core.Token) error {
	f.tokens[t.User+"/"+t.Provider] = t
	return nil
}

func (f *fakeTokenStore) GetToken(_ context.Context, user, provider string) (core.Token, error) {
	t, ok := f.tokens[user+"/"+provider]
	if !ok {
		return core.Token{}, errors.New("not found")
	}
	return t, nil
}

func oauthOptions(google GoogleFlow, store *fakeTokenStore) Options {
	opts := testOptions(&fakeRunner{})
	opts.Google = google
	opts.Sealer = mustSealer()
	opts.TokenStore = store
	return opts
}

func mustSealer() *core.Sealer {
	s, err := core.NewSealer(core.Secret("token-key"))
	if err != nil {
		panic(err)
	}
	return s
}

func TestOAuthStartRedirectsToGoogle(t *testing.T) {
	opts := oauthOptions(&fakeGoogle{}, newFakeTokenStore())
	handler := Routes(opts)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request("GET", "/oauth/google/start", "marco"))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("code = %d, want 303", rec.Code)
	}
	location := rec.Header().Get("Location")
	if !strings.Contains(location, "accounts.google.com") {
		t.Errorf("redirect target = %q", location)
	}
	if !strings.Contains(location, "state=") {
		t.Errorf("the redirect carries no state: %q", location)
	}
}

func TestOAuthStartWithoutGoogleIsUnavailable(t *testing.T) {
	opts := testOptions(&fakeRunner{})
	opts.Google = nil
	handler := Routes(opts)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request("GET", "/oauth/google/start", "marco"))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d, want 503", rec.Code)
	}
}

func TestOAuthCallbackStoresSealedToken(t *testing.T) {
	store := newFakeTokenStore()
	google := &fakeGoogle{tokens: oauth.Tokens{
		AccessToken:  "ya29.access",
		RefreshToken: "1//refresh",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(time.Hour),
	}}
	opts := oauthOptions(google, store)
	handler := Routes(opts)

	// Build a valid state for marco.
	signer, _ := newStateSigner(core.Secret("token-key"))
	state := signer.sign("marco")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request("GET", "/oauth/google/callback?code=abc&state="+state, "marco"))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("code = %d, want 303 (body: %s)", rec.Code, rec.Body.String())
	}

	stored, ok := store.tokens["marco/google"]
	if !ok {
		t.Fatal("no token was stored")
	}
	if len(stored.Sealed) == 0 {
		t.Fatal("the stored token is empty")
	}
	// The sealed bytes must not contain the plaintext refresh token.
	if strings.Contains(string(stored.Sealed), "1//refresh") {
		t.Fatal("the stored token is not sealed")
	}

	// And it must open with the right key.
	opened, err := oauth.OpenJSON(mustSealer(), stored.Sealed)
	if err != nil {
		t.Fatalf("OpenJSON: %v", err)
	}
	if opened.RefreshToken != "1//refresh" {
		t.Errorf("refresh token = %q", opened.RefreshToken)
	}
}

// A state signed for one person must not complete a callback for another.
func TestOAuthCallbackRejectsAnotherUsersState(t *testing.T) {
	store := newFakeTokenStore()
	opts := oauthOptions(&fakeGoogle{}, store)
	handler := Routes(opts)

	signer, _ := newStateSigner(core.Secret("token-key"))
	state := signer.sign("federico")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request("GET", "/oauth/google/callback?code=abc&state="+state, "marco"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rec.Code)
	}
	if len(store.tokens) != 0 {
		t.Fatal("a token was stored despite the state mismatch")
	}
}

// Without a refresh token the copy would die mid-way, so the callback refuses
// rather than storing a token that cannot finish the job.
func TestOAuthCallbackRefusesWithoutRefreshToken(t *testing.T) {
	store := newFakeTokenStore()
	google := &fakeGoogle{tokens: oauth.Tokens{AccessToken: "ya29.only"}}
	opts := oauthOptions(google, store)
	handler := Routes(opts)

	signer, _ := newStateSigner(core.Secret("token-key"))
	state := signer.sign("marco")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request("GET", "/oauth/google/callback?code=abc&state="+state, "marco"))

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("code = %d, want 502", rec.Code)
	}
	if len(store.tokens) != 0 {
		t.Fatal("a token without a refresh token was stored")
	}
}

func TestOAuthCallbackReportsGoogleRefusal(t *testing.T) {
	opts := oauthOptions(&fakeGoogle{}, newFakeTokenStore())
	handler := Routes(opts)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request("GET", "/oauth/google/callback?error=access_denied", "marco"))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403", rec.Code)
	}
}
