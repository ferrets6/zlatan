// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/marcodellemarche/zlatan/internal/config"
	"github.com/marcodellemarche/zlatan/internal/core"
	"github.com/marcodellemarche/zlatan/internal/i18n"
	"github.com/marcodellemarche/zlatan/internal/immich"
	"github.com/marcodellemarche/zlatan/internal/oauth"
	"github.com/marcodellemarche/zlatan/internal/store"
)

// fakeState is an in-memory State, so the HTTP layer is tested without SQLite.
type fakeState struct {
	migrations    map[string]core.Migration
	verifications map[string]core.Verify
}

func newFakeState() *fakeState {
	return &fakeState{
		migrations:    map[string]core.Migration{},
		verifications: map[string]core.Verify{},
	}
}

func (f *fakeState) LatestVerification(_ context.Context, user string, track core.Track) (core.Verify, error) {
	v, ok := f.verifications[user+"/"+string(track)]
	if !ok {
		return core.Verify{}, store.ErrNoVerification
	}
	return v, nil
}

func (f *fakeState) EnsureMigration(_ context.Context, user, email string) (core.Migration, error) {
	m, ok := f.migrations[user]
	if !ok {
		m = core.Migration{
			User:        user,
			Email:       email,
			DriveState:  core.DriveNotStarted,
			PhotosState: core.PhotosNotStarted,
			CreatedAt:   time.Now(),
		}
		f.migrations[user] = m
	}
	return m, nil
}

func (f *fakeState) GetMigration(_ context.Context, user string) (core.Migration, error) {
	m, ok := f.migrations[user]
	if !ok {
		return core.Migration{}, store.ErrNoMigration
	}
	return m, nil
}

func (f *fakeState) SetDriveState(_ context.Context, user string, state core.DriveState, progress string) (core.Migration, error) {
	m, _ := f.EnsureMigration(context.Background(), user, "")
	m.DriveState, m.DriveProgress = state, progress
	f.migrations[user] = m
	return m, nil
}

func (f *fakeState) SetPhotosState(_ context.Context, user string, state core.PhotosState, progress string) (core.Migration, error) {
	m, _ := f.EnsureMigration(context.Background(), user, "")
	m.PhotosState, m.PhotosProgress = state, progress
	f.migrations[user] = m
	return m, nil
}

// fakeRunner records what was started.
type fakeRunner struct {
	started      []string
	err          error
	immichKey    string
	archiveReady bool
	takeoutFits  bool
	freeKnown    bool
	partsPending bool
	declared     int
	autoSet      bool
	parts        core.Parts
}

func (f *fakeRunner) StartDrive(_ context.Context, user string) error {
	if f.err != nil {
		return f.err
	}
	f.started = append(f.started, "drive:"+user)
	return nil
}

func (f *fakeRunner) BeginPhotosUpload(_ context.Context, user string) error {
	if f.err != nil {
		return f.err
	}
	f.started = append(f.started, "upload:"+user)
	return nil
}

func (f *fakeRunner) StartPhotosImport(_ context.Context, user string) error {
	if f.err != nil {
		return f.err
	}
	f.started = append(f.started, "import:"+user)
	return nil
}

// AutoImportIfEnabled / StartImportIfComplete start the import unless the test
// says parts are still missing, which is the decision the real runner makes
// from disk.
func (f *fakeRunner) AutoImportIfEnabled(_ context.Context, user string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	if f.partsPending {
		return false, nil
	}
	f.started = append(f.started, "import:"+user)
	return true, nil
}

func (f *fakeRunner) StartImportIfComplete(_ context.Context, user string) (bool, error) {
	return f.AutoImportIfEnabled(context.Background(), user)
}

func (f *fakeRunner) SetAutoImport(_ context.Context, _ string, on bool) error {
	if f.err != nil {
		return f.err
	}
	f.autoSet = on
	return nil
}

