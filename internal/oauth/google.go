// SPDX-License-Identifier: AGPL-3.0-or-later

// Package oauth handles the per-user Google authorisation for the Drive route.
//
// It is deliberately separate from Nextcloud's integration_google client: this
// one is asked only for drive.readonly, so the blast radius of a leaked secret
// is the smallest one that still lets the Drive route work.
package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/marcodellemarche/migrate/internal/core"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// DriveScope is the only scope requested. Read-only: the migration never
// modifies anything on the person's Drive.
const DriveScope = "https://www.googleapis.com/auth/drive.readonly"

// Provider wraps the OAuth client for Google.
type Provider struct {
	oauth *oauth2.Config
}

// New builds the provider. It returns an error rather than a half-configured
// provider, so a caller cannot accidentally start a flow that cannot finish.
func New(clientID, clientSecret core.Secret, redirectURL string) (*Provider, error) {
	if clientID.Empty() || clientSecret.Empty() || redirectURL == "" {
		return nil, errors.New("the Google OAuth client is not fully configured")
	}
	return &Provider{
		oauth: &oauth2.Config{
			ClientID:     clientID.Reveal(),
			ClientSecret: clientSecret.Reveal(),
			RedirectURL:  redirectURL,
			Scopes:       []string{DriveScope},
			Endpoint:     google.Endpoint,
		},
	}, nil
}

// AuthCodeURL is where the person is sent to authorise.
//
// AccessTypeOffline asks for a refresh token, so a copy that runs overnight
// survives the access token expiring. PromptConsent forces the consent screen
// even on a second attempt, because without it Google omits the refresh token
// when the person has authorised before — and a migration without a refresh
// token dies mid-copy.
func (p *Provider) AuthCodeURL(state string) string {
	return p.oauth.AuthCodeURL(state,
		oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("prompt", "consent"),
	)
}

// Tokens is what gets sealed and stored. It is a plain struct rather than
// oauth2.Token so the sealed bytes are stable across library versions.
type Tokens struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	Expiry       time.Time `json:"expiry"`
}

// Valid reports whether the access token can still be used without refreshing.
func (t Tokens) Valid() bool {
	return t.AccessToken != "" && time.Now().Before(t.Expiry.Add(-time.Minute))
}

// Exchange turns the callback code into tokens.
func (p *Provider) Exchange(ctx context.Context, code string) (Tokens, error) {
	tok, err := p.oauth.Exchange(ctx, code)
	if err != nil {
		return Tokens{}, fmt.Errorf("exchanging the authorisation code: %w", err)
	}
	return fromOAuth2(tok), nil
}

// Refresh exchanges the refresh token for a fresh access token.
func (p *Provider) Refresh(ctx context.Context, refresh string) (Tokens, error) {
	if refresh == "" {
		return Tokens{}, errors.New("no refresh token to use")
	}
	source := p.oauth.TokenSource(ctx, &oauth2.Token{RefreshToken: refresh})
	tok, err := source.Token()
	if err != nil {
		return Tokens{}, fmt.Errorf("refreshing the access token: %w", err)
	}
	// Google does not return the refresh token on refresh; keep the one we
	// already have or the next refresh would have nothing to use.
	out := fromOAuth2(tok)
	if out.RefreshToken == "" {
		out.RefreshToken = refresh
	}
	return out, nil
}

// Sealer is the part of core.Sealer this package needs. Keeping it an
// interface means the caller can pass anything that opens and seals bytes.
type Sealer interface {
	Seal(plaintext []byte) ([]byte, error)
	Open(sealed []byte) ([]byte, error)
}

// SealJSON encodes and seals the tokens, so only ciphertext reaches the store.
func SealJSON(s Sealer, t Tokens) ([]byte, error) {
	raw, err := json.Marshal(t)
	if err != nil {
		return nil, fmt.Errorf("encoding tokens: %w", err)
	}
	return s.Seal(raw)
}

// OpenJSON unseals and decodes the tokens.
func OpenJSON(s Sealer, sealed []byte) (Tokens, error) {
	raw, err := s.Open(sealed)
	if err != nil {
		return Tokens{}, err
	}
	var t Tokens
	if err := json.Unmarshal(raw, &t); err != nil {
		return Tokens{}, fmt.Errorf("decoding tokens: %w", err)
	}
	return t, nil
}

// RcloneJSON renders the tokens in the shape rclone expects for
// RCLONE_CONFIG_<REMOTE>_TOKEN: a single-line JSON object.
func (t Tokens) RcloneJSON() string {
	type rcloneToken struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		RefreshToken string `json:"refresh_token"`
		Expiry       string `json:"expiry"`
	}
	tokenType := t.TokenType
	if tokenType == "" {
		tokenType = "Bearer"
	}
	raw, err := json.Marshal(rcloneToken{
		AccessToken:  t.AccessToken,
		TokenType:    tokenType,
		RefreshToken: t.RefreshToken,
		Expiry:       t.Expiry.UTC().Format(time.RFC3339),
	})
	if err != nil {
		// A struct of strings cannot fail to marshal.
		return "{}"
	}
	return string(raw)
}

func fromOAuth2(tok *oauth2.Token) Tokens {
	return Tokens{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		TokenType:    tok.TokenType,
		Expiry:       tok.Expiry,
	}
}
