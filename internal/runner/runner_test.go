// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marcodellemarche/zlatan/internal/config"
	"github.com/marcodellemarche/zlatan/internal/core"
	"github.com/marcodellemarche/zlatan/internal/nextcloud"
	"github.com/marcodellemarche/zlatan/internal/oauth"
)

// fakeExecutor records the commands it was asked to run.
type fakeExecutor struct {
	mu       sync.Mutex
	commands []command
	lines    []string
	err      error
}

type command struct {
	name string
	args []string
	env  []string
}

func (f *fakeExecutor) Run(_ context.Context, name string, args []string, env []string, onLine func(string)) error {
	f.mu.Lock()
	f.commands = append(f.commands, command{name: name, args: args, env: env})
	lines := append([]string(nil), f.lines...)
	err := f.err
	f.mu.Unlock()

	for _, line := range lines {
		if onLine != nil {
			onLine(line)
		}
	}
	return err
}

func (f *fakeExecutor) last() (command, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.commands) == 0 {
		return command{}, false
	}
	return f.commands[len(f.commands)-1], true
}

// fakeStore is an in-memory Store.
type fakeStore struct {
	mu         sync.Mutex
	migration  core.Migration
	tokens     map[string]core.Token
	driveBytes int64
	driveFiles int64
	photos     int64
	waitSince  time.Time
}

func newFakeStore() *fakeStore {
	return &fakeStore{migration: core.Migration{User: "marco"}, tokens: map[string]core.Token{}}
}

func (f *fakeStore) GetMigration(_ context.Context, user string) (core.Migration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := f.migration
	m.User = user
	return m, nil
}

func (f *fakeStore) SetDriveState(_ context.Context, user string, state core.DriveState, progress string) (core.Migration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.migration.DriveState, f.migration.DriveProgress = state, progress
	return f.migration, nil
}

func (f *fakeStore) SetPhotosState(_ context.Context, user string, state core.PhotosState, progress string) (core.Migration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.migration.PhotosState, f.migration.PhotosProgress = state, progress
	return f.migration, nil
}

func (f *fakeStore) AddDriveProgress(_ context.Context, _ string, bytes, files int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.driveBytes += bytes
	f.driveFiles += files
	return nil
}

func (f *fakeStore) SetPhotosAssets(_ context.Context, _ string, assets int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.photos = assets
	return nil
}

func (f *fakeStore) SetError(_ context.Context, _ string, message string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.migration.LastError = message
	return nil
}

func (f *fakeStore) GetToken(_ context.Context, _, provider string) (core.Token, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tokens[provider]
	if !ok {
		return core.Token{}, errors.New("no token")
	}
	return t, nil
}

func (f *fakeStore) PutToken(_ context.Context, t core.Token) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens[t.Provider] = t
	return nil
}

func (f *fakeStore) DeleteToken(_ context.Context, _, provider string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.tokens, provider)
	return nil
}

func (f *fakeStore) ListAwaitingTakeout(_ context.Context) ([]core.TakeoutWait, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.migration.PhotosState == core.PhotosAwaitingTakeout {
		return []core.TakeoutWait{{User: f.migration.User, Since: f.waitSince}}, nil
	}
	return nil, nil
}

func (f *fakeStore) state() core.Migration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.migration
}

func testConfig() *config.Config {
	return &config.Config{
		StagingDir:    t_TempDir(),
		MaxConcurrent: 1,
		Google: config.Google{
			ClientID:      "client-id",
			ClientSecret:  "client-secret",
			RedirectURL:   "https://zlatan.example/cb",
			TakeoutFolder: "Takeout",
		},
		Nextcloud: config.Nextcloud{URL: "http://nextcloud"},
		Immich:    config.Immich{URL: "http://immich:2283", APIKey: "immich-key"},
	}
}

// t_TempDir exists because testConfig has no *testing.T; the caller overrides
// StagingDir when it needs a real directory.
func t_TempDir() string { return "/tmp/zlatan-test-staging" }

func newRunner(t *testing.T, store Store, exec Executor) *Runner {
	t.Helper()
	cfg := testConfig()
	cfg.StagingDir = t.TempDir()
	sealer, err := core.NewSealer(core.Secret("token-key"))
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}
	r := New(cfg, store, sealer, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return r.WithExecutor(exec)
}