func (f *fakeRunner) DeclarePhotosParts(_ context.Context, user string, parts int) error {
	if f.err != nil {
		return f.err
	}
	f.declared = parts
	return nil
}

func (f *fakeRunner) PhotosParts(_ string, _ int) (core.Parts, error) {
	return f.parts, nil
}

func (f *fakeRunner) PhotosArchiveReady(_ context.Context, _ string) (bool, error) {
	return f.archiveReady, nil
}

func (f *fakeRunner) TakeoutFits(_ context.Context, _ string) (bool, bool, error) {
	return f.takeoutFits, f.freeKnown, nil
}

func (f *fakeRunner) StartPhotosTakeout(_ context.Context, user string) error {
	if f.err != nil {
		return f.err
	}
	f.started = append(f.started, "takeout:"+user)
	return nil
}

func (f *fakeRunner) StartNextcloud(_ context.Context, user string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.started = append(f.started, "nextcloud:"+user)
	return "https://cloud.example/login/v2/flow/abc", nil
}

func (f *fakeRunner) PollNextcloud(_ context.Context, _ string) (core.DriveState, error) {
	if f.err != nil {
		return "", f.err
	}
	return core.DriveConsentPending, nil
}

func (f *fakeRunner) ConnectImmich(_ context.Context, _ string, apiKey string) (immich.Me, error) {
	if f.err != nil {
		return immich.Me{}, f.err
	}
	f.immichKey = apiKey
	return immich.Me{Email: "marco@example.com", Name: "Marco"}, nil
}

func testOptions(runner Runner) Options {
	cfg := &config.Config{
		ProxySecret:   "proxy-secret",
		TrustedProxy:  "172.18.0.0/16",
		TokenKey:      "token-key",
		StagingDir:    "/tmp/zlatan-test",
		MaxConcurrent: 1,
		Google: config.Google{
			ClientID:      "id",
			ClientSecret:  "secret",
			RedirectURL:   "https://zlatan.example/cb",
			TakeoutFolder: "Takeout",
		},
		Nextcloud: config.Nextcloud{URL: "http://nextcloud", PublicURL: "https://nextcloud.example.org"},
		Immich:    config.Immich{URL: "http://immich", PublicURL: "https://immich.example.org"},
	}
	return Options{
		Version: "test",
		Config:  cfg,
		State:   newFakeState(),
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Runner:  runner,
	}
}

// request builds a request that has passed the proxy, as Caddy would present
// it, with an authenticated identity.
func request(method, target, user string) *http.Request {
	r := httptest.NewRequest(method, target, nil)
	r.RemoteAddr = "172.18.0.5:44444"
	r.Header.Set("X-Zlatan-Proxy-Secret", "proxy-secret")
	if user != "" {
		r.Header.Set("Remote-User", user)
		r.Header.Set("Remote-Email", user+"@example.com")
	}
	return r
}

func TestWizardRendersForAuthenticatedUser(t *testing.T) {
	opts := testOptions(&fakeRunner{})
	handler := Routes(opts)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request("GET", "/", "marco"))

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "marco") {
		t.Error("the page should show who is connected")
	}
	// Where the data is going is read from configuration and shown, so nobody
	// has to guess which server their library is about to land on.
	if !strings.Contains(body, "Google Drive → nextcloud") || !strings.Contains(body, "Google Photos → immich") {
		t.Error("the page should name both destinations, from configuration")
	}
	// The Photos import runs as the person, so before their key is connected
	// the screen asks for it instead of offering a start button.
	if !strings.Contains(body, "/immich/connect") {
		t.Error("the page should ask for the Immich key before importing")
	}
}

