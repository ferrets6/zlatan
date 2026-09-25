// SPDX-License-Identifier: AGPL-3.0-or-later

// Package runner executes the actual migration: it drives rclone for the Drive
// half and immich-go for the Photos half, and reports progress through the
// store.
//
// Two rules govern everything here:
//
//   - Arguments are built in Go from validated values. No user input is ever
//     interpolated into a command line, so there is no injection surface.
//   - Secrets reach rclone through its environment, never through a config
//     file on disk, so a token cannot be left behind by a crash.
package runner

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/marcodellemarche/zlatan/internal/config"
	"github.com/marcodellemarche/zlatan/internal/core"
	"github.com/marcodellemarche/zlatan/internal/nextcloud"
	"github.com/marcodellemarche/zlatan/internal/oauth"
)

// DriveDestination is the folder inside Nextcloud that the Drive half writes
// into. It matches the folder the manual runbook uses
// (docs/migration-new-users.md), so a person who migrated by hand and one who
// used Zlatan end up with the same layout.
const DriveDestination = "Google Drive"

// nextcloudFlowProvider is the token provider under which the in-flight Login
// Flow poll token is stored, separate from the granted credentials.
const nextcloudFlowProvider = "nextcloud-flow"

// Store is the persistence the runner writes progress to.
type Store interface {
	GetMigration(ctx context.Context, user string) (core.Migration, error)
	SetDriveState(ctx context.Context, user string, state core.DriveState, progress string) (core.Migration, error)
	SetPhotosState(ctx context.Context, user string, state core.PhotosState, progress string) (core.Migration, error)
	AddDriveProgress(ctx context.Context, user string, bytes, files int64) error
	SetPhotosAssets(ctx context.Context, user string, assets int64) error
	SetError(ctx context.Context, user, message string) error
	GetToken(ctx context.Context, user, provider string) (core.Token, error)
	PutToken(ctx context.Context, t core.Token) error
	DeleteToken(ctx context.Context, user, provider string) error
}

// Sealer opens the stored tokens. The runner never sees a token in the clear
// except for the moment it hands it to rclone.
type Sealer interface {
	Open(sealed []byte) ([]byte, error)
	Seal(plaintext []byte) ([]byte, error)
}

// NextcloudFlow is the Login Flow v2 the runner drives to obtain a per-user
// app password. It is an interface so the runner can be tested without a live
// Nextcloud.
type NextcloudFlow interface {
	BeginFlow(ctx context.Context) (nextcloud.Flow, error)
	Poll(ctx context.Context, token string) (nextcloud.Credentials, bool, error)
	DAVURL(loginName string) string
}

// Executor runs a command and streams its output. It is an interface so the
// runner can be tested without rclone or immich-go installed.
type Executor interface {
	Run(ctx context.Context, name string, args []string, env []string, onLine func(string)) error
}

// Runner is the production Executor: it actually starts processes.
type Runner struct {
	cfg     *config.Config
	store   Store
	sealer  Sealer
	log     *slog.Logger
	exec    Executor
	nc      NextcloudFlow
	limiter chan struct{}
}

// New builds the runner. The limiter serialises heavy migrations: immich-go
// and Immich's own jobs already saturate this single-node homelab, so running
// two at once makes both slower and risks the memory limits.
func New(cfg *config.Config, store Store, sealer Sealer, log *slog.Logger) *Runner {
	limit := cfg.MaxConcurrent
	if limit < 1 {
		limit = 1
	}
	r := &Runner{
		cfg:     cfg,
		store:   store,
		sealer:  sealer,
		log:     log,
		exec:    &OSExecutor{Log: log},
		limiter: make(chan struct{}, limit),
	}
	if cfg.Nextcloud.URL != "" {
		client, err := nextcloud.New(cfg.Nextcloud.URL, 0)
		if err != nil {
			log.Error("build the Nextcloud client", "error", err)
		} else {
			r.nc = client
		}
	}
	return r
}

// WithExecutor replaces the process runner. Used by tests.
func (r *Runner) WithExecutor(e Executor) *Runner {
	r.exec = e
	return r
}

