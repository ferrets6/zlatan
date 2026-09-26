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
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marcodellemarche/zlatan/internal/config"
	"github.com/marcodellemarche/zlatan/internal/core"
	"github.com/marcodellemarche/zlatan/internal/immich"
	"github.com/marcodellemarche/zlatan/internal/nextcloud"
	"github.com/marcodellemarche/zlatan/internal/notify"
	"github.com/marcodellemarche/zlatan/internal/oauth"
	"github.com/marcodellemarche/zlatan/internal/store"
)

// fakeExecutor records the commands it was asked to run.
type fakeExecutor struct {
	mu       sync.Mutex
	commands []command
	lines    []string
	err      error

	// byCommand scripts a specific subcommand (the first argument) with its own
	// lines and error, so a test can make "copy" succeed while "check" reports
	// differences, which is the whole point of the verification tests.
	byCommand map[string]scripted
	// byDownload scripts the sample check specifically. It is told apart from
	// the whole-tree check by the --download flag, so a test can make the size
	// pass find a problem the byte pass does not see.
	byDownload  scripted
	hasDownload bool
}

type scripted struct {
	lines []string
	err   error
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
	if len(args) > 0 && f.byCommand != nil {
		if s, ok := f.byCommand[args[0]]; ok {
			lines = append([]string(nil), s.lines...)
			err = s.err
		}
	}
	if f.hasDownload && containsArg(args, "--download") {
		lines = append([]string(nil), f.byDownload.lines...)
		err = f.byDownload.err
	}
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

// first returns the first recorded command whose args start with prefix. A run
// makes several calls (copy, then check), so a test that cares about one of
// them must say which rather than take whichever was last.
func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func (f *fakeExecutor) first(prefix ...string) (command, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.commands {
		if len(c.args) >= len(prefix) {
			match := true
			for i, p := range prefix {
				if c.args[i] != p {
					match = false
					break
				}
			}
			if match {
				return c, true
			}
		}
	}
	return command{}, false
}

// fakeStore is an in-memory Store.
type fakeStore struct {
	mu           sync.Mutex
	migration    core.Migration
	tokens       map[string]core.Token
	driveBytes   int64
	driveFiles   int64
	photos       int64
	waitSince    time.Time
	verification core.Verify
	finishedAt   time.Time
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

func (f *fakeStore) PutVerification(_ context.Context, v core.Verify) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.verification = v
	return nil
}

func (f *fakeStore) SetQuotaEstimate(_ context.Context, _ string, driveSource, used, total int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.migration.DriveSourceBytes, f.migration.QuotaUsedBytes, f.migration.QuotaTotalBytes = driveSource, used, total
	return nil
}

func (f *fakeStore) ListFinished(_ context.Context) ([]store.FinishedMigration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return []store.FinishedMigration{{User: f.migration.User, FinishedAt: f.finishedAt}}, nil
}

func (f *fakeStore) StampFinished(_ context.Context, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.finishedAt.IsZero() {
		f.finishedAt = time.Now()
	}
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
		Immich:    config.Immich{URL: "http://immich:2283"},
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

// seedImmich stores the person's own Immich API key, so a test can reach the
// import without a live Immich. The Photos half now requires it: without a
// key, Immich would file the import under whoever owns the key, so there is no
// safe fallback to a shared one.
func seedImmich(t *testing.T, store *fakeStore, sealer oauth.Sealer) {
	t.Helper()
	sealed, err := immich.SealCredentials(sealer, immich.Credentials{APIKey: "immich-personal-key", Email: "marco@example.com"})
	if err != nil {
		t.Fatalf("SealCredentials: %v", err)
	}
	store.tokens[immich.Provider] = core.Token{User: "marco", Provider: immich.Provider, Sealed: sealed}
}

// fakeImmich proves a key and names the account it belongs to.
type fakeImmich struct {
	me  immich.Me
	err error
}

func (f *fakeImmich) Validate(_ context.Context, apiKey string) (immich.Me, error) {
	if f.err != nil {
		return immich.Me{}, f.err
	}
	if apiKey == "" {
		return immich.Me{}, errors.New("empty key")
	}
	return f.me, nil
}

// fakeNextcloud is a Login Flow that a test drives by hand.
type fakeNextcloud struct {
	flow        nextcloud.Flow
	creds       nextcloud.Credentials
	done        bool
	pollErr     error
	loginName   string
	beginCalled bool
	usage       nextcloud.Usage
	quotaErr    error
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

func (f *fakeNextcloud) Quota(context.Context, string) (nextcloud.Usage, error) {
	if f.quotaErr != nil {
		return nextcloud.Usage{}, f.quotaErr
	}
	return f.usage, nil
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

	cmd, ok := exec.first("copy")
	if !ok {
		t.Fatal("no copy command was run")
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
	// A clean check: one file matched. The copy is the default (no lines).
	exec := &fakeExecutor{byCommand: map[string]scripted{
		"check": {lines: []string{"= file.txt"}},
		"lsf":   {lines: []string{"file.txt"}},
	}}
	r := newRunner(t, store, exec)
	seedToken(t, store, sealerOf(t, r))
	seedNextcloud(t, store, sealerOf(t, r))

	r.runDrive(context.Background(), "marco", mustToken(t, store, "google"))

	if got := store.state().DriveState; got != core.DriveDone {
		t.Fatalf("drive state = %q, want done", got)
	}
	if store.verification.Mismatch != 0 {
		t.Errorf("verification should have found no mismatch, got %+v", store.verification)
	}
}

// A check that finds a file missing on the destination must fail the track,
// not mark it done: the whole point of verifying is to catch this.
func TestRunDriveFailsWhenVerificationFindsMismatches(t *testing.T) {
	store := newFakeStore()
	exec := &fakeExecutor{byCommand: map[string]scripted{
		"check": {lines: []string{"= ok.txt", "+ missing.txt"}},
		"lsf":   {lines: []string{"ok.txt", "missing.txt"}},
	}}
	r := newRunner(t, store, exec)
	seedToken(t, store, sealerOf(t, r))
	seedNextcloud(t, store, sealerOf(t, r))

	r.runDrive(context.Background(), "marco", mustToken(t, store, "google"))

	if got := store.state().DriveState; got != core.DriveFailed {
		t.Fatalf("drive state = %q, want failed", got)
	}
	if store.verification.Mismatch < 1 {
		t.Errorf("verification should have found a mismatch, got %+v", store.verification)
	}
	if !strings.Contains(store.state().LastError, "did not match") {
		t.Errorf("the error should say the check failed, got %q", store.state().LastError)
	}
}

// The sample check must not re-check a file the size pass already found bad:
// it would only report the same mismatch twice. It samples from the files
// whose size matched, which is where a byte comparison adds information.
func TestVerifySampleExcludesKnownBadFiles(t *testing.T) {
	store := newFakeStore()
	exec := &fakeExecutor{
		byCommand: map[string]scripted{
			// The size pass: one matched, one missing.
			"check": {lines: []string{"= ok.txt", "+ missing.txt"}},
		},
		// The byte pass finds nothing wrong with the file it sampled.
		hasDownload: true,
		byDownload:  scripted{lines: []string{"= ok.txt"}},
	}
	r := newRunner(t, store, exec)

	v, err := r.VerifyDrive(context.Background(), "marco",
		oauth.Tokens{AccessToken: "a"}, nextcloud.Credentials{LoginName: "uid", AppPassword: "pw"})
	if err != nil {
		t.Fatalf("VerifyDrive: %v", err)
	}
	if v.Mismatch != 1 {
		t.Fatalf("expected exactly one mismatch, got %+v", v)
	}

	// Find the sample's --files-from list and assert it does not contain the
	// file the size pass already flagged.
	exec.mu.Lock()
	defer exec.mu.Unlock()
	for _, c := range exec.commands {
		for i, a := range c.args {
			if a != "--files-from" || i+1 >= len(c.args) {
				continue
			}
			raw, readErr := os.ReadFile(c.args[i+1])
			if readErr != nil {
				continue
			}
			if strings.Contains(string(raw), "missing.txt") {
				t.Errorf("the sample re-checked a known-bad file:\n%s", raw)
			}
			if !strings.Contains(string(raw), "ok.txt") {
				t.Errorf("the sample should draw from the files that matched, got:\n%s", raw)
			}
		}
	}
}

func TestRunPhotosImportEndsOnDone(t *testing.T) {
	store := newFakeStore()
	r := newRunner(t, store, &fakeExecutor{})
	seedImmich(t, store, sealerOf(t, r))

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
	seedImmich(t, store, sealerOf(t, r))

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

	cmd, ok := exec.first("copy")
	if !ok {
		t.Fatal("no copy command was run")
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

func TestParseRcloneSize(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{`{"count":42,"bytes":123456}`, 123456},
		{"{\n  \"count\": 3,\n  \"bytes\": 999999999999\n}", 999999999999},
		{`{"count":0,"bytes":0}`, 0},
		{"rclone: command not found", 0},
		{"", 0},
		{`{"bytes":-5}`, 0},
	}
	for _, c := range cases {
		if got := parseRcloneSize(c.in); got != c.want {
			t.Errorf("parseRcloneSize(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

// recordingNotifier captures what the runner tried to send.
type recordingNotifier struct {
	mu       sync.Mutex
	messages []notify.Message
}

func (n *recordingNotifier) Notify(_ context.Context, m notify.Message) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.messages = append(n.messages, m)
	return nil
}

func (n *recordingNotifier) all() []notify.Message {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]notify.Message(nil), n.messages...)
}

func TestRunDriveRecordsTheQuotaEstimate(t *testing.T) {
	store := newFakeStore()
	exec := &fakeExecutor{
		// `rclone size --json` is what the quota scan parses.
		byCommand: map[string]scripted{
			"size": {lines: []string{`{"count":42,"bytes":` + itoa(60*1024*1024*1024) + `}`}},
		},
	}
	r := newRunner(t, store, exec)
	seedToken(t, store, sealerOf(t, r))
	seedNextcloud(t, store, sealerOf(t, r))
	r = r.WithNextcloud(&fakeNextcloud{
		usage: nextcloud.Usage{Used: 50 * 1024 * 1024 * 1024, Available: 50 * 1024 * 1024 * 1024},
	})

	r.runDrive(context.Background(), "marco", mustToken(t, store, "google"))

	m := store.state()
	if m.DriveSourceBytes != 60*1024*1024*1024 {
		t.Errorf("DriveSourceBytes = %d, want 60 GiB", m.DriveSourceBytes)
	}
	if m.QuotaUsedBytes != 50*1024*1024*1024 {
		t.Errorf("QuotaUsedBytes = %d, want 50 GiB", m.QuotaUsedBytes)
	}
	if m.QuotaTotalBytes != 100*1024*1024*1024 {
		t.Errorf("QuotaTotalBytes = %d, want 100 GiB", m.QuotaTotalBytes)
	}
}

func TestRunDriveNotifiesOnSuccessAndFailure(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		store := newFakeStore()
		exec := &fakeExecutor{byCommand: map[string]scripted{
			"size":  {lines: []string{`{"count":1,"bytes":10}`}},
			"check": {lines: []string{"= a.txt"}},
		}}
		r := newRunner(t, store, exec)
		seedToken(t, store, sealerOf(t, r))
		seedNextcloud(t, store, sealerOf(t, r))
		r = r.WithNextcloud(&fakeNextcloud{})
		n := &recordingNotifier{}
		r = r.WithNotifier(n)

		r.runDrive(context.Background(), "marco", mustToken(t, store, "google"))
		// The notification is sent from a goroutine; wait briefly for it.
		waitFor(t, func() bool { return len(n.all()) > 0 })
		msgs := n.all()
		if len(msgs) == 0 {
			t.Fatal("no notification was sent on success")
		}
		if !strings.Contains(msgs[0].Title, "Nextcloud") {
			t.Errorf("success title = %q, want it to mention Nextcloud", msgs[0].Title)
		}
	})

	t.Run("failure", func(t *testing.T) {
		store := newFakeStore()
		exec := &fakeExecutor{byCommand: map[string]scripted{
			"copy": {err: errors.New("rclone blew up")},
		}}
		r := newRunner(t, store, exec)
		seedToken(t, store, sealerOf(t, r))
		seedNextcloud(t, store, sealerOf(t, r))
		n := &recordingNotifier{}
		r = r.WithNotifier(n)

		r.runDrive(context.Background(), "marco", mustToken(t, store, "google"))
		waitFor(t, func() bool { return len(n.all()) > 0 })
		msgs := n.all()
		if len(msgs) == 0 {
			t.Fatal("no notification was sent on failure")
		}
		if msgs[0].Priority != 4 {
			t.Errorf("failure priority = %d, want 4", msgs[0].Priority)
		}
	})
}

func TestPurgeStagingRemovesOnlyExpiredFinishedMigrations(t *testing.T) {
	store := newFakeStore()
	exec := &fakeExecutor{}
	r := newRunner(t, store, exec)
	r.cfg.StagingRetention = time.Hour

	dir := filepath.Join(r.cfg.StagingDir, core.SafeName("marco"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Finished two hours ago: past the one-hour retention.
	store.finishedAt = time.Now().Add(-2 * time.Hour)
	r.purgeStaging(context.Background())

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("staging should have been purged, stat err = %v", err)
	}
}

func TestPurgeStagingKeepsAFreshMigration(t *testing.T) {
	store := newFakeStore()
	exec := &fakeExecutor{}
	r := newRunner(t, store, exec)
	r.cfg.StagingRetention = 24 * time.Hour

	dir := filepath.Join(r.cfg.StagingDir, core.SafeName("marco"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	store.finishedAt = time.Now() // just finished

	r.purgeStaging(context.Background())

	if _, err := os.Stat(dir); err != nil {
		t.Errorf("staging should have been kept, stat err = %v", err)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// waitFor polls a condition for a short while. The notification is sent from a
// goroutine, so a test cannot assume it has landed by the time runDrive returns.
func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestConnectImmichStoresTheKeySealed(t *testing.T) {
	store := newFakeStore()
	r := newRunner(t, store, &fakeExecutor{})
	r = r.WithImmich(&fakeImmich{me: immich.Me{Email: "marco@example.com", Name: "Marco"}})

	me, err := r.ConnectImmich(context.Background(), "marco", "the-personal-key")
	if err != nil {
		t.Fatalf("ConnectImmich: %v", err)
	}
	if me.Email != "marco@example.com" {
		t.Errorf("me.Email = %q", me.Email)
	}

	// The key must be sealed in the store, not stored in the clear.
	tok, err := store.GetToken(context.Background(), "marco", immich.Provider)
	if err != nil {
		t.Fatalf("the key was not stored: %v", err)
	}
	if strings.Contains(string(tok.Sealed), "the-personal-key") {
		t.Fatal("the key reached the store unsealed")
	}
	// And it must come back out intact.
	creds, err := r.ImmichKey(context.Background(), "marco")
	if err != nil {
		t.Fatalf("ImmichKey: %v", err)
	}
	if creds.APIKey != "the-personal-key" {
		t.Errorf("ImmichKey = %q, want the key that was stored", creds.APIKey)
	}
}

func TestConnectImmichRefusesAKeyImmichRejects(t *testing.T) {
	store := newFakeStore()
	r := newRunner(t, store, &fakeExecutor{})
	r = r.WithImmich(&fakeImmich{err: errors.New("Immich did not accept that API key")})

	if _, err := r.ConnectImmich(context.Background(), "marco", "wrong"); err == nil {
		t.Fatal("a key Immich rejects must not be stored")
	}
	if _, err := store.GetToken(context.Background(), "marco", immich.Provider); err == nil {
		t.Error("a rejected key must not be stored")
	}
}

func TestRunPhotosImportUsesThePersonsOwnKey(t *testing.T) {
	store := newFakeStore()
	exec := &fakeExecutor{}
	r := newRunner(t, store, exec)
	seedImmich(t, store, sealerOf(t, r))

	staging := filepath.Join(r.cfg.StagingDir, core.SafeName("marco"))
	if err := os.MkdirAll(staging, 0o750); err != nil {
		t.Fatalf("staging: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staging, "takeout-1.zip"), []byte("zip"), 0o600); err != nil {
		t.Fatalf("write archive: %v", err)
	}

	r.runPhotosImport(context.Background(), "marco")

	cmd, ok := exec.first("upload")
	if !ok {
		t.Fatal("no immich-go upload was run")
	}
	// The key passed to immich-go must be the person's own, not a shared one:
	// Immich files every asset under the key's owner.
	joined := strings.Join(cmd.args, " ")
	if !strings.Contains(joined, "immich-personal-key") {
		t.Errorf("the import should use the person's own key, got: %v", cmd.args)
	}
}

func TestRunPhotosImportFailsWithoutAPersonalKey(t *testing.T) {
	store := newFakeStore()
	exec := &fakeExecutor{}
	r := newRunner(t, store, exec)

	staging := filepath.Join(r.cfg.StagingDir, core.SafeName("marco"))
	if err := os.MkdirAll(staging, 0o750); err != nil {
		t.Fatalf("staging: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staging, "takeout-1.zip"), []byte("zip"), 0o600); err != nil {
		t.Fatalf("write archive: %v", err)
	}

	r.runPhotosImport(context.Background(), "marco")

	// No key means no import: a shared fallback would misfile the photos, so
	// the run must fail loudly instead.
	if got := store.state().PhotosState; got != core.PhotosFailed {
		t.Fatalf("photos state = %q, want failed without a personal key", got)
	}
	if _, ok := exec.first("upload"); ok {
		t.Error("immich-go must not run without the person's own key")
	}
}

func TestStartPhotosUploadRequiresThePersonalKey(t *testing.T) {
	store := newFakeStore()
	r := newRunner(t, store, &fakeExecutor{})

	if err := r.StartPhotosUpload(context.Background(), "marco"); err == nil {
		t.Error("StartPhotosUpload should refuse without the person's Immich key")
	}

	seedImmich(t, store, sealerOf(t, r))
	if err := r.StartPhotosUpload(context.Background(), "marco"); err != nil {
		t.Errorf("StartPhotosUpload with a key: %v", err)
	}
}