// Without an identity the root is a public description of the service, not a
// migration and not a bare 401: Google's OAuth review rejects a home page that
// is only a sign in screen. It must still show nobody's data.
func TestUnauthenticatedRootIsThePublicPage(t *testing.T) {
	opts := testOptions(&fakeRunner{})
	handler := Routes(opts)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request("GET", "/", ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, leak := range []string{"data-track=", "/oauth/google/start", "/drive/start", "/immich/connect"} {
		if strings.Contains(body, leak) {
			t.Errorf("the public page exposes %q", leak)
		}
	}
	if !strings.Contains(body, "/privacy") || !strings.Contains(body, "/terms") {
		t.Error("the home page must link to the privacy policy and the terms")
	}
}

func TestWizardRefusesWithoutProxySecret(t *testing.T) {
	opts := testOptions(&fakeRunner{})
	handler := Routes(opts)

	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "172.18.0.5:44444"
	r.Header.Set("Remote-User", "marco")
	// No X-Zlatan-Proxy-Secret.

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403", rec.Code)
	}
}

// The identity comes from the header, never from the request: a person cannot
// ask for somebody else's migration.
func TestStatusOnlyEverReturnsTheCaller(t *testing.T) {
	opts := testOptions(&fakeRunner{})
	// Seed a second person with distinct progress.
	if _, err := opts.State.SetDriveState(context.Background(), "federico", core.DriveCopying, "federico's copy"); err != nil {
		t.Fatal(err)
	}
	handler := Routes(opts)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request("GET", "/status?user=federico", "marco"))

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "federico") {
		t.Fatalf("the response leaked another person's migration: %s", rec.Body.String())
	}
}

func TestStatusReportsOwnProgress(t *testing.T) {
	opts := testOptions(&fakeRunner{})
	progress := core.EncodeProgress(core.Progress{Key: core.ProgressCopying, Args: []int64{2 << 30, 4}})
	if _, err := opts.State.SetDriveState(context.Background(), "marco", core.DriveCopying, progress); err != nil {
		t.Fatal(err)
	}
	handler := Routes(opts)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request("GET", "/status", "marco"))

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["user"] != "marco" {
		t.Errorf("user = %v", body["user"])
	}
	// The progress must reach the page as a rendered sentence, not the key it
	// is stored as: the polling script shows it verbatim.
	tracks, _ := body["tracks"].([]any)
	if len(tracks) == 0 {
		t.Fatalf("no tracks in the status response")
	}
	drive, _ := tracks[0].(map[string]any)
	got, _ := drive["Progress"].(string)
	want := i18n.Progress(i18n.EN, core.Progress{Key: core.ProgressCopying, Args: []int64{2 << 30, 4}})
	if got != want {
		t.Errorf("progress = %q, want %q", got, want)
	}
	if strings.Contains(got, "progress.") {
		t.Errorf("the raw key leaked to the page: %q", got)
	}
}

func TestStartDriveQueuesTheWork(t *testing.T) {
	runner := &fakeRunner{}
	opts := testOptions(runner)
	handler := Routes(opts)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request("POST", "/drive/start", "marco"))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("code = %d, want 303", rec.Code)
	}
	if len(runner.started) != 1 || runner.started[0] != "drive:marco" {
		t.Fatalf("started = %v", runner.started)
	}
}

func TestStartPhotosRoutes(t *testing.T) {
	runner := &fakeRunner{}
	opts := testOptions(runner)
	handler := Routes(opts)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request("POST", "/photos/takeout/start", "marco"))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, request("POST", "/photos/upload/start", "marco"))

	want := []string{"takeout:marco", "upload:marco"}
	if len(runner.started) != len(want) {
		t.Fatalf("started = %v, want %v", runner.started, want)
	}
	for i := range want {
		if runner.started[i] != want[i] {
			t.Errorf("started[%d] = %q, want %q", i, runner.started[i], want[i])
		}
	}
}

// A missing runner is not a crash: the wizard says the route is unavailable.
func TestStartWithoutRunnerIsRefused(t *testing.T) {
	opts := testOptions(nil)
	handler := Routes(opts)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request("POST", "/drive/start", "marco"))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d, want 503", rec.Code)
	}
}

