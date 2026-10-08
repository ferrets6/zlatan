// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
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

	// GoogleSpaceDrive and GoogleSpacePhotos state how much space the person's
	// data takes up at Google, one per card, so the size of what is being moved
	// is visible. Drive is exact; Photos is only an upper bound, because Google
	// reports it inside "other" (Gmail plus Photos) and never on its own. Both
	// are already rendered, and empty when nothing has been read yet.
	GoogleSpaceDrive  string
	GoogleSpacePhotos string

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

	// PhotosArchiveReady is true when a Takeout archive is already in the
	// person's staging area. The failed screen uses it to offer "import again"
	// rather than sending the person back to Google for an export they already
	// have.
	PhotosArchiveReady bool

	// TakeoutFits is false when Google's own free space cannot hold the export,
	// so "Add to Drive" cannot work and the upload route is the only one. It is
	// true when the space is enough, and also when it could not be read: an
	// unreadable quota must not take away a route.
	TakeoutFits bool

	// GoogleFreeKnown is false when the free space could not be read, so the
	// wizard does not claim the Drive is full on a guess.
	GoogleFreeKnown bool

	CanStartDrive  bool
	CanStartPhotos bool

	// Parts is the split export measured against the count the person
	// declared, for the upload screen. PartsStatus and PartsMissing are the
	// two lines it produces, already in the reader's language.
	Parts        core.Parts
	PartsStatus  string
	PartsMissing string

	// AutoImport is whether the import starts by itself once every part is
	// here; the upload screen shows it as a checkbox the person can toggle.
	AutoImport bool

	// KioskURL is the address of the throwaway browser that downloads straight
	// onto the NAS, shown on the upload screen as the second route. Empty when
	// no kiosk is configured, and then no link is shown.
	KioskURL string

	// PhotosFromDrive is true when the import collected the export from the
	// person's Drive, where it still takes up their Google storage.
	PhotosFromDrive bool

	// PhotosLeftOut is the files a failed import did not bring over, and nil
	// unless Photos stopped on exactly that (see leftOut).
	PhotosLeftOut *photosProblems

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
func (p page) Files(n int64) string             { return i18n.Files(p.Lang, n) }

// Check renders a recorded verification from its numbers, so the sentence is
// in the reader's language rather than the runner's. It returns "" when no
// check ran, and the closing screen then falls back to the plain sentence.
func (p page) Check(v *core.Verify) string {
	if v == nil || v.Checked == 0 {
		return ""
	}
	if v.Mismatch > 0 {
		return p.T("check.mismatch", i18n.Files(p.Lang, int64(v.Checked)), i18n.Files(p.Lang, int64(v.Mismatch)))
	}
	return p.T("check.ok", i18n.Files(p.Lang, int64(v.Checked)))
}

// Why renders the reason a track stopped in the reader's language. The runner
// stores a catalogue key, not a sentence, so the reason is chosen here in the
// reader's language. A value that is not a key is shown as it is rather than
// hidden, so a reason written before the keys existed is still visible.
func (p page) Why(message string) string {
	if message == "" {
		return ""
	}
	return i18n.Progress(p.Lang, core.DecodeProgress(message))
}

// photosProblems is what the stopped Photos card shows about an import that
// left files out: one entry per file, whether there were more than the log
// named, and whether "retry only these" is offered (core.Verify.CanRetryProblems,
// the rule the runner accepts it on).
type photosProblems struct {
	Items    []problemView
	More     bool
	CanRetry bool
}

// problemView is one file an import left out, ready to show: its name, where
// it sits in the archives, why, and a search in Immich for the photo it
// belongs to.
type problemView struct {
	Name, Where, Reason, SearchURL string
}

// leftOut lists the files a failed import did not bring over, and nil unless
// Photos stopped on exactly that (core.Migration.LeftOut, as the runner): the
// latest verification may be from an earlier run, and its files must not sit
// under an unrelated reason such as a missing key. With no file named (the log
// would not read, or named none), it is still there with no items, so the
// person can accept.
func leftOut(lang i18n.Lang, m core.Migration, v *core.Verify, immichURL string) *photosProblems {
	if !m.LeftOut() {
		return nil
	}
	if v == nil {
		v = &core.Verify{}
	}
	out := &photosProblems{More: v.Mismatch > len(v.Problems), CanRetry: v.CanRetryProblems()}
	for _, pr := range v.Problems {
		// "<archive>:<path>" as immich-go names a file in a zip; a name in any
		// other shape is shown as it is.
		where, inside := "", filepath.ToSlash(pr.File)
		if archive, rest, ok := strings.Cut(pr.File, ":"); ok {
			where, inside = archive+".zip › ", rest
		}
		name := path.Base(inside)
		item := problemView{
			Name:      name,
			Where:     where + dirOf(inside),
			Reason:    pr.Reason,
			SearchURL: immichSearch(immichURL, name),
		}
		switch {
		case pr.Missing:
			item.Reason = i18n.T(lang, "problem.notFound")
		case pr.Pending:
			item.Reason = i18n.T(lang, "problem.pending", pr.Reason)
		}
		out.Items = append(out.Items, item)
	}
	return out
}