func seedToken(t *testing.T, store *fakeStore, sealer oauth.Sealer) {
	t.Helper()
	sealed, err := oauth.SealJSON(sealer, oauth.Tokens{
		AccessToken:  "ya29.access",
		RefreshToken: "1//refresh",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("SealJSON: %v", err)
	}
	store.tokens["google"] = core.Token{User: "marco", Provider: "google", Sealed: sealed, Scopes: oauth.DriveScope}
}

// seedNextcloud stores a Nextcloud app password the runner can open, so a test
// can reach the copy step without a live Login Flow.
func seedNextcloud(t *testing.T, store *fakeStore, sealer oauth.Sealer) {
	t.Helper()
	sealed, err := nextcloud.SealCredentials(sealer, nextcloud.Credentials{
		Server:      "https://cloud.example",
		LoginName:   "marco-uid",
		AppPassword: "app-password-1234",
	})
	if err != nil {
		t.Fatalf("SealCredentials: %v", err)
	}
	store.tokens[nextcloud.Provider] = core.Token{User: "marco", Provider: nextcloud.Provider, Sealed: sealed}
}

// fakeNextcloud is a Login Flow that a test drives by hand.
type fakeNextcloud struct {
	flow        nextcloud.Flow
	creds       nextcloud.Credentials
	done        bool
	pollErr     error
	loginName   string
	beginCalled bool
}

func (f *fakeNextcloud) BeginFlow(context.Context) (nextcloud.Flow, error) {
	f.beginCalled = true
	return f.flow, nil
}

func (f *fakeNextcloud) Poll(context.Context, string) (nextcloud.Credentials, bool, error) {
	if f.pollErr != nil {
		return nextcloud.Credentials{}, false, f.pollErr
	}
	return f.creds, f.done, nil
}

func (f *fakeNextcloud) DAVURL(loginName string) string {
	f.loginName = loginName
	return "http://nextcloud/remote.php/dav/files/" + loginName
}

// sealerOf reaches into the runner for the sealer it was built with, so a
// test can seed a token the runner will be able to open.
func sealerOf(t *testing.T, r *Runner) oauth.Sealer {
	t.Helper()
	return r.sealer
}

func mustToken(t *testing.T, store *fakeStore, provider string) core.Token {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	tok, ok := store.tokens[provider]
	if !ok {
		t.Fatalf("no %s token was seeded", provider)
	}
	return tok
}

func TestStartNextcloudReturnsTheLoginURL(t *testing.T) {
	store := newFakeStore()
	nc := &fakeNextcloud{flow: nextcloud.Flow{PollToken: "poll", LoginURL: "https://cloud.example/login/v2/flow/x"}}
	r := newRunner(t, store, &fakeExecutor{}).WithNextcloud(nc)

	url, err := r.StartNextcloud(context.Background(), "marco")
	if err != nil {
		t.Fatalf("StartNextcloud: %v", err)
	}
	if url != "https://cloud.example/login/v2/flow/x" {
		t.Errorf("url = %q", url)
	}
	// The poll token must be sealed and stored, so a later request (or a
	// restart) can finish the flow without starting a new one.
	if _, err := store.GetToken(context.Background(), "marco", nextcloudFlowProvider); err != nil {
		t.Errorf("the poll token was not stored: %v", err)
	}
}

func TestPollNextcloudStoresCredentialsWhenGranted(t *testing.T) {
	store := newFakeStore()
	nc := &fakeNextcloud{
		flow:  nextcloud.Flow{PollToken: "poll"},
		creds: nextcloud.Credentials{Server: "https://cloud.example", LoginName: "marco-uid", AppPassword: "pw"},
		done:  true,
	}
	r := newRunner(t, store, &fakeExecutor{}).WithNextcloud(nc)
	if _, err := r.StartNextcloud(context.Background(), "marco"); err != nil {
		t.Fatalf("StartNextcloud: %v", err)
	}

	state, err := r.PollNextcloud(context.Background(), "marco")
	if err != nil {
		t.Fatalf("PollNextcloud: %v", err)
	}
	if state != core.DriveSelecting {
		t.Errorf("state = %q, want selecting", state)
	}
	// The credentials must be stored, the spent flow token gone.
	if _, err := store.GetToken(context.Background(), "marco", nextcloud.Provider); err != nil {
		t.Errorf("the credentials were not stored: %v", err)
	}
	if _, err := store.GetToken(context.Background(), "marco", nextcloudFlowProvider); err == nil {
		t.Error("the spent flow token should have been deleted")
	}
}

func TestPollNextcloudStaysPendingUntilGranted(t *testing.T) {
	store := newFakeStore()
	nc := &fakeNextcloud{flow: nextcloud.Flow{PollToken: "poll"}, done: false}
	r := newRunner(t, store, &fakeExecutor{}).WithNextcloud(nc)
	if _, err := r.StartNextcloud(context.Background(), "marco"); err != nil {
		t.Fatalf("StartNextcloud: %v", err)
	}

	state, err := r.PollNextcloud(context.Background(), "marco")
	if err != nil {
		t.Fatalf("PollNextcloud: %v", err)
	}
	if state != core.DriveConsentPending {
		t.Errorf("state = %q, want consent_pending", state)
	}
	if _, err := store.GetToken(context.Background(), "marco", nextcloud.Provider); err == nil {
		t.Error("no credentials should be stored while the flow is pending")
	}
}

func TestStartDriveRefusesWithoutToken(t *testing.T) {
	store := newFakeStore()
	r := newRunner(t, store, &fakeExecutor{})

	err := r.StartDrive(context.Background(), "marco")
	if err == nil {
		t.Fatal("expected an error when Google is not connected")
	}
	if !strings.Contains(err.Error(), "connect Google") {
		t.Errorf("the error should tell the person what to do, got: %v", err)
	}
}

func TestStartDriveRefusesWithoutConfig(t *testing.T) {
	store := newFakeStore()
	cfg := testConfig()
	cfg.Nextcloud = config.Nextcloud{}
	sealer, _ := core.NewSealer(core.Secret("k"))
	r := New(cfg, store, sealer, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if err := r.StartDrive(context.Background(), "marco"); err == nil {
		t.Fatal("expected an error when Nextcloud is not configured")
	}
}

// The Drive copy must run rclone with the token in the environment and never
// on the command line, so a token cannot leak through the process list.
func TestRunDriveUsesRcloneAndPassesTokenViaEnv(t *testing.T) {
	store := newFakeStore()
	exec := &fakeExecutor{}
	r := newRunner(t, store, exec)
	seedToken(t, store, sealerOf(t, r))
	seedNextcloud(t, store, sealerOf(t, r))

	r.runDrive(context.Background(), "marco", mustToken(t, store, "google"))

	cmd, ok := exec.last()
	if !ok {
		t.Fatal("no command was run")
	}
	if cmd.name != "rclone" {
		t.Fatalf("command = %q, want rclone", cmd.name)
	}
	if len(cmd.args) == 0 || cmd.args[0] != "copy" {
		t.Errorf("args = %v, want a copy", cmd.args)
	}

	// The token must be in the environment, not the arguments.
	joined := strings.Join(cmd.args, " ")
	if strings.Contains(joined, "refresh") || strings.Contains(joined, "ya29") {
		t.Fatalf("the token leaked into the command line: %s", joined)
	}
	env := strings.Join(cmd.env, "\n")
	if !strings.Contains(env, "RCLONE_CONFIG_GDRIVE_TOKEN=") {
		t.Error("the token was not passed through the environment")
	}
	if !strings.Contains(env, "drive.readonly") {
		t.Error("the scope was not passed through the environment")
	}

	// The copy writes into Nextcloud, so the WebDAV remote must be there too,
	// with the password obscured the way rclone requires.
	if !strings.Contains(env, "RCLONE_CONFIG_NC_TYPE=webdav") {
		t.Error("the Nextcloud remote was not configured")
	}
	if !strings.Contains(env, "RCLONE_CONFIG_NC_URL=http://nextcloud/remote.php/dav/files/marco-uid") {
		t.Errorf("the Nextcloud DAV URL is wrong:\n%s", env)
	}
	if strings.Contains(env, "RCLONE_CONFIG_NC_PASS=app-password-1234") {
		t.Error("the Nextcloud password must be obscured, not cleartext")
	}
	if !strings.Contains(env, "RCLONE_CONFIG_NC_PASS=") {
		t.Error("the Nextcloud password was not passed through the environment")
	}
	// The destination must be the Nextcloud remote, not a local staging path.
	if !slices.Contains(cmd.args, "nc:Google Drive") {
		t.Errorf("the destination should be the Nextcloud remote, got %v", cmd.args)
	}
}

func TestRunDriveRecordsProgress(t *testing.T) {
	store := newFakeStore()
	exec := &fakeExecutor{
		lines: []string{
			"Transferred:   1.000 GiB / 4.000 GiB, 25%, 10.0 MiB/s, ETA 5m0s (xfr#1)",
			"Transferred:   2.000 GiB / 4.000 GiB, 50%, 10.0 MiB/s, ETA 2m30s (xfr#2)",
		},
	}
	r := newRunner(t, store, exec)
	seedToken(t, store, sealerOf(t, r))
	seedNextcloud(t, store, sealerOf(t, r))

	r.runDrive(context.Background(), "marco", mustToken(t, store, "google"))

	// Cumulative stats must be turned into deltas: 2 GiB total, not 3.
	got := store.driveBytes
	want := int64(2 * 1024 * 1024 * 1024)
	if got != want {
		t.Errorf("recorded bytes = %d, want %d (cumulative stats must not be summed)", got, want)
	}
	if store.driveFiles != 2 {
		t.Errorf("recorded files = %d, want 2", store.driveFiles)
	}
}

func TestRunDriveFailureMarksTheTrackFailed(t *testing.T) {
	store := newFakeStore()
	exec := &fakeExecutor{err: errors.New("rclone exited 1")}
	r := newRunner(t, store, exec)
	seedToken(t, store, sealerOf(t, r))
	seedNextcloud(t, store, sealerOf(t, r))

	r.runDrive(context.Background(), "marco", mustToken(t, store, "google"))

	if got := store.state().DriveState; got != core.DriveFailed {
		t.Fatalf("drive state = %q, want failed", got)
	}
	if store.state().LastError == "" {
		t.Error("a failure should leave an explanation")
	}
}

// A clean copy must end on done, not on verifying: parking there would tell
// the person a check is running when nothing is. Verification is a later
// phase and will run before this transition when it exists.
func TestRunDriveEndsOnDone(t *testing.T) {
	store := newFakeStore()
	r := newRunner(t, store, &fakeExecutor{})
	seedToken(t, store, sealerOf(t, r))
	seedNextcloud(t, store, sealerOf(t, r))

	r.runDrive(context.Background(), "marco", mustToken(t, store, "google"))

	if got := store.state().DriveState; got != core.DriveDone {
		t.Fatalf("drive state = %q, want done", got)
	}
}

func TestRunPhotosImportEndsOnDone(t *testing.T) {
	store := newFakeStore()
	r := newRunner(t, store, &fakeExecutor{})

	// A real Takeout archive in the person's staging directory, so the import
	// has something to find and gets past the empty check.
	staging := filepath.Join(r.cfg.StagingDir, core.SafeName("marco"))
	if err := os.MkdirAll(staging, 0o750); err != nil {
		t.Fatalf("staging: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staging, "takeout-1.zip"), []byte("zip"), 0o600); err != nil {
		t.Fatalf("write archive: %v", err)
	}

	r.runPhotosImport(context.Background(), "marco")

	if got := store.state().PhotosState; got != core.PhotosDone {
		t.Fatalf("photos state = %q, want done", got)
	}
}

func TestRunPhotosImportRefusesWithoutArchives(t *testing.T) {
	store := newFakeStore()
	exec := &fakeExecutor{}
	r := newRunner(t, store, exec)

	r.runPhotosImport(context.Background(), "marco")

	if got := store.state().PhotosState; got != core.PhotosFailed {
		t.Fatalf("photos state = %q, want failed", got)
	}
	if !strings.Contains(store.state().LastError, "Takeout") {
		t.Errorf("the error should mention the Takeout, got %q", store.state().LastError)
	}
}

func TestStartPhotosTakeoutRecordsTheRoute(t *testing.T) {
	store := newFakeStore()
	r := newRunner(t, store, &fakeExecutor{})
	seedToken(t, store, sealerOf(t, r))

	if err := r.StartPhotosTakeout(context.Background(), "marco"); err != nil {
		t.Fatalf("StartPhotosTakeout: %v", err)
	}
	if m := store.state(); m.PhotosState != core.PhotosAwaitingTakeout {
		t.Fatalf("photos state = %q, want awaiting_takeout", m.PhotosState)
	}
}

// Without the Drive token there is no Drive to watch, so the route is refused
// up front rather than leaving the person on a screen that can never move.
func TestStartPhotosTakeoutRefusesWithoutGoogleToken(t *testing.T) {
	store := newFakeStore()
	r := newRunner(t, store, &fakeExecutor{})

	if err := r.StartPhotosTakeout(context.Background(), "marco"); err == nil {
		t.Fatal("expected an error when Google is not connected")
	}
}

// takeoutReady must say "not yet" for a folder that is missing or still being
// written, and only "ready" once every part is present and non-empty: an
// import of a half-written export is the failure this whole step prevents.
func TestTakeoutReady(t *testing.T) {
	sealedTokens := func(t *testing.T, r *Runner) oauth.Tokens {
		t.Helper()
		return oauth.Tokens{AccessToken: "a", RefreshToken: "r", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour)}
	}

	cases := []struct {
		name string
		out  []string
		err  error
		want bool
	}{
		{name: "folder not there yet", err: errors.New("directory not found"), want: false},
		{name: "empty listing", out: []string{"null"}, want: false},
		{name: "one complete part", out: []string{`[{"Name":"takeout-1.zip","Size":100}]`}, want: true},
		{name: "a part still being written", out: []string{`[{"Name":"takeout-1.zip","Size":100},{"Name":"takeout-2.zip","Size":0}]`}, want: false},
		{name: "only a non-zip", out: []string{`[{"Name":"archive_browser.html","Size":10}]`}, want: false},
		{name: "zip plus a non-zip", out: []string{`[{"Name":"takeout-1.zip","Size":10},{"Name":"archive_browser.html","Size":10}]`}, want: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := newFakeStore()
			exec := &fakeExecutor{lines: c.out, err: c.err}
			r := newRunner(t, store, exec)
			got, err := r.takeoutReady(context.Background(), sealedTokens(t, r))
			if err != nil {
				t.Fatalf("takeoutReady: %v", err)
			}
			if got != c.want {
				t.Errorf("takeoutReady = %v, want %v", got, c.want)
			}
		})
	}
}

// A wait that has run past the configured limit must end, not run forever: the
// person is told to use the upload route instead. Otherwise they sit on a
// screen that can never move, and Google's own archive link expires anyway.
func TestCheckTakeoutGivesUpAfterMaxWait(t *testing.T) {
	store := newFakeStore()
	exec := &fakeExecutor{}
	r := newRunner(t, store, exec)
	seedToken(t, store, sealerOf(t, r))
	// The wait started well past the limit.
	r.cfg.TakeoutMaxWait = time.Hour

	w := core.TakeoutWait{User: "marco", Since: time.Now().Add(-48 * time.Hour)}
	if err := r.checkTakeout(context.Background(), w); err != nil {
		t.Fatalf("checkTakeout: %v", err)
	}

	m := store.state()
	if m.PhotosState != core.PhotosFailed {
		t.Fatalf("photos state = %q, want failed", m.PhotosState)
	}
	if !strings.Contains(m.LastError, "upload") {
		t.Errorf("the message should point at the upload route, got %q", m.LastError)
	}
	// No rclone call should have been made: there was nothing to look at.
	if _, ok := exec.last(); ok {
		t.Error("an expired wait must not run a command")
	}
}

// The Drive copy must exclude the Takeout folder: otherwise the "Add to Drive"
// archive lands in Nextcloud as files, when the photos belong in Immich.
func TestRunDriveExcludesTheTakeoutFolder(t *testing.T) {
	store := newFakeStore()
	exec := &fakeExecutor{}
	r := newRunner(t, store, exec)
	seedToken(t, store, sealerOf(t, r))
	seedNextcloud(t, store, sealerOf(t, r))

	r.runDrive(context.Background(), "marco", mustToken(t, store, "google"))

	cmd, ok := exec.last()
	if !ok {
		t.Fatal("no command was run")
	}
	joined := strings.Join(cmd.args, " ")
	if !strings.Contains(joined, "--exclude") || !strings.Contains(joined, "/Takeout/**") {
		t.Errorf("the Drive copy should exclude the Takeout folder, got: %v", cmd.args)
	}
}

func TestParseRcloneStats(t *testing.T) {
	cases := []struct {
		line      string
		wantBytes int64
		wantFiles int64
		wantOK    bool
	}{
		{"Transferred:   1.500 GiB / 4.000 GiB, 37%, 10.0 MiB/s, ETA 4m0s (xfr#3)", int64(1.5 * 1024 * 1024 * 1024), 3, true},
		{"Transferred:   512 B / 1.000 KiB, 50%, 1.0 KiB/s, ETA 1s (xfr#1)", 512, 1, true},
		{"Errors: 0", 0, 0, false},
		{"", 0, 0, false},
	}
	for _, c := range cases {
		bytes, files, ok := parseRcloneStats(c.line)
		if ok != c.wantOK {
			t.Errorf("parseRcloneStats(%q) ok = %v, want %v", c.line, ok, c.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if bytes != c.wantBytes || files != c.wantFiles {
			t.Errorf("parseRcloneStats(%q) = %d/%d, want %d/%d",
				c.line, bytes, files, c.wantBytes, c.wantFiles)
		}
	}
}