func TestStartDriveReportsFailure(t *testing.T) {
	runner := &fakeRunner{err: errors.New("no quota")}
	opts := testOptions(runner)
	handler := Routes(opts)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request("POST", "/drive/start", "marco"))

	if rec.Code != http.StatusConflict {
		t.Fatalf("code = %d, want 409", rec.Code)
	}
}

// A refused credential must not leave the person on a generic failure page:
// the runner has already cleared it and moved the track back, so the wizard
// now offers to connect it again. The handler sends them there.
func TestStartDriveSendsARefusedCredentialBackToReconnect(t *testing.T) {
	runner := &fakeRunner{err: fmt.Errorf("connect Google again: %w", core.ErrCredentialRefused)}
	opts := testOptions(runner)
	handler := Routes(opts)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request("POST", "/drive/start", "marco"))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("code = %d, want 303 to the wizard", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/" {
		t.Errorf("Location = %q, want /", got)
	}
}

func TestStartPhotosSendsARefusedCredentialBackToReconnect(t *testing.T) {
	runner := &fakeRunner{err: fmt.Errorf("connect Immich again: %w", core.ErrCredentialRefused)}
	opts := testOptions(runner)
	handler := Routes(opts)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request("POST", "/photos/upload/start", "marco"))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("code = %d, want 303 to the wizard", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/" {
		t.Errorf("Location = %q, want /", got)
	}
}

// "Send the file myself" must move the track to the upload screen and queue no
// import: the file has not been sent yet. Before, this route started the import
// directly, which found no archive and failed, leaving the upload screen
// unreachable.
func TestStartPhotosUploadMovesToTheUploadScreen(t *testing.T) {
	runner := &fakeRunner{}
	opts := testOptions(runner)
	handler := Routes(opts)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request("POST", "/photos/upload/start", "marco"))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("code = %d, want 303 to the wizard", rec.Code)
	}
	if len(runner.started) != 1 || runner.started[0] != "upload:marco" {
		t.Fatalf("BeginPhotosUpload was not called: %v", runner.started)
	}
}

// The failed screen offers "import again" when the archive is already here,
// rather than sending the person back to Google for an export they have.
func TestPhotosRetryImportsWhenTheArchiveIsAlreadyHere(t *testing.T) {
	runner := &fakeRunner{archiveReady: true}
	opts := testOptions(runner)
	handler := Routes(opts)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request("POST", "/photos/import/start", "marco"))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("code = %d, want 303", rec.Code)
	}
	if len(runner.started) != 1 || runner.started[0] != "import:marco" {
		t.Fatalf("StartPhotosImport was not called: %v", runner.started)
	}
}

func TestHealthz(t *testing.T) {
	opts := testOptions(&fakeRunner{})
	handler := Routes(opts)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("status = %v", body["status"])
	}
}

// The "Add to Drive" route reads the person's Drive, so it must not be offered
// before they have connected Google: the button would start a wait that could
// never look anywhere.
func TestTakeoutRouteNeedsGoogleConnected(t *testing.T) {
	// Both credentials the Photos half needs are seeded: the person's own
	// Immich key (the import runs as them) and the Google token (the watcher
	// reads their Drive). The test then removes Google to prove the gate.
	seeded := func() *fakeTokenStore {
		store := newFakeTokenStore()
		store.tokens["marco/immich"] = core.Token{User: "marco", Provider: "immich", Sealed: []byte("x")}
		return store
	}

	opts := oauthOptions(&fakeGoogle{}, seeded())
	opts.Runner = &fakeRunner{}
	handler := Routes(opts)

	// Immich connected but Google not: no start button, and the screen says so.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request("GET", "/", "marco"))
	body := rec.Body.String()
	if strings.Contains(body, "/photos/takeout/start") {
		t.Error("the Takeout start form must not be offered before Google is connected")
	}
	// The Photos route is one numbered procedure, and connecting Google is the
	// step that is still open.
	if !strings.Contains(body, "Connect Google") {
		t.Error("the screen should still show connecting Google as a step")
	}

	// Both connected: the route is offered.
	store := seeded()
	store.tokens["marco/google"] = core.Token{User: "marco", Provider: "google", Sealed: []byte("x")}
	opts = oauthOptions(&fakeGoogle{}, store)
	opts.Runner = &fakeRunner{}
	handler = Routes(opts)

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, request("GET", "/", "marco"))
	if !strings.Contains(rec.Body.String(), "/photos/takeout/start") {
		t.Error("the Takeout start form should be offered once Google is connected")
	}
}

