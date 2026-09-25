// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marcodellemarche/zlatan/internal/config"
	"github.com/marcodellemarche/zlatan/internal/core"
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
	token      core.Token
	haveToken  bool
	driveBytes int64
	driveFiles int64
	photos     int64
}

func newFakeStore() *fakeStore {
	return &fakeStore{migration: core.Migration{User: "marco"}}
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

func (f *fakeStore) GetToken(_ context.Context, _, _ string) (core.Token, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.haveToken {
		return core.Token{}, errors.New("no token")
	}
	return f.token, nil
}

func (f *fakeStore) PutToken(_ context.Context, t core.Token) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.token, f.haveToken = t, true
	return nil
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
			ClientID:     "client-id",
			ClientSecret: "client-secret",
			RedirectURL:  "https://zlatan.example/cb",
			ShareAccount: "family@example.com",
		},
		Nextcloud: config.Nextcloud{URL: "http://nextcloud", AdminUser: "admin", AdminPassword: "pw"},
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
	store.token = core.Token{User: "marco", Provider: "google", Sealed: sealed, Scopes: oauth.DriveScope}
	store.haveToken = true
}

// sealerOf reaches into the runner for the sealer it was built with, so a
// test can seed a token the runner will be able to open.
func sealerOf(t *testing.T, r *Runner) oauth.Sealer {
	t.Helper()
	return r.sealer
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

	r.runDrive(context.Background(), "marco", store.token)

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

	r.runDrive(context.Background(), "marco", store.token)

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

	r.runDrive(context.Background(), "marco", store.token)

	if got := store.state().DriveState; got != core.DriveFailed {
		t.Fatalf("drive state = %q, want failed", got)
	}
	if store.state().LastError == "" {
		t.Error("a failure should leave an explanation")
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

func TestStartPhotosShareRecordsTheRoute(t *testing.T) {
	store := newFakeStore()
	r := newRunner(t, store, &fakeExecutor{})

	if err := r.StartPhotosShare(context.Background(), "marco"); err != nil {
		t.Fatalf("StartPhotosShare: %v", err)
	}
	m := store.state()
	if m.PhotosState != core.PhotosAwaitingShare {
		t.Fatalf("photos state = %q, want awaiting_share", m.PhotosState)
	}
	if !strings.Contains(m.PhotosProgress, "family@example.com") {
		t.Errorf("the progress should name the share address, got %q", m.PhotosProgress)
	}
}

func TestStartPhotosShareRefusesWithoutAccount(t *testing.T) {
	store := newFakeStore()
	cfg := testConfig()
	cfg.Google.ShareAccount = ""
	sealer, _ := core.NewSealer(core.Secret("k"))
	r := New(cfg, store, sealer, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if err := r.StartPhotosShare(context.Background(), "marco"); err == nil {
		t.Fatal("expected an error when no share account is configured")
	}
}

func TestSanitizeRejectsTraversal(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"marco", "marco"},
		{"../../etc/passwd", "______etc_passwd"},
		{"a/b", "a_b"},
		{"..", "unknown"},
		{"", "unknown"},
		{".hidden", "_hidden"},
		{"user@example.com", "user_example_com"},
		{"a..b", "a__b"},
	}
	for _, c := range cases {
		if got := sanitize(c.in); got != c.want {
			t.Errorf("sanitize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A sanitized identity must never escape the staging root.
func TestSanitizedPathStaysUnderStaging(t *testing.T) {
	base := "/staging"
	for _, hostile := range []string{"../../etc", "..", "/etc/passwd", "a/../../b"} {
		got := base + "/" + sanitize(hostile)
		if !strings.HasPrefix(got, base+"/") || strings.Contains(got, "..") {
			t.Errorf("sanitize(%q) escaped the staging root: %s", hostile, got)
		}
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
