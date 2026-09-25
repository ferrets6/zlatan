// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/marcodellemarche/zlatan/internal/core"
	"github.com/marcodellemarche/zlatan/internal/nextcloud"
	"github.com/marcodellemarche/zlatan/internal/store"
)

// page is what the wizard template renders.
type page struct {
	User    string
	Email   string
	Version string
	Tracks  []core.TrackSummary

	// Screen is which of the design's screens the wizard renders: entry,
	// tracks, takeout, share, upload, waiting, done or error. It is derived
	// from the two track states so the markup and the state cannot disagree.
	Screen string

	// DriveBytes and DriveFiles are rendered through FormatBytes, so the
	// person reads "4.1 GiB" rather than a nine-digit number.
	DriveBytes   int64
	DriveFiles   int64
	PhotosAssets int64

	LastError string

	// ShareAccount is the address the wizard shows for the "Add to Drive"
	// route. Empty means the route is not offered and the upload route is
	// used instead.
	ShareAccount string
	CanShare     bool
	CanUpload    bool

	// CanStartDrive is false when the OAuth client or Nextcloud is not
	// configured, so the button is not offered when it cannot work.
	CanStartDrive  bool
	CanStartPhotos bool

	// GoogleConnected and NextcloudConnected drive which button the Drive
	// section shows: connect Google, connect Nextcloud, or start the copy.
	// The copy needs both.
	GoogleConnected    bool
	NextcloudConnected bool
}

