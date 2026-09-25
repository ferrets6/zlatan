// SPDX-License-Identifier: AGPL-3.0-or-later

package oauth

import (
	"strings"
	"testing"
	"time"

	"github.com/marcodellemarche/migrate/internal/core"
)

func TestNewRefusesIncompleteClient(t *testing.T) {
	cases := []struct {
		name   string
		id     core.Secret
		secret core.Secret
		url    string
	}{
		{"no id", "", "secret", "https://x/cb"},
		{"no secret", "id", "", "https://x/cb"},
		{"no url", "id", "secret", ""},
	}
	for _, c := range cases {
		if _, err := New(c.id, c.secret, c.url); err == nil {
			t.Errorf("%s: expected an error", c.name)
		}
	}
}

// The consent URL must request offline access and force the consent screen:
// without both, Google omits the refresh token and an overnight copy dies.
func TestAuthCodeURLRequestsOfflineAndConsent(t *testing.T) {
	p, err := New("id", "secret", "https://migrate.example/oauth/google/callback")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	url := p.AuthCodeURL("state-token")
	if !strings.Contains(url, "access_type=offline") {
		t.Errorf("the URL does not ask for offline access: %s", url)
	}
	if !strings.Contains(url, "prompt=consent") {
		t.Errorf("the URL does not force the consent screen: %s", url)
	}
	if !strings.Contains(url, "state=state-token") {
		t.Errorf("the URL does not carry the state: %s", url)
	}
	if !strings.Contains(url, "drive.readonly") {
		t.Errorf("the URL does not request the read-only scope: %s", url)
	}
}

func TestTokensValid(t *testing.T) {
	valid := Tokens{AccessToken: "a", Expiry: time.Now().Add(time.Hour)}
	if !valid.Valid() {
		t.Error("a token expiring in an hour should be valid")
	}

	expired := Tokens{AccessToken: "a", Expiry: time.Now().Add(-time.Minute)}
	if expired.Valid() {
		t.Error("an expired token should not be valid")
	}

	// A token about to expire is treated as invalid, so a long copy does not
	// start with one that dies immediately.
	soon := Tokens{AccessToken: "a", Expiry: time.Now().Add(30 * time.Second)}
	if soon.Valid() {
		t.Error("a token expiring in 30 seconds should be refreshed first")
	}

	empty := Tokens{}
	if empty.Valid() {
		t.Error("a token with no access token is not valid")
	}
}

func TestSealAndOpenTokens(t *testing.T) {
	sealer, err := core.NewSealer(core.Secret("token-key"))
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}

	original := Tokens{
		AccessToken:  "ya29.access",
		RefreshToken: "1//refresh",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(time.Hour).Truncate(time.Second),
	}

	sealed, err := SealJSON(sealer, original)
	if err != nil {
		t.Fatalf("SealJSON: %v", err)
	}
	if strings.Contains(string(sealed), "refresh") {
		t.Fatal("the sealed bytes contain the plaintext")
	}

	opened, err := OpenJSON(sealer, sealed)
	if err != nil {
		t.Fatalf("OpenJSON: %v", err)
	}
	if opened.AccessToken != original.AccessToken {
		t.Errorf("access token = %q", opened.AccessToken)
	}
	if opened.RefreshToken != original.RefreshToken {
		t.Errorf("refresh token = %q", opened.RefreshToken)
	}
	if !opened.Expiry.Equal(original.Expiry) {
		t.Errorf("expiry = %v, want %v", opened.Expiry, original.Expiry)
	}
}

func TestOpenJSONWithWrongKeyFails(t *testing.T) {
	a, _ := core.NewSealer(core.Secret("key-a"))
	b, _ := core.NewSealer(core.Secret("key-b"))

	sealed, _ := SealJSON(a, Tokens{RefreshToken: "1//x"})
	if _, err := OpenJSON(b, sealed); err == nil {
		t.Fatal("opening with the wrong key should fail")
	}
}
