// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/marcodellemarche/zlatan/internal/config"
	"github.com/marcodellemarche/zlatan/internal/core"
	"github.com/marcodellemarche/zlatan/internal/i18n"
	"github.com/marcodellemarche/zlatan/internal/immich"
	"github.com/marcodellemarche/zlatan/internal/nextcloud"
	"github.com/marcodellemarche/zlatan/internal/store"
)

// page is what the wizard template renders. Everything the person reads is
// resolved here, in their language, so the template holds no logic and the
// polling script holds no text.
type page struct {
	User    string
	Version string

	// Lang is the language for this request; Langs is the switcher.
	Lang  i18n.Lang
	Langs []langChoice

	// Screen is which of the design's screens the wizard renders: entry,
	// tracks, takeout, upload, waiting, done or error. It is derived from the
	// two track states so the markup and the state cannot disagree.
	Screen string

	Drive  trackView
	Photos trackView

	DriveBytes   int64
	DriveFiles   int64
	PhotosAssets int64

	LastError string

	// DriveFacts and PhotosFacts are the one line of numbers under each track,
	// already rendered. Empty while there is nothing to count.
	DriveFacts  string
	PhotosFacts string

	// DriveVerification and PhotosVerification are the last recorded checks, so
	// the closing screen states what was compared rather than a bare "done".
	DriveVerification  *core.Verify
	PhotosVerification *core.Verify

	// TakeoutPoll is how often the watcher looks in the person's Drive. The
	// waiting screen states it rather than inventing a number.
	TakeoutPoll time.Duration

	// Quota is advisory: the copy runs either way, so this is a line, never a
	// block.
	QuotaOver      bool
	QuotaProjected int64
	QuotaBudget    int64

	// Where the data is going. Read from configuration, never hardcoded: the
	// person should be able to see which cloud is theirs.
	NextcloudURL  string
	NextcloudHost string
	ImmichURL     string
	ImmichHost    string

	// TakeoutFolder is the folder name the wizard tells the person to look
	// for. It is Google's own name, not something they choose.
	TakeoutFolder string

	CanTakeoutRoute bool
	CanTakeout      bool
	CanUpload       bool

	CanStartDrive  bool
	CanStartPhotos bool

	GoogleConnected    bool
	NextcloudConnected bool
	ImmichConnected    bool

	// ImmichNote is the outcome of the last connect attempt, already resolved
	// in the reader's language: "connected", "that key was not accepted", or
	// "paste a key first". Empty when there is nothing to say.
	ImmichNote string
}

// langChoice is one entry in the switcher.
type langChoice struct {
	Lang    i18n.Lang
	Name    string
	Current bool
}

// trackView is one half of the migration, ready to render: the state named in
// the person's language and the class that colours it.
type trackView struct {
	Track     core.Track
	State     string
	Progress  string
	Pill      string
	PillClass string
	Done      bool
	Failed    bool
}

// T, N and B are the template's only formatting helpers. They are methods
// rather than template functions because every one of them needs the language,
// and a template function cannot see it.
func (p page) T(key string, args ...any) string { return i18n.T(p.Lang, key, args...) }
func (p page) N(n int64) string                 { return i18n.Count(p.Lang, n) }
func (p page) B(n int64) string                 { return i18n.Bytes(p.Lang, n) }

// Check renders a recorded verification from its numbers, so the sentence is
// in the reader's language rather than the runner's. It returns "" when no
// check ran, and the closing screen then falls back to the plain sentence.
func (p page) Check(v *core.Verify) string {
	if v == nil || v.Checked == 0 {
		return ""
	}
	if v.Mismatch > 0 {
		return p.T("check.mismatch", p.N(int64(v.Checked)), p.N(int64(v.Mismatch)))
	}
	return p.T("check.ok", p.N(int64(v.Checked)))
}

// Every states the real poll interval from configuration.
func (p page) Every() string {
	return p.T("wait.every", i18n.Span(p.Lang, p.TakeoutPoll))
}