func TestConnectImmichStoresTheKeyAndRedirects(t *testing.T) {
	runner := &fakeRunner{}
	opts := testOptions(runner)
	handler := Routes(opts)

	form := url.Values{"api_key": {"the-personal-key"}}
	r := request("POST", "/immich/connect", "marco")
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Body = io.NopCloser(strings.NewReader(form.Encode()))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("code = %d, want 303", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/?immich=connected" {
		t.Errorf("Location = %q, want /?immich=connected", got)
	}
	if runner.immichKey != "the-personal-key" {
		t.Errorf("the key did not reach the runner, got %q", runner.immichKey)
	}
}

func TestConnectImmichRejectsAnEmptyKey(t *testing.T) {
	runner := &fakeRunner{}
	opts := testOptions(runner)
	handler := Routes(opts)

	form := url.Values{"api_key": {"   "}}
	r := request("POST", "/immich/connect", "marco")
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Body = io.NopCloser(strings.NewReader(form.Encode()))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	if got := rec.Header().Get("Location"); got != "/?immich=empty" {
		t.Errorf("Location = %q, want /?immich=empty", got)
	}
	if runner.immichKey != "" {
		t.Error("an empty key must not reach the runner")
	}
}

func TestConnectImmichReportsARejectedKey(t *testing.T) {
	runner := &fakeRunner{err: errors.New("Immich did not accept that API key")}
	opts := testOptions(runner)
	handler := Routes(opts)

	form := url.Values{"api_key": {"wrong"}}
	r := request("POST", "/immich/connect", "marco")
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Body = io.NopCloser(strings.NewReader(form.Encode()))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	if got := rec.Header().Get("Location"); got != "/?immich=invalid" {
		t.Errorf("Location = %q, want /?immich=invalid", got)
	}
}

func TestConnectImmichRefusesUnauthenticated(t *testing.T) {
	opts := testOptions(&fakeRunner{})
	handler := Routes(opts)

	form := url.Values{"api_key": {"k"}}
	r := httptest.NewRequest("POST", "/immich/connect", strings.NewReader(form.Encode()))
	r.RemoteAddr = "172.18.0.5:44444"
	r.Header.Set("X-Zlatan-Proxy-Secret", "proxy-secret")
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// No Remote-User.

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", rec.Code)
	}
}

// The wizard links a person to their own cloud, so it must use the public
// address. The internal Docker address (http://immich_server:2283) is not
// resolvable from a browser: a link to it is a dead end, and naming it as the
// destination is worse, because it is the one thing on the card that is
// supposed to tell the person where their data went.
func TestWizardNeverShowsTheInternalAddress(t *testing.T) {
	opts := testOptions(&fakeRunner{})
	opts.Config.Nextcloud.URL = "http://nextcloud"
	opts.Config.Nextcloud.PublicURL = "https://cloud.example.org"
	opts.Config.Immich.URL = "http://immich_server:2283"
	opts.Config.Immich.PublicURL = "https://immich.example.org"
	// The Nextcloud link only appears once the Drive half is done.
	if _, err := opts.State.SetDriveState(context.Background(), "marco", core.DriveDone, "done"); err != nil {
		t.Fatal(err)
	}
	handler := Routes(opts)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request("GET", "/", "marco"))
	body := rec.Body.String()

	for _, internal := range []string{"http://nextcloud", "http://immich_server:2283"} {
		if strings.Contains(body, internal) {
			t.Errorf("the page leaks the internal address %q", internal)
		}
	}
	for _, public := range []string{"https://cloud.example.org", "https://immich.example.org"} {
		if !strings.Contains(body, public) {
			t.Errorf("the page should link to the public address %q", public)
		}
	}
}