// WithNextcloud replaces the Login Flow client. Used by tests.
func (r *Runner) WithNextcloud(nc NextcloudFlow) *Runner {
	r.nc = nc
	return r
}

// StartNextcloud begins the Nextcloud Login Flow and returns the URL the
// person must open to grant access. Nothing is polled yet: the person has to
// click first, and the wizard's status polling is what notices the grant.
func (r *Runner) StartNextcloud(ctx context.Context, user string) (string, error) {
	if r.nc == nil {
		return "", errors.New("Nextcloud is not configured")
	}
	flow, err := r.nc.BeginFlow(ctx)
	if err != nil {
		return "", err
	}
	// Store the poll token so a later request can finish the flow, and so a
	// restart does not lose it. It is short-lived and single-use.
	sealed, err := r.sealer.Seal([]byte(flow.PollToken))
	if err != nil {
		return "", err
	}
	if err := r.store.PutToken(ctx, core.Token{
		User:     user,
		Provider: nextcloudFlowProvider,
		Sealed:   sealed,
	}); err != nil {
		return "", err
	}
	return flow.LoginURL, nil
}

// PollNextcloud asks whether the person granted access. It is called from the
// wizard's status polling, so the grant is picked up without a second click.
// When it succeeds, the app password is sealed and stored, and the Drive
// track moves to "selecting".
func (r *Runner) PollNextcloud(ctx context.Context, user string) (core.DriveState, error) {
	if r.nc == nil {
		return "", errors.New("Nextcloud is not configured")
	}
	// Already granted: nothing to do.
	if _, err := r.store.GetToken(ctx, user, nextcloud.Provider); err == nil {
		return core.DriveSelecting, nil
	}

	flowTok, err := r.store.GetToken(ctx, user, nextcloudFlowProvider)
	if err != nil {
		return "", nil // no flow in progress; the wizard offers to start one
	}
	raw, err := r.sealer.Open(flowTok.Sealed)
	if err != nil {
		return "", err
	}

	creds, done, err := r.nc.Poll(ctx, string(raw))
	if err != nil {
		return "", err
	}
	if !done {
		return core.DriveConsentPending, nil
	}

	sealed, err := nextcloud.SealCredentials(r.sealer, creds)
	if err != nil {
		return "", err
	}
	if err := r.store.PutToken(ctx, core.Token{
		User:     user,
		Provider: nextcloud.Provider,
		Sealed:   sealed,
	}); err != nil {
		return "", err
	}
	// The flow token is spent: the credentials replace it.
	_ = r.store.DeleteToken(ctx, user, nextcloudFlowProvider)

	if _, err := r.store.SetDriveState(ctx, user, core.DriveSelecting,
		"Nextcloud is connected: ready to copy"); err != nil {
		return "", err
	}
	return core.DriveSelecting, nil
}

// StartDrive queues the Drive migration and returns immediately: the copy runs
// in the background and reports through the store, so closing the browser does
// not stop it.
func (r *Runner) StartDrive(ctx context.Context, user string) error {
	if !r.cfg.Google.Configured() {
		return errors.New("the Google OAuth client is not configured")
	}
	if !r.cfg.Nextcloud.Configured() {
		return errors.New("Nextcloud is not configured")
	}
	tok, err := r.store.GetToken(ctx, user, "google")
	if err != nil {
		return fmt.Errorf("%w: connect Google before starting the Drive copy", err)
	}
	if _, err := r.store.GetToken(ctx, user, nextcloud.Provider); err != nil {
		return fmt.Errorf("%w: connect Nextcloud before starting the Drive copy", err)
	}

	// Detach from the request context: the work outlives the HTTP request.
	go r.runDrive(context.WithoutCancel(ctx), user, tok)
	return nil
}