// dirOf is the folder of a path inside an archive, or "" for a name with none
// (path.Dir would say ".").
func dirOf(p string) string {
	if d := path.Dir(p); d != "." {
		return d
	}
	return ""
}

// immichSearch is a link to Immich's search for the photo a file belongs to.
// It searches the name without its extension and without the " (1)" or "(1)"
// Google adds to a second copy, so a left-out copy finds the original already
// imported. Immich's search page reads ?query= as a JSON search. Empty when no
// Immich address is configured.
func immichSearch(immichURL, name string) string {
	if immichURL == "" || name == "" {
		return ""
	}
	stem := strings.TrimSuffix(name, path.Ext(name))
	stem = strings.TrimSpace(copySuffix.ReplaceAllString(stem, ""))
	query, err := json.Marshal(map[string]string{"originalFileName": stem})
	if err != nil {
		return ""
	}
	return strings.TrimRight(immichURL, "/") + "/search?query=" + url.QueryEscape(string(query))
}

// copySuffix is the "(1)" Google appends to a second copy of a file's name.
var copySuffix = regexp.MustCompile(`\s*\(\d+\)$`)

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
		Progress:  i18n.Progress(lang, core.DecodeProgress(t.Progress)),
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
	// An explicit language choice is remembered and the parameter dropped, so
	// the address bar stays clean and a bookmark does not pin a language
	// forever. It works without JavaScript because it is a plain link.
	lang, done := chooseLang(w, r)
	if done {
		return
	}

	// Nobody signed in: show what the service is and how to get in, rather
	// than a bare 401. This is also the page Google's OAuth review reads.
	user, email, err := identityFrom(r, opts.Config.TrustedProxy)
	if err != nil {
		opts.renderPublic(w, "landing.html", "", lang)
		return
	}

	m, err := opts.State.EnsureMigration(r.Context(), user, email)
	if err != nil {
		opts.Log.Error("wizard: ensure migration", "user", user, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	summaries := m.Summaries()
	p := page{
		User:          user,
		Version:       opts.Version,
		Lang:          lang,
		Langs:         langChoices(lang),
		Screen:        screenFor(m),
		Drive:         viewOf(lang, summaries[0]),
		Photos:        viewOf(lang, summaries[1]),
		DriveBytes:    m.DriveBytesCopied,
		DriveFiles:    m.DriveFilesCopied,
		PhotosAssets:  m.PhotosAssetsAdded,
		LastError:     m.LastError,
		TakeoutFolder: opts.Config.Google.TakeoutFolder,
		// The public names, never the internal Docker ones: a browser cannot
		// resolve http://immich_server:2283. Empty when not configured, so the
		// template shows no link and no host rather than a dead one.
		NextcloudURL:   opts.Config.Nextcloud.WebURL(),
		NextcloudHost:  hostOf(opts.Config.Nextcloud.WebURL()),
		ImmichURL:      opts.Config.Immich.WebURL(),
		ImmichHost:     hostOf(opts.Config.Immich.WebURL()),
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
	p.GoogleSpaceDrive = googleSpaceDrive(lang, m)
	p.GoogleSpacePhotos = googleSpacePhotos(lang, m)
	p.PhotosFromDrive = m.PhotosState == core.PhotosDone && core.DecodeProgress(m.PhotosProgress).FromDrive()

	p.AutoImport = m.AutoImport
	p.KioskURL = opts.Config.KioskURL
	if p.Screen == "upload" && opts.Runner != nil {
		if parts, err := opts.Runner.PhotosParts(user, m.PhotosPartsExpected); err == nil {
			p.Parts = parts
			p.PartsStatus, p.PartsMissing = partsLines(lang, parts)
		}
	}

	// The closing screen states what was actually compared. A missing row is
	// not an error: the screen falls back to the plain sentence.
	if v, err := opts.State.LatestVerification(r.Context(), user, core.TrackDrive); err == nil {
		p.DriveVerification = &v
	}
	if v, err := opts.State.LatestVerification(r.Context(), user, core.TrackPhotos); err == nil {
		p.PhotosVerification = &v
	}
	p.PhotosLeftOut = leftOut(lang, m, p.PhotosVerification, p.ImmichURL)

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

	// Whether there is already something to import decides what "try again"
	// means on the stopped screen: importing what is here, or going back to
	// Google for an export that never arrived.
	if opts.Runner != nil {
		if ready, err := opts.Runner.PhotosArchiveReady(r.Context(), user); err == nil {
			p.PhotosArchiveReady = ready
		}
	}

	// "Add to Drive" writes the export into the person's own Drive, so it needs
	// Google's own free space. On a full account it cannot work, and the person
	// whose Drive is full of the photos they are moving is exactly the one who
	// needs the upload route. Default to "it fits" so an unreadable quota never
	// removes a route.
	p.TakeoutFits = true
	if opts.Runner != nil && p.CanTakeout {
		if fits, known, err := opts.Runner.TakeoutFits(r.Context(), user); err == nil {
			p.TakeoutFits = fits
			p.GoogleFreeKnown = known
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Vary", "Accept-Language, Cookie")
	if err := wizardTemplate.ExecuteTemplate(w, "wizard.html", p); err != nil {
		opts.Log.Error("wizard: render", "error", err)
	}
}

// partsLines renders the two lines the upload screen shows for a split export:
// "X of Y here" and, when parts are missing, "still to send: part(s) …". Shared
// by the full render and the /status poll so the live update reads identically.
// i18n.Files carries the file/files plural, so "1 of 1 file" is never "1 files";
// the missing line is singular for one part, plural for several.
func partsLines(lang i18n.Lang, parts core.Parts) (status, missing string) {
	status = i18n.T(lang, "parts.status",
		i18n.Count(lang, int64(parts.Have)), i18n.Files(lang, int64(parts.Expected)))
	if len(parts.Missing) > 0 {
		nums := make([]string, len(parts.Missing))
		for i, n := range parts.Missing {
			nums[i] = strconv.Itoa(n)
		}
		key := "parts.missing"
		if len(parts.Missing) == 1 {
			key = "parts.missingOne"
		}
		missing = i18n.T(lang, key, strings.Join(nums, ", "))
	}
	return status, missing
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

	payload := map[string]any{
		"user":   m.User,
		"tracks": tracks,
	}
	// On the upload screen the parts count is not a track card, so the poll
	// cannot update it the way it updates the cards. Carry it here so the page
	// shows parts arriving (a kiosk download, or files sent over days) without a
	// manual reload, and can reveal the Start button once they are all present.
	if m.PhotosState == core.PhotosAwaitingUpload && m.PhotosPartsExpected > 0 && opts.Runner != nil {
		if parts, err := opts.Runner.PhotosParts(user, m.PhotosPartsExpected); err == nil {
			status, missing := partsLines(lang, parts)
			payload["parts"] = map[string]any{
				"status":   status,
				"missing":  missing,
				"complete": parts.Complete(),
			}
		}
	}
	writeJSON(w, opts, payload)
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
		// A refused credential is not a failure the person cannot act on: the
		// runner cleared it and moved the track back, so the wizard now offers
		// to connect it again. Send them there rather than to an error page.
		if errors.Is(err, core.ErrCredentialRefused) {
			opts.Log.Warn("startDrive: a credential needs renewing", "user", user, "error", err)
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		// The runner recorded why on the track, so the wizard says it.
		if errors.Is(err, core.ErrCredentialUnreadable) {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
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
		core.EncodeProgress(core.Progress{Key: core.ProgressConsentPending})); err != nil {
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
	// "Send the file myself" does not start an import: it moves the track to
	// the upload screen, where the file is sent in chunks. Starting an import
	// here found no archive and failed with "no Takeout archive found", which
	// is how this route was unreachable in practice.
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
		opts.Log.Error("startPhotosUpload: ensure migration", "user", user, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := opts.Runner.BeginPhotosUpload(r.Context(), user); err != nil {
		// A click from a stale tab while an import holds the track: the wizard
		// shows it running.
		if errors.Is(err, core.ErrPhotosNotStopped) {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		if errors.Is(err, core.ErrCredentialRefused) {
			opts.Log.Warn("startPhotosUpload: a credential needs renewing", "user", user, "error", err)
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		opts.Log.Error("startPhotosUpload: begin", "user", user, "error", err)
		http.Error(w, "could not start the upload", http.StatusConflict)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// declarePhotosParts records how many files Google split the export into. It
// is a plain form, so it works without a script, and it may start the import
// at once when every part is already here.
func (opts Options) declarePhotosParts(w http.ResponseWriter, r *http.Request) {
	user, _, err := identityFrom(r, opts.Config.TrustedProxy)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if opts.Runner == nil {
		http.Error(w, "the Photos route is not available on this instance", http.StatusServiceUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "could not read the form", http.StatusBadRequest)
		return
	}
	parts, err := strconv.Atoi(strings.TrimSpace(r.PostFormValue("parts")))
	if err != nil {
		http.Error(w, "the number of files must be a number", http.StatusBadRequest)
		return
	}
	// Only the count here; the auto-import checkbox is its own form
	// (/photos/auto), so saving the number never flips it. The auto-start that
	// may follow is best effort inside the runner, so the only errors here are a
	// bad count, the wrong state, or a store failure — each with its own status.
	if err := opts.Runner.DeclarePhotosParts(r.Context(), user, parts); err != nil {
		switch {
		case errors.Is(err, core.ErrPartsOutOfRange):
			http.Error(w, "the number of files is out of range", http.StatusBadRequest)
		case errors.Is(err, core.ErrNotUploading):
			http.Error(w, "not waiting for an upload", http.StatusConflict)
		default:
			opts.Log.Error("declarePhotosParts", "user", user, "parts", parts, "error", err)
			http.Error(w, "could not save the number of files", http.StatusInternalServerError)
		}
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// setPhotosAuto toggles "start the import by itself when every file is here".
// It is a plain form (works without a script), and the person can change it at
// any time — during an upload or a kiosk download. Turning it on when the files
// are already present starts the import at once.
func (opts Options) setPhotosAuto(w http.ResponseWriter, r *http.Request) {
	user, _, err := identityFrom(r, opts.Config.TrustedProxy)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if opts.Runner == nil {
		http.Error(w, "the Photos route is not available on this instance", http.StatusServiceUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "could not read the form", http.StatusBadRequest)
		return
	}
	on := r.PostFormValue("auto") != ""
	// SetAutoImport only returns an error when the save itself fails (the
	// auto-start it may trigger is best effort inside the runner), so this is a
	// server-side failure, not a bad request.
	if err := opts.Runner.SetAutoImport(r.Context(), user, on); err != nil {
		opts.Log.Error("setPhotosAuto", "user", user, "on", on, "error", err)
		http.Error(w, "could not save the setting", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// startPhotosImportNow is the explicit "Start" button on the upload screen: it
// imports what is in staging if every declared part is there, ignoring the
// auto-import flag. It refuses an incomplete set, so it can never import half a
// Takeout.
func (opts Options) startPhotosImportNow(w http.ResponseWriter, r *http.Request) {
	user, _, err := identityFrom(r, opts.Config.TrustedProxy)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if opts.Runner == nil {
		http.Error(w, "the Photos route is not available on this instance", http.StatusServiceUnavailable)
		return
	}
	// Whatever happens, redirect to the wizard, which re-renders the live state.
	// On success it shows "importing"; on an error the runner has already moved
	// the track to where the person can act next — the reconnect screen, a
	// stopped screen with a retry, or back to the upload screen — so a 409 with
	// no way forward is never the answer. started is not inspected for the same
	// reason: the sweep may have beaten this click, and the page shows the truth.
	if _, err := opts.Runner.StartImportIfComplete(r.Context(), user); err != nil {
		opts.Log.Warn("startPhotosImportNow", "user", user, "error", err)
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// settlePhotos answers the two ways out of an import that left files out:
// retry only those, or accept it as it is. Like the Start button it always
// goes back to the wizard, which shows the live state: a page left open from
// before, clicked twice, finds the track already moved and is shown where it
// is, not a dead-end error.
func (opts Options) settlePhotos(action func(Runner, context.Context, string) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _, err := identityFrom(r, opts.Config.TrustedProxy)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if opts.Runner == nil {
			http.Error(w, "the Photos route is not available on this instance", http.StatusServiceUnavailable)
			return
		}
		if err := action(opts.Runner, r.Context(), user); err != nil {
			opts.Log.Warn("settlePhotos", "user", user, "path", r.URL.Path, "error", err)
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}
}

func (opts Options) startPhotosImport(w http.ResponseWriter, r *http.Request) {
	opts.startPhotos(w, r, "import")
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
		// "import" and the legacy default both mean: import what is on disk.
		startErr = opts.Runner.StartPhotosImport(r.Context(), user)
	}
	if startErr != nil {
		if errors.Is(startErr, core.ErrCredentialRefused) {
			opts.Log.Warn("startPhotos: a credential needs renewing", "user", user, "route", route, "error", startErr)
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		// A credential that will not unseal, or a click from a stale tab on a
		// track already running again: the wizard shows where it really is.
		if errors.Is(startErr, core.ErrCredentialUnreadable) || errors.Is(startErr, core.ErrPhotosNotStopped) {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
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
