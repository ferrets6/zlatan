// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/marcodellemarche/zlatan/internal/core"
	"github.com/marcodellemarche/zlatan/internal/oauth"
)

// oauthStateTTL is how long an authorisation may sit before the callback is
// refused. Long enough for a person to log in and consent, short enough that
// an intercepted state is not useful for long.
const oauthStateTTL = 15 * time.Minute

// stateSigner produces and verifies the OAuth "state" parameter. The state
// binds the callback to the person who started the flow, so a callback cannot
// be replayed onto somebody else's session.
type stateSigner struct {
	key []byte
	now func() time.Time
}

func newStateSigner(key core.Secret) (*stateSigner, error) {
	if key.Empty() {
		return nil, errors.New("no key to sign the OAuth state with")
	}
	sum := sha256.Sum256([]byte("oauth-state:" + key.Reveal()))
	return &stateSigner{key: sum[:], now: time.Now}, nil
}

// sign returns "user.expiry.signature", base64url-encoded as one token.
func (s *stateSigner) sign(user string) string {
	expiry := s.now().Add(oauthStateTTL).Unix()
	payload := user + "." + itoa(expiry)
	return payload + "." + s.mac(payload)
}

// verify checks the signature and the expiry, and returns the user it names.
func (s *stateSigner) verify(state string) (string, error) {
	// SplitN, not Split: a user name could contain dots.
	lastDot := strings.LastIndex(state, ".")
	if lastDot < 0 {
		return "", errors.New("malformed state")
	}
	payload, signature := state[:lastDot], state[lastDot+1:]
	if !hmac.Equal([]byte(signature), []byte(s.mac(payload))) {
		return "", errors.New("the state signature does not match")
	}

	sep := strings.LastIndex(payload, ".")
	if sep < 0 {
		return "", errors.New("malformed state payload")
	}
	user, expiryStr := payload[:sep], payload[sep+1:]
	expiry, err := atoi(expiryStr)
	if err != nil {
		return "", errors.New("malformed state expiry")
	}
	if s.now().After(time.Unix(expiry, 0)) {
		return "", errors.New("the authorisation took too long and expired")
	}
	if user == "" {
		return "", errors.New("the state names no user")
	}
	return user, nil
}

func (s *stateSigner) mac(payload string) string {
	h := hmac.New(sha256.New, s.key)
	h.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

func itoa(n int64) string {
	// Small, dependency-free integer formatting: the value is always positive.
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func atoi(s string) (int64, error) {
	if s == "" {
		return 0, errors.New("empty")
	}
	var n int64
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, errors.New("not a number")
		}
		n = n*10 + int64(r-'0')
	}
	return n, nil
}

// oauthStart sends the person to Google. The state is signed with the token
// key, so the callback can prove the flow was started here, by this person.
func (opts Options) oauthStart(w http.ResponseWriter, r *http.Request) {
	user, email, err := identityFrom(r, opts.Config.TrustedProxy)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if opts.Google == nil {
		http.Error(w, "Google is not configured on this instance", http.StatusServiceUnavailable)
		return
	}
	if _, err := opts.State.EnsureMigration(r.Context(), user, email); err != nil {
		opts.Log.Error("oauthStart: ensure migration", "user", user, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	signer, err := newStateSigner(opts.Config.TokenKey)
	if err != nil {
		opts.Log.Error("oauthStart: state signer", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, opts.Google.AuthCodeURL(signer.sign(user)), http.StatusSeeOther)
}

// oauthCallback completes the flow. It refuses a state that does not verify,
// or one that names somebody other than the caller: a callback for another
// person's flow must not be able to attach their tokens here.
func (opts Options) oauthCallback(w http.ResponseWriter, r *http.Request) {
	user, _, err := identityFrom(r, opts.Config.TrustedProxy)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if opts.Google == nil || opts.TokenStore == nil {
		http.Error(w, "Google is not configured on this instance", http.StatusServiceUnavailable)
		return
	}

	if errParam := r.URL.Query().Get("error"); errParam != "" {
		opts.Log.Warn("oauthCallback: Google refused", "user", user, "error", errParam)
		http.Error(w, "Google ha rifiutato l'autorizzazione", http.StatusForbidden)
		return
	}

	signer, err := newStateSigner(opts.Config.TokenKey)
	if err != nil {
		opts.Log.Error("oauthCallback: state signer", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	stateUser, err := signer.verify(r.URL.Query().Get("state"))
	if err != nil {
		opts.Log.Warn("oauthCallback: bad state", "user", user, "error", err)
		http.Error(w, "la richiesta di autorizzazione non è valida", http.StatusBadRequest)
		return
	}
	if stateUser != user {
		opts.Log.Warn("oauthCallback: state names another user", "caller", user, "state_user", stateUser)
		http.Error(w, "la richiesta di autorizzazione non è valida", http.StatusBadRequest)
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "nessun codice di autorizzazione", http.StatusBadRequest)
		return
	}

	tokens, err := opts.Google.Exchange(r.Context(), code)
	if err != nil {
		opts.Log.Error("oauthCallback: exchange", "user", user, "error", err)
		http.Error(w, "non sono riuscito a completare l'autorizzazione", http.StatusBadGateway)
		return
	}
	if tokens.RefreshToken == "" {
		// Without a refresh token the copy dies when the access token
		// expires, which for a large library is guaranteed. Say so now
		// rather than half-way through.
		opts.Log.Warn("oauthCallback: no refresh token", "user", user)
		http.Error(w, "Google non ha fornito un token a lunga durata: riprova e accetta tutte le autorizzazioni", http.StatusBadGateway)
		return
	}

	sealed, err := oauth.SealJSON(opts.Sealer, tokens)
	if err != nil {
		opts.Log.Error("oauthCallback: seal", "user", user, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := opts.TokenStore.PutToken(r.Context(), core.Token{
		User:     user,
		Provider: "google",
		Sealed:   sealed,
		Scopes:   oauth.DriveScope,
	}); err != nil {
		opts.Log.Error("oauthCallback: store token", "user", user, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if _, err := opts.State.SetDriveState(r.Context(), user, core.DriveSelecting, "Google è collegato: pronto per la copia"); err != nil {
		opts.Log.Error("oauthCallback: set state", "user", user, "error", err)
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
