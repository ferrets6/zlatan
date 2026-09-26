// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/marcodellemarche/zlatan/internal/immich"
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
	started   []string
	err       error
	immichKey string
}

func (f *fakeRunner) StartDrive(_ context.Context, user string) error {
	if f.err != nil {
		return f.err
	}
	f.started = append(f.started, "drive:"+user)
	return nil
}

func (f *fakeRunner) StartPhotosUpload(_ context.Context, user string) error {
	if f.err != nil {
		return f.err
	}
	f.started = append(f.started, "upload:"+user)
	return nil
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
		Nextcloud: config.Nextcloud{URL: "http://nextcloud"},
		Immich:    config.Immich{URL: "http://immich"},
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
	if !strings.Contains(body, "Takeout") {
		t.Error("the page should name the Takeout folder the watcher looks for")
	}
	// The Photos import runs as the person, so before their key is connected
	// the screen asks for it instead of offering a start button.
	if !strings.Contains(body, "/immich/connect") {
		t.Error("the page should ask for the Immich key before importing")
	}
}

func TestWizardRefusesUnauthenticated(t *testing.T) {
	opts := testOptions(&fakeRunner{})
	handler := Routes(opts)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request("GET", "/", ""))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", rec.Code)
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
	if _, err := opts.State.SetDriveState(context.Background(), "marco", core.DriveCopying, "copied 2 GiB"); err != nil {
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
	if !strings.Contains(body, "Connect Google first") {
		t.Error("the screen should tell the person to connect Google first")
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