// With no public address configured the wizard shows no link at all, rather
// than linking to the internal one or to an empty href.
func TestWizardShowsNoLinkWithoutAPublicAddress(t *testing.T) {
	opts := testOptions(&fakeRunner{})
	opts.Config.Nextcloud.URL = "http://nextcloud"
	opts.Config.Nextcloud.PublicURL = ""
	opts.Config.Immich.URL = "http://immich_server:2283"
	opts.Config.Immich.PublicURL = ""
	handler := Routes(opts)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request("GET", "/", "marco"))
	body := rec.Body.String()

	if strings.Contains(body, "http://nextcloud") || strings.Contains(body, "http://immich_server:2283") {
		t.Error("the page must not fall back to the internal address")
	}
	if strings.Contains(body, `href=""`) {
		t.Error("the page must not render an empty link")
	}
}

// A migration that a restart left mid-flight must be recoverable from the
// page: the recovery moves the state, and the page then offers the button.
// This is the regression guard for the dead end where a copy sat in "copying"
// with no way to start it again.
func TestInterruptedStateIsRestartableFromThePage(t *testing.T) {
	// Both credentials the Drive half needs, so the button is the thing under
	// test rather than a missing Google or Nextcloud connection.
	store := newFakeTokenStore()
	store.tokens["marco/google"] = core.Token{User: "marco", Provider: "google", Sealed: []byte("x")}
	store.tokens["marco/nextcloud"] = core.Token{User: "marco", Provider: "nextcloud", Sealed: []byte("x")}
	store.tokens["marco/immich"] = core.Token{User: "marco", Provider: "immich", Sealed: []byte("x")}
	opts := oauthOptions(&fakeGoogle{}, store)
	opts.Runner = &fakeRunner{}
	// Seed a row stuck in the states only the running process writes.
	interrupted := core.EncodeProgress(core.Progress{Key: core.ProgressInterrupted})
	if _, err := opts.State.SetDriveState(context.Background(), "marco", core.DriveSelecting, interrupted); err != nil {
		t.Fatal(err)
	}
	if _, err := opts.State.SetPhotosState(context.Background(), "marco", core.PhotosNotStarted, interrupted); err != nil {
		t.Fatal(err)
	}
	handler := Routes(opts)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request("GET", "/", "marco"))
	body := rec.Body.String()

	if !strings.Contains(body, `action="/drive/start"`) {
		t.Error("a recovered Drive track must offer the start button")
	}
	if !strings.Contains(body, `action="/photos/upload/start"`) {
		t.Error("a recovered Photos track must offer a way to import")
	}
}

// The privacy policy and the terms are read by Google's reviewers before any
// human uses the service, so they must render without an identity, in both
// languages, and the policy must name the exact scope the code asks for.
func TestLegalPagesArePublicAndStateTheScope(t *testing.T) {
	opts := testOptions(&fakeRunner{})
	opts.Config.ContactEmail = "marco@example.org"
	opts.Config.PublicURL = "https://zlatan.example.org"
	handler := Routes(opts)

	for _, path := range []string{"/privacy", "/terms"} {
		for _, lang := range []string{"en", "it"} {
			r := request("GET", path, "")
			r.Header.Set("Accept-Language", lang)

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, r)

			if rec.Code != http.StatusOK {
				t.Fatalf("%s in %s: code = %d, want 200", path, lang, rec.Code)
			}
			body := rec.Body.String()
			if !strings.Contains(body, `lang="`+lang+`"`) {
				t.Errorf("%s did not render in %s", path, lang)
			}
			if !strings.Contains(body, "marco@example.org") {
				t.Errorf("%s in %s does not say who to contact", path, lang)
			}
		}
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request("GET", "/privacy", ""))
	body := rec.Body.String()

	// If the scope ever widens, the policy says so by construction: it renders
	// the same constant the OAuth client asks with.
	if !strings.Contains(body, oauth.DriveScope) {
		t.Errorf("the privacy policy must name the scope %q", oauth.DriveScope)
	}
	// Google requires this sentence verbatim for restricted scopes.
	if !strings.Contains(body, "Limited Use") {
		t.Error("the privacy policy must state that it follows the Limited Use requirements")
	}
	if !strings.Contains(body, "14 days") {
		t.Error("the privacy policy must state the real staging retention")
	}
}

