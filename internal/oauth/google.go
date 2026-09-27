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
	"net/http"
	"time"

	"github.com/marcodellemarche/zlatan/internal/core"
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
// when the person has authorised before, and a migration without a refresh
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

// Refresh exchanges the refresh token for a fresh access token. A refused
// refresh token (Google answers invalid_grant) is a dead credential and comes
// back as ErrUnauthorized; anything else is transient.
func (p *Provider) Refresh(ctx context.Context, refresh string) (Tokens, error) {
	if refresh == "" {
		return Tokens{}, fmt.Errorf("no refresh token to use: %w", ErrUnauthorized)
	}
	source := p.oauth.TokenSource(ctx, &oauth2.Token{RefreshToken: refresh})
	tok, err := source.Token()
	if err != nil {
		if isRefused(err) {
			return Tokens{}, fmt.Errorf("refreshing the access token: %w", ErrUnauthorized)
		}
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

// ErrUnauthorized means Google refused the refresh token. Only a refusal is a
// dead credential: a timeout or a 5xx is transient and must not stop work the
// person asked for.
var ErrUnauthorized = errors.New("Google refused the credential")

// Probe checks that the refresh token still works, without keeping the result.
// It is the preflight a copy runs so a revoked credential is caught before a
// long copy starts, rather than hours in.
func (p *Provider) Probe(ctx context.Context, refresh string) error {
	_, err := p.Refresh(ctx, refresh)
	return err
}

// isRefused reports whether an error from the token endpoint means the
// credential itself was rejected, rather than a transient failure. Google
// answers invalid_grant for a revoked or expired refresh token, and 401/403
// for a token the client may not use.
func isRefused(err error) bool {
	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		if re.ErrorCode == "invalid_grant" || re.ErrorCode == "invalid_client" {
			return true
		}
		if re.Response != nil {
			switch re.Response.StatusCode {
			case http.StatusUnauthorized, http.StatusForbidden:
				return true
			}
		}
	}
	return false
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