// Route is "Google Drive → nextcloud.example.org", with the destination taken
// from configuration. When no destination is configured the arrow is dropped
// rather than pointing at nothing.
func (p page) Route(srcKey, host string) string {
	if host == "" {
		return p.T(srcKey)
	}
	return p.T("route", p.T(srcKey), host)
}

// hostOf reduces a configured URL to the name a person recognises. A value
// that will not parse is shown as it was configured, which is more useful than
// an empty line.
func hostOf(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return strings.TrimSuffix(strings.TrimPrefix(raw, "https://"), "/")
	}
	return u.Host
}

func viewOf(lang i18n.Lang, t core.TrackSummary) trackView {
	return trackView{
		Track:     t.Track,
		State:     t.State,
		Progress:  t.Progress,
		Pill:      i18n.T(lang, pillKey(t.State)),
		PillClass: pillClass(t.State),
		Done:      t.Done,
		Failed:    t.Failed,
	}
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

	// An explicit language choice is remembered and the parameter dropped, so
	// the address bar stays clean and a bookmark does not pin a language
	// forever. It works without JavaScript because it is a plain link.
	if choice := r.URL.Query().Get(i18n.Param); choice != "" {
		if l, ok := i18n.Parse(choice); ok {
			i18n.SetCookie(w, l)
		}
		http.Redirect(w, r, r.URL.Path, http.StatusSeeOther)
		return
	}
	lang := i18n.FromRequest(r)

	m, err := opts.State.EnsureMigration(r.Context(), user, email)
	if err != nil {
		opts.Log.Error("wizard: ensure migration", "user", user, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	summaries := m.Summaries()
	p := page{
		User:           user,
		Version:        opts.Version,
		Lang:           lang,
		Langs:          langChoices(lang),
		Screen:         screenFor(m),
		Drive:          viewOf(lang, summaries[0]),
		Photos:         viewOf(lang, summaries[1]),
		DriveBytes:     m.DriveBytesCopied,
		DriveFiles:     m.DriveFilesCopied,
		PhotosAssets:   m.PhotosAssetsAdded,
		LastError:      m.LastError,
		TakeoutFolder:  opts.Config.Google.TakeoutFolder,
		NextcloudURL:   opts.Config.Nextcloud.URL,
		NextcloudHost:  hostOf(opts.Config.Nextcloud.URL),
		ImmichURL:      opts.Config.Immich.URL,
		ImmichHost:     hostOf(opts.Config.Immich.URL),
		CanUpload:      opts.Config.Immich.Configured(),
		CanStartDrive:  opts.Google != nil && opts.Sealer != nil && opts.TokenStore != nil && opts.Config.Nextcloud.Configured(),
		CanStartPhotos: opts.Config.Immich.Configured() && opts.Runner != nil,
		TakeoutPoll:    opts.Config.TakeoutPoll,
	}
	if p.TakeoutPoll <= 0 {
		p.TakeoutPoll = config.DefaultTakeoutPoll
	}
	p.DriveFacts = factsFor(lang, p.Drive, m)
	p.PhotosFacts = factsFor(lang, p.Photos, m)

	// The closing screen states what was actually compared. A missing row is
	// not an error: the screen falls back to the plain sentence.
	if v, err := opts.State.LatestVerification(r.Context(), user, core.TrackDrive); err == nil {
		p.DriveVerification = &v
	}
	if v, err := opts.State.LatestVerification(r.Context(), user, core.TrackPhotos); err == nil {
		p.PhotosVerification = &v
	}

	// The budget is advisory. It is computed here rather than stored so it
	// always reflects the current policy, and shown only once the pre-copy scan
	// has produced numbers.
	p.QuotaProjected, p.QuotaBudget, p.QuotaOver = m.QuotaOverrun(opts.Config.Quota.BudgetGiBFor(user))

	// The "Add to Drive" route needs the Google client to watch the Drive and
	// Immich to import. Whether the person has connected Google is filled in
	// below, with the other credential reads.
	p.CanTakeoutRoute = opts.Config.Google.Configured() && opts.Config.Immich.Configured() && opts.Runner != nil

	// Both credentials the Drive half needs. A failed read is "not connected",
	// which is the honest answer and offers the button again.
	if opts.TokenStore != nil {
		_, err := opts.TokenStore.GetToken(r.Context(), user, "google")
		p.GoogleConnected = err == nil
		_, err = opts.TokenStore.GetToken(r.Context(), user, nextcloud.Provider)
		p.NextcloudConnected = err == nil
		_, err = opts.TokenStore.GetToken(r.Context(), user, immich.Provider)
		p.ImmichConnected = err == nil
	}

	// The outcome of a connect attempt, carried back as a short code so the
	// address bar holds no key and the sentence is chosen here, in the
	// person's language.
	switch r.URL.Query().Get("immich") {
	case "connected":
		p.ImmichNote = i18n.T(lang, "immich.connected")
	case "invalid":
		p.ImmichNote = i18n.T(lang, "immich.invalid")
	case "empty":
		p.ImmichNote = i18n.T(lang, "immich.empty")
	}

	// The Takeout watcher reads the person's Drive, so the route can only be
	// offered once they have connected Google. Without it the button would
	// start a wait that could never look anywhere.
	p.CanTakeout = p.CanTakeoutRoute && p.GoogleConnected

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Vary", "Accept-Language, Cookie")
	if err := wizardTemplate.ExecuteTemplate(w, "wizard.html", p); err != nil {
		opts.Log.Error("wizard: render", "error", err)
	}
}

func langChoices(current i18n.Lang) []langChoice {
	out := make([]langChoice, 0, len(i18n.Supported))
	for _, l := range i18n.Supported {
		out = append(out, langChoice{Lang: l, Name: i18n.Name(l), Current: l == current})
	}
	return out
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

	// Every string here is already rendered in the reader's language, so the
	// polling script never has to know a state name or a plural rule. When the
	// state itself changes the script reloads, because a different state means
	// a different screen.
	lang := i18n.FromRequest(r)
	tracks := make([]map[string]any, 0, 2)
	for _, t := range m.Summaries() {
		v := viewOf(lang, t)
		tracks = append(tracks, map[string]any{
			"Track":     v.Track,
			"State":     v.State,
			"Pill":      v.Pill,
			"PillClass": v.PillClass,
			"Facts":     factsFor(lang, v, m),
			"Progress":  v.Progress,
			"Done":      v.Done,
			"Failed":    v.Failed,
		})
	}

	writeJSON(w, opts, map[string]any{
		"user":   m.User,
		"tracks": tracks,
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

// connectImmich validates the API key the person created in their own Immich
// account and stores it. The key travels in the POST body, never the URL, so
// it does not land in a log line or the browser history. Immich has no admin
// endpoint that mints a key for another account, so the person is the only one
// who can produce it; this is where they hand it over.
func (opts Options) connectImmich(w http.ResponseWriter, r *http.Request) {
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
		opts.Log.Error("connectImmich: ensure migration", "user", user, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// A body cap: an API key is short, and a huge body is not a key. Without
	// it a POST of any size would be read into memory before the value is even
	// looked at.
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "could not read the form", http.StatusBadRequest)
		return
	}
	apiKey := strings.TrimSpace(r.PostFormValue("api_key"))
	if apiKey == "" {
		http.Redirect(w, r, "/?immich=empty", http.StatusSeeOther)
		return
	}

	if _, err := opts.Runner.ConnectImmich(r.Context(), user, apiKey); err != nil {
		opts.Log.Warn("connectImmich: rejected", "user", user, "error", err)
		// The key is not echoed back, and the reason is generic on purpose:
		// whether Immich refused it or was unreachable, the person's next move
		// is the same, and a distinction could help somebody probe keys.
		http.Redirect(w, r, "/?immich=invalid", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/?immich=connected", http.StatusSeeOther)
}

func (opts Options) startPhotosUpload(w http.ResponseWriter, r *http.Request) {
	opts.startPhotos(w, r, "upload")
}

func (opts Options) startPhotosTakeout(w http.ResponseWriter, r *http.Request) {
	opts.startPhotos(w, r, "takeout")
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
	case "takeout":
		startErr = opts.Runner.StartPhotosTakeout(r.Context(), user)
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