// A reconnect message is stored as a catalogue key, and the wizard must render
// it in the reader's language. This is the reported bug: an English sentence
// used to reach an Italian reader because the runner stored prose.
func TestReconnectReasonRendersInTheReadersLanguage(t *testing.T) {
	opts := testOptions(&fakeRunner{})
	reason := core.EncodeProgress(core.Progress{Key: core.ReconnectGoogleCopy})
	if _, err := opts.State.SetDriveState(context.Background(), "marco", core.DriveSelecting, reason); err != nil {
		t.Fatal(err)
	}
	// A reconnect writes the same key into last_error, which the stopped screen
	// renders through .Why.
	m := opts.State.(*fakeState).migrations["marco"]
	m.LastError = reason
	opts.State.(*fakeState).migrations["marco"] = m
	handler := Routes(opts)

	r := request("GET", "/", "marco")
	r.Header.Set("Accept-Language", "it")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)
	body := rec.Body.String()

	if strings.Contains(body, core.ReconnectGoogleCopy) {
		t.Errorf("the raw key leaked to the page:\n%s", body)
	}
	if !strings.Contains(body, "Google non è collegato") {
		t.Errorf("the reconnect reason is not in Italian on the page:\n%s", body)
	}
}

// The stopped screen must inflect the count: "the check found 1 file", not "1
// files". The bug this guards against shipped, and is the sentence that was on
// the screen in the report.
func TestTheStoppedScreenInflectsASingleMismatch(t *testing.T) {
	opts := testOptions(&fakeRunner{})
	reason := core.EncodeProgress(core.Progress{Key: core.FailMismatch, Args: []int64{1}})
	m := opts.State.(*fakeState).migrations["marco"]
	m.DriveState = core.DriveFailed
	m.LastError = reason
	m.DriveProgress = reason
	opts.State.(*fakeState).migrations["marco"] = m

	handler := Routes(opts)
	r := request("GET", "/", "marco")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)
	body := rec.Body.String()

	if strings.Contains(body, "1 files") {
		t.Errorf("the page says \"1 files\", want \"1 file\":\n%s", body)
	}
	if !strings.Contains(body, "1 file that did not match") {
		t.Errorf("the singular mismatch reason is missing:\n%s", body)
	}
}

// The space at Google must be shown, so the size of what is being moved is
// visible. Drive is exact; Photos is stated as an upper bound because Google
// folds it into "other".
func TestTheWizardShowsTheSpaceAtGoogle(t *testing.T) {
	opts := testOptions(&fakeRunner{})
	m := opts.State.(*fakeState).migrations["marco"]
	m.DriveSourceBytes = 44_181_000_000
	m.GoogleOtherBytes = 12_000_000_000
	opts.State.(*fakeState).migrations["marco"] = m

	handler := Routes(opts)
	r := request("GET", "/", "marco")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)
	body := rec.Body.String()

	if !strings.Contains(body, "Google Drive: 41.1 GiB") {
		t.Errorf("the Drive space is not shown:\n%s", body)
	}
	if !strings.Contains(body, "up to 11.2 GiB, Gmail included") {
		t.Errorf("the Photos bound is not shown:\n%s", body)
	}
}