func (r *Runner) runDrive(ctx context.Context, user string, tok core.Token) {
	r.acquire()
	defer r.release()

	ctx, cancel := context.WithTimeout(ctx, 24*time.Hour)
	defer cancel()

	if _, err := r.store.SetDriveState(ctx, user, core.DriveCopying, "preparing the copy"); err != nil {
		r.log.Error("runDrive: set state", "user", user, "error", err)
		return
	}

	tokens, err := r.openTokens(tok)
	if err != nil {
		r.failDrive(ctx, user, "the access token cannot be read: reconnect Google")
		return
	}

	// The Nextcloud app password the person granted through the Login Flow.
	ncTok, err := r.store.GetToken(ctx, user, nextcloud.Provider)
	if err != nil {
		r.failDrive(ctx, user, "Nextcloud is not connected: connect it before copying")
		return
	}
	creds, err := nextcloud.OpenCredentials(r.sealer, ncTok.Sealed)
	if err != nil {
		r.failDrive(ctx, user, "the Nextcloud credential cannot be read: reconnect Nextcloud")
		return
	}
	if r.nc == nil {
		r.failDrive(ctx, user, "Nextcloud is not configured")
		return
	}

	// rclone copies Google Drive straight into Nextcloud over WebDAV, so there
	// is no local staging for the Drive half and no second import step: the
	// files land in the person's space as themselves, with their own rights.
	dest := "nc:" + DriveDestination
	args := []string{
		"copy", "gdrive:", dest,
		"--transfers", "4",
		"--checkers", "8",
		"--fast-list",
		"--drive-export-formats", "docx,xlsx,pptx,svg",
		"--stats", "10s",
		"--stats-one-line",
	}
	env, err := r.rcloneEnv(tokens, creds)
	if err != nil {
		r.failDrive(ctx, user, "the copy could not be prepared")
		r.log.Error("runDrive: build environment", "user", user, "error", err)
		return
	}

	// rclone's stats are cumulative totals, not deltas, so the counters are
	// advanced by the difference from the previous line. Summing the totals
	// would multiply the real figure by the number of stat lines.
	var (
		lastProgress  string
		reportedBytes int64
		reportedFiles int64
	)
	onLine := func(line string) {
		bytes, files, ok := parseRcloneStats(line)
		if !ok {
			return
		}
		lastProgress = fmt.Sprintf("copied %s in %d files", core.FormatBytes(bytes), files)
		// Clamp both at zero: a re-scan can report a lower count, and a
		// negative delta would subtract from the running total.
		if deltaBytes, deltaFiles := bytes-reportedBytes, files-reportedFiles; deltaBytes > 0 || deltaFiles > 0 {
			if deltaBytes < 0 {
				deltaBytes = 0
			}
			if deltaFiles < 0 {
				deltaFiles = 0
			}
			if err := r.store.AddDriveProgress(ctx, user, deltaBytes, deltaFiles); err != nil {
				r.log.Debug("runDrive: progress", "error", err)
			}
		}
		reportedBytes, reportedFiles = bytes, files
		if _, err := r.store.SetDriveState(ctx, user, core.DriveCopying, lastProgress); err != nil {
			r.log.Debug("runDrive: set progress", "error", err)
		}
	}

	if err := r.exec.Run(ctx, "rclone", args, env, onLine); err != nil {
		r.failDrive(ctx, user, "the copy from Google Drive did not finish")
		r.log.Error("runDrive: rclone", "user", user, "error", err)
		return
	}

	// The copy writes into Nextcloud directly, so there is no separate import
	// step; verification is the next phase and is run by the caller.
	if _, err := r.store.SetDriveState(ctx, user, core.DriveVerifying, "verifying the copied files"); err != nil {
		r.log.Error("runDrive: set verifying", "user", user, "error", err)
	}
	r.log.Info("runDrive: copy finished", "user", user, "progress", lastProgress)
}

func (r *Runner) failDrive(ctx context.Context, user, message string) {
	if err := r.store.SetError(ctx, user, message); err != nil {
		r.log.Error("failDrive: set error", "user", user, "error", err)
	}
	if _, err := r.store.SetDriveState(ctx, user, core.DriveFailed, message); err != nil {
		r.log.Error("failDrive: set state", "user", user, "error", err)
	}
}

