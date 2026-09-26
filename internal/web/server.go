// SPDX-License-Identifier: AGPL-3.0-or-later

// Package web is Zlatan's HTTP surface: the wizard a person sees, and the
// small JSON API the wizard's progress polling uses.
package web

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/marcodellemarche/zlatan/internal/config"
	"github.com/marcodellemarche/zlatan/internal/core"
	"github.com/marcodellemarche/zlatan/internal/oauth"
	"github.com/marcodellemarche/zlatan/internal/upload"
)

// Pinger is the part of the store /healthz needs, kept narrow so the handler
// can be tested without a database.
type Pinger interface {
	PingContext(ctx context.Context) error
}

// State is the persistence the wizard needs.
type State interface {
	EnsureMigration(ctx context.Context, user, email string) (core.Migration, error)
	GetMigration(ctx context.Context, user string) (core.Migration, error)
	SetDriveState(ctx context.Context, user string, state core.DriveState, progress string) (core.Migration, error)
	SetPhotosState(ctx context.Context, user string, state core.PhotosState, progress string) (core.Migration, error)

	// LatestVerification is read for the closing page, so it can state what was
	// actually compared rather than "done". A missing row is not an error; the
	// caller treats it as "no check recorded".
	LatestVerification(ctx context.Context, user string, track core.Track) (core.Verify, error)
}

// TokenStore persists the sealed OAuth tokens.
type TokenStore interface {
	PutToken(ctx context.Context, t core.Token) error
	GetToken(ctx context.Context, user, provider string) (core.Token, error)
}

// GoogleFlow is the OAuth provider the wizard drives.
type GoogleFlow interface {
	AuthCodeURL(state string) string
	Exchange(ctx context.Context, code string) (oauth.Tokens, error)
}

// Sealer seals tokens at rest.
type Sealer interface {
	Seal(plaintext []byte) ([]byte, error)
	Open(sealed []byte) ([]byte, error)
}

// UploadStore holds a Takeout archive being uploaded in chunks.
type UploadStore interface {
	Begin(user, name string, size, chunkSize int64) (upload.Session, error)
	WriteChunk(user, name string, index int64, r io.Reader) (upload.Session, error)
	Status(user, name string) (upload.Session, error)
	Complete(user, name string) (string, error)
	Abort(user, name string) error
}

// Options wires the server.
type Options struct {
	Version string
	Config  *config.Config
	State   State
	DB      Pinger
	Log     *slog.Logger

	// Google, Sealer and TokenStore are optional: with none of them the Drive
	// route cannot be started, which the wizard says rather than offering a
	// button that fails.
	Google     GoogleFlow
	Sealer     Sealer
	TokenStore TokenStore

	// Uploads is optional too: without it the upload route is not offered.
	Uploads UploadStore

	// Runner is the machinery that actually moves data. It is an interface so
	// the HTTP layer can be tested without rclone or immich-go, and so a
	// missing runner degrades to "the wizard shows state but cannot start
	// anything" rather than a panic.
	Runner Runner
}

// Runner is what the wizard can start. Each call is expected to return as soon
// as the work is queued: the work itself runs in the background and reports
// through the store.
type Runner interface {
	StartDrive(ctx context.Context, user string) error
	StartPhotosUpload(ctx context.Context, user string) error
	StartPhotosTakeout(ctx context.Context, user string) error

	// StartNextcloud begins the Nextcloud Login Flow and returns the URL the
	// person opens to grant access. PollNextcloud notices the grant; it is
	// called from the status polling, so the person does not have to come back
	// and click again.
	StartNextcloud(ctx context.Context, user string) (string, error)
	PollNextcloud(ctx context.Context, user string) (core.DriveState, error)
}

// Routes builds the wizard's surface. Every page is behind the proxy gate and
// the identity check; nothing is served to an unauthenticated request.
func Routes(opts Options) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz(opts))

	gate := func(next http.HandlerFunc) http.Handler {
		return proxyGate(opts.Config.ProxySecret, next)
	}

	mux.Handle("GET /{$}", gate(opts.wizard))
	mux.Handle("GET /status", gate(opts.status))
	mux.Handle("GET /oauth/google/start", gate(opts.oauthStart))
	mux.Handle("GET /oauth/google/callback", gate(opts.oauthCallback))
	mux.Handle("POST /nextcloud/start", gate(opts.startNextcloud))
	mux.Handle("POST /drive/start", gate(opts.startDrive))
	mux.Handle("POST /photos/upload/start", gate(opts.startPhotosUpload))
	mux.Handle("POST /photos/takeout/start", gate(opts.startPhotosTakeout))

	// Resumable Takeout upload. Each request is authenticated and scoped to the
	// caller, so one person can never write into another's staging area.
	mux.Handle("POST /upload/begin", gate(opts.uploadBegin))
	mux.Handle("PUT /upload/chunk", gate(opts.uploadChunk))
	mux.Handle("GET /upload/status", gate(opts.uploadStatus))
	mux.Handle("POST /upload/complete", gate(opts.uploadComplete))
	mux.Handle("DELETE /upload", gate(opts.uploadAbort))
	mux.Handle("GET /static/", gate(http.StripPrefix("/static/", staticHandler()).ServeHTTP))

	return mux
}

// healthz answers for the process, not the providers. It pings the database,
// because a process that cannot reach its own state is not healthy.
func healthz(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{"status": "ok", "version": opts.Version}
		code := http.StatusOK

		if opts.DB != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			if err := opts.DB.PingContext(ctx); err != nil {
				body["status"] = "unavailable"
				body["detail"] = "database unreachable"
				code = http.StatusServiceUnavailable
				opts.Log.Error("healthz: database unreachable", "error", err)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(code)
		if err := json.NewEncoder(w).Encode(body); err != nil {
			opts.Log.Error("healthz: write response", "error", err)
		}
	}
}