// wizard renders the person's own progress. The identity is resolved from the
// proxy header, never from the request, so a person can only ever see their
// own migration.
func (opts Options) wizard(w http.ResponseWriter, r *http.Request) {
	user, email, err := identityFrom(r, opts.Config.TrustedProxy)
	if err != nil {
		http.Error(w, "sign in through the portal to migrate your data", http.StatusUnauthorized)
		return
	}

	m, err := opts.State.EnsureMigration(r.Context(), user, email)
	if err != nil {
		opts.Log.Error("wizard: ensure migration", "user", user, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	p := page{
		User:           user,
		Email:          email,
		Version:        opts.Version,
		Tracks:         m.Summaries(),
		Screen:         screenFor(m),
		DriveBytes:     m.DriveBytesCopied,
		DriveFiles:     m.DriveFilesCopied,
		PhotosAssets:   m.PhotosAssetsAdded,
		LastError:      m.LastError,
		ShareAccount:   opts.Config.Google.ShareAccount,
		CanShare:       opts.Config.Google.ShareAccount != "" && opts.Config.Google.Configured(),
		CanUpload:      opts.Config.Immich.Configured(),
		CanStartDrive:  opts.Google != nil && opts.Sealer != nil && opts.TokenStore != nil && opts.Config.Nextcloud.Configured(),
		CanStartPhotos: opts.Config.Immich.Configured() && opts.Runner != nil,
	}

	// Both credentials the Drive half needs. A failed read is "not connected",
	// which is the honest answer and offers the button again.
	if opts.TokenStore != nil {
		_, err := opts.TokenStore.GetToken(r.Context(), user, "google")
		p.GoogleConnected = err == nil
		_, err = opts.TokenStore.GetToken(r.Context(), user, nextcloud.Provider)
		p.NextcloudConnected = err == nil
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := wizardTemplate.ExecuteTemplate(w, "wizard.html", p); err != nil {
		opts.Log.Error("wizard: render", "error", err)
	}
}

// status is the JSON the page polls, so a multi-hour copy is not a frozen
// screen. It reports only the caller's own migration.
func (opts Options) status(w http.ResponseWriter, r *http.Request) {
	user, _, err := identityFrom(r, opts.Config.TrustedProxy)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// While the person is expected to be granting Nextcloud access, check
	// whether they have. Doing it here means the grant is picked up by the
	// polling the page already does, so no extra click is needed.
	if opts.Runner != nil {
		if _, err := opts.Runner.PollNextcloud(r.Context(), user); err != nil {
			opts.Log.Warn("status: poll Nextcloud", "user", user, "error", err)
		}
	}

	m, err := opts.State.GetMigration(r.Context(), user)
	if errors.Is(err, store.ErrNoMigration) {
		writeJSON(w, opts, map[string]any{"user": user, "tracks": []any{}})
		return
	}
	if err != nil {
		opts.Log.Error("status: read migration", "user", user, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// The line is resolved here so the polling script can refresh the calm
	// sentence without knowing the state names, exactly as the server rendered
	// it. Progress is the technical detail shown underneath.
	tracks := make([]map[string]any, 0, 2)
	for _, t := range m.Summaries() {
		tracks = append(tracks, map[string]any{
			"Track":    t.Track,
			"State":    t.State,
			"Progress": t.Progress,
			"Line":     stateLine(string(t.Track), t.State),
			"Done":     t.Done,
			"Failed":   t.Failed,
		})
	}

	writeJSON(w, opts, map[string]any{
		"user":         m.User,
		"tracks":       tracks,
		"driveBytes":   m.DriveBytesCopied,
		"driveFiles":   m.DriveFilesCopied,
		"photosAssets": m.PhotosAssetsAdded,
		"lastError":    m.LastError,
	})
}

func writeJSON(w http.ResponseWriter, opts Options, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		opts.Log.Error("write json response", "error", err)
	}
}

// startDrive queues the Drive migration. The handler returns as soon as the
// work is queued: the copy runs in the background and reports through the
// store, so closing the tab does not stop it.
func (opts Options) startDrive(w http.ResponseWriter, r *http.Request) {
	user, email, err := identityFrom(r, opts.Config.TrustedProxy)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if opts.Runner == nil {
		http.Error(w, "the Drive route is not available on this instance", http.StatusServiceUnavailable)
		return
	}
	if _, err := opts.State.EnsureMigration(r.Context(), user, email); err != nil {
		opts.Log.Error("startDrive: ensure migration", "user", user, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := opts.Runner.StartDrive(r.Context(), user); err != nil {
		opts.Log.Error("startDrive: queue", "user", user, "error", err)
		http.Error(w, "could not start the Drive migration", http.StatusConflict)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// startNextcloud begins the Nextcloud Login Flow and sends the person to the
// grant page. The grant itself is picked up by the status polling.
func (opts Options) startNextcloud(w http.ResponseWriter, r *http.Request) {
	user, email, err := identityFrom(r, opts.Config.TrustedProxy)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if opts.Runner == nil {
		http.Error(w, "the Drive route is not available on this instance", http.StatusServiceUnavailable)
		return
	}
	if _, err := opts.State.EnsureMigration(r.Context(), user, email); err != nil {
		opts.Log.Error("startNextcloud: ensure migration", "user", user, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	loginURL, err := opts.Runner.StartNextcloud(r.Context(), user)
	if err != nil {
		opts.Log.Error("startNextcloud: begin flow", "user", user, "error", err)
		http.Error(w, "could not start the Nextcloud connection", http.StatusBadGateway)
		return
	}
	if _, err := opts.State.SetDriveState(r.Context(), user, core.DriveConsentPending,
		"Waiting for your Nextcloud consent"); err != nil {
		opts.Log.Error("startNextcloud: set state", "user", user, "error", err)
	}
	http.Redirect(w, r, loginURL, http.StatusSeeOther)
}

func (opts Options) startPhotosUpload(w http.ResponseWriter, r *http.Request) {
	opts.startPhotos(w, r, "upload")
}

func (opts Options) startPhotosShare(w http.ResponseWriter, r *http.Request) {
	opts.startPhotos(w, r, "share")
}

func (opts Options) startPhotos(w http.ResponseWriter, r *http.Request, route string) {
	user, email, err := identityFrom(r, opts.Config.TrustedProxy)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if opts.Runner == nil {
		http.Error(w, "the Photos route is not available on this instance", http.StatusServiceUnavailable)
		return
	}
	if _, err := opts.State.EnsureMigration(r.Context(), user, email); err != nil {
		opts.Log.Error("startPhotos: ensure migration", "user", user, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var startErr error
	switch route {
	case "share":
		startErr = opts.Runner.StartPhotosShare(r.Context(), user)
	default:
		startErr = opts.Runner.StartPhotosUpload(r.Context(), user)
	}
	if startErr != nil {
		opts.Log.Error("startPhotos: queue", "user", user, "route", route, "error", startErr)
		http.Error(w, "could not start the Photos migration", http.StatusConflict)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// staticHandler serves the embedded assets.
func staticHandler() http.Handler {
	return http.FileServer(http.FS(staticRoot))
}