// StartPhotosUpload queues the import of an already-uploaded Takeout. The
// upload itself is handled elsewhere; this is the immich-go step.
func (r *Runner) StartPhotosUpload(ctx context.Context, user string) error {
	if !r.cfg.Immich.Configured() {
		return errors.New("Immich is not configured")
	}
	go r.runPhotosImport(context.WithoutCancel(ctx), user)
	return nil
}

// StartPhotosShare records that the person is on the "Add to Drive" route and
// leaves them waiting for the shared folder to appear. The polling that
// notices it is a later phase.
func (r *Runner) StartPhotosShare(ctx context.Context, user string) error {
	if r.cfg.Google.ShareAccount == "" {
		return errors.New("no Takeout share account is configured")
	}
	if _, err := r.store.SetPhotosState(ctx, user, core.PhotosAwaitingShare,
		"share the Takeout folder with "+r.cfg.Google.ShareAccount); err != nil {
		return err
	}
	return nil
}

func (r *Runner) runPhotosImport(ctx context.Context, user string) {
	r.acquire()
	defer r.release()

	ctx, cancel := context.WithTimeout(ctx, 24*time.Hour)
	defer cancel()

	if _, err := r.store.SetPhotosState(ctx, user, core.PhotosImporting, "importing into Immich"); err != nil {
		r.log.Error("runPhotosImport: set state", "user", user, "error", err)
		return
	}

	// The upload store writes completed archives directly under the person's
	// staging directory, so the glob reads the same place it writes. Two
	// different layouts here would mean an import that silently finds nothing.
	staging := filepath.Join(r.cfg.StagingDir, core.SafeName(user))
	archives, err := filepath.Glob(filepath.Join(staging, "*.zip"))
	if err != nil {
		r.failPhotos(ctx, user, "the staging area could not be read")
		return
	}
	if len(archives) == 0 {
		r.failPhotos(ctx, user, "no Takeout archive found: upload one first")
		return
	}

	args := []string{
		"upload", "from-google-photos",
		"--server", r.cfg.Immich.URL,
		"--api-key", r.cfg.Immich.APIKey.Reveal(),
		"--manage-burst", "Stack",
		"--sync-albums",
		"--people-tag=false",
		"--on-errors", "continue",
		"--no-ui",
	}
	args = append(args, archives...)

	if err := r.exec.Run(ctx, "immich-go", args, nil, nil); err != nil {
		r.failPhotos(ctx, user, "the import into Immich did not finish")
		r.log.Error("runPhotosImport: immich-go", "user", user, "error", err)
		return
	}

	if _, err := r.store.SetPhotosState(ctx, user, core.PhotosVerifying, "verifying the imported items"); err != nil {
		r.log.Error("runPhotosImport: set verifying", "user", user, "error", err)
	}
}

func (r *Runner) failPhotos(ctx context.Context, user, message string) {
	if err := r.store.SetError(ctx, user, message); err != nil {
		r.log.Error("failPhotos: set error", "user", user, "error", err)
	}
	if _, err := r.store.SetPhotosState(ctx, user, core.PhotosFailed, message); err != nil {
		r.log.Error("failPhotos: set state", "user", user, "error", err)
	}
}

func (r *Runner) openTokens(tok core.Token) (oauth.Tokens, error) {
	return oauth.OpenJSON(r.sealer, tok.Sealed)
}

// rcloneEnv builds the environment rclone needs, passing every secret through
// the environment rather than a config file so nothing is written to disk.
//
// rclone's convention is RCLONE_CONFIG_<REMOTE>_<KEY>; the remotes are named
// "gdrive" (Google Drive) and "nc" (Nextcloud) so the arguments read the same
// as the documentation. The Nextcloud password must be obscured the way rclone
// expects, because that is the only form its config parser accepts.
func (r *Runner) rcloneEnv(tokens oauth.Tokens, nc nextcloud.Credentials) ([]string, error) {
	obscured, err := obscurePassword(nc.AppPassword)
	if err != nil {
		return nil, err
	}

	env := append(os.Environ(),
		"RCLONE_CONFIG_GDRIVE_TYPE=drive",
		"RCLONE_CONFIG_GDRIVE_SCOPE=drive.readonly",
		"RCLONE_CONFIG_GDRIVE_TOKEN="+tokens.RcloneJSON(),
		"RCLONE_CONFIG_NC_TYPE=webdav",
		"RCLONE_CONFIG_NC_VENDOR=nextcloud",
		"RCLONE_CONFIG_NC_URL="+r.nc.DAVURL(nc.LoginName),
		"RCLONE_CONFIG_NC_USER="+nc.LoginName,
		"RCLONE_CONFIG_NC_PASS="+obscured,
	)
	if !r.cfg.Google.ClientID.Empty() {
		env = append(env,
			"RCLONE_CONFIG_GDRIVE_CLIENT_ID="+r.cfg.Google.ClientID.Reveal(),
			"RCLONE_CONFIG_GDRIVE_CLIENT_SECRET="+r.cfg.Google.ClientSecret.Reveal(),
		)
	}
	return env, nil
}

// acquire blocks until a heavy slot is free.
func (r *Runner) acquire() { r.limiter <- struct{}{} }
func (r *Runner) release() { <-r.limiter }

// rcloneStats matches the one-line stats rclone prints with
// --stats-one-line, e.g. "1.234 GiB / 5.678 GiB, 21%, ...".
//
// The byte figure may carry thousands separators ("1,234.5 MiB"), which are
// stripped before parsing; without that, a transfer past 999 MiB would stop
// reporting progress.
var rcloneStats = regexp.MustCompile(`([\d,.]+)\s*([KMGT]?i?B)\s*/\s*[\d,.]+\s*[KMGT]?i?B,\s*\d+%,\s*[\d.,]+\s*[kKMG]?i?B/s,\s*ETA\s+\S+\s*\(xfr#(\d+)`)

// parseRcloneStats extracts the transferred bytes and file count from a stats
// line. It returns false for any line that is not a stats line, so the caller
// can pass every line through it.
func parseRcloneStats(line string) (bytes int64, files int64, ok bool) {
	m := rcloneStats.FindStringSubmatch(line)
	if m == nil {
		return 0, 0, false
	}
	value, err := strconv.ParseFloat(strings.ReplaceAll(m[1], ",", ""), 64)
	if err != nil {
		return 0, 0, false
	}
	multiplier := unitMultiplier(m[2])
	count, err := strconv.ParseInt(m[3], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	return int64(value * float64(multiplier)), count, true
}

func unitMultiplier(unit string) int64 {
	switch strings.ToUpper(unit) {
	case "B":
		return 1
	case "KIB":
		return 1024
	case "MIB":
		return 1024 * 1024
	case "GIB":
		return 1024 * 1024 * 1024
	case "TIB":
		return 1024 * 1024 * 1024 * 1024
	case "KB":
		return 1000
	case "MB":
		return 1000 * 1000
	case "GB":
		return 1000 * 1000 * 1000
	default:
		return 1
	}
}

// OSExecutor is the production Executor.
type OSExecutor struct {
	Log *slog.Logger
}

// Run starts the command and streams stdout, calling onLine for each line. A
// non-zero exit is an error; the caller decides what to do with it.
func (e *OSExecutor) Run(ctx context.Context, name string, args []string, env []string, onLine func(string)) error {
	path, err := exec.LookPath(name)
	if err != nil {
		return fmt.Errorf("%s is not installed: %w", name, err)
	}
	cmd := exec.CommandContext(ctx, path, args...)
	if env != nil {
		cmd.Env = env
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	var wg sync.WaitGroup
	wg.Add(2)
	// Both streams are read: rclone writes stats to stderr, and a pipe nobody
	// reads fills up and blocks the process.
	go func() { defer wg.Done(); e.scan(stdout, onLine) }()
	go func() { defer wg.Done(); e.scan(stderr, onLine) }()

	wg.Wait()
	return cmd.Wait()
}

func (e *OSExecutor) scan(r io.Reader, onLine func(string)) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		e.Log.Debug("command output", "line", line)
		if onLine != nil {
			onLine(line)
		}
	}
}
