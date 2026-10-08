// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/marcodellemarche/zlatan/internal/core"
)

// timeFormat is how timestamps are stored: RFC3339 in UTC, so they sort
// lexicographically and never depend on the server's timezone.
const timeFormat = time.RFC3339Nano

func now() string { return time.Now().UTC().Format(timeFormat) }

func parseTime(s string) time.Time {
	t, err := time.Parse(timeFormat, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// ErrNoMigration means the person has never opened the wizard.
var ErrNoMigration = errors.New("no migration for this user")

// GetMigration returns one person's progress. A person with no row is not an
// error at the call site: the wizard shows a fresh start.
func (db *DB) GetMigration(ctx context.Context, user string) (core.Migration, error) {
	const q = `
		SELECT user, email, drive_state, photos_state,
		       drive_progress, photos_progress,
		       drive_bytes_copied, drive_files_copied, photos_assets_added,
		       last_error, created_at, updated_at,
		       drive_source_bytes, quota_used_bytes, quota_total_bytes,
		       google_other_bytes, google_total_bytes, photos_parts_expected,
		       auto_import, photos_finished_at
		FROM migrations WHERE user = ?`

	var m core.Migration
	var createdAt, updatedAt, photosFinishedAt string
	var autoImport int
	err := db.R.QueryRowContext(ctx, q, user).Scan(
		&m.User, &m.Email, &m.DriveState, &m.PhotosState,
		&m.DriveProgress, &m.PhotosProgress,
		&m.DriveBytesCopied, &m.DriveFilesCopied, &m.PhotosAssetsAdded,
		&m.LastError, &createdAt, &updatedAt,
		&m.DriveSourceBytes, &m.QuotaUsedBytes, &m.QuotaTotalBytes,
		&m.GoogleOtherBytes, &m.GoogleTotalBytes, &m.PhotosPartsExpected,
		&autoImport, &photosFinishedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return core.Migration{}, ErrNoMigration
	}
	if err != nil {
		return core.Migration{}, fmt.Errorf("read migration for %s: %w", user, err)
	}
	m.CreatedAt = parseTime(createdAt)
	m.UpdatedAt = parseTime(updatedAt)
	m.AutoImport = autoImport != 0
	m.PhotosFinishedAt = parseTime(photosFinishedAt)
	return m, nil
}

// EnsureMigration creates the row if it is missing and returns it, so the
// wizard has something to render on first visit.
func (db *DB) EnsureMigration(ctx context.Context, user, email string) (core.Migration, error) {
	m, err := db.GetMigration(ctx, user)
	if err == nil {
		return m, nil
	}
	if !errors.Is(err, ErrNoMigration) {
		return core.Migration{}, err
	}

	ts := now()
	if err := db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO migrations (user, email, created_at, updated_at)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(user) DO NOTHING`,
			user, email, ts, ts)
		return err
	}); err != nil {
		return core.Migration{}, fmt.Errorf("create migration for %s: %w", user, err)
	}
	return db.GetMigration(ctx, user)
}

// SetDriveState moves the Drive half and records the new progress line. It
// returns the updated row so the caller never has to re-read to render.
func (db *DB) SetDriveState(ctx context.Context, user string, state core.DriveState, progress string) (core.Migration, error) {
	if err := db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			UPDATE migrations
			SET drive_state = ?, drive_progress = ?, updated_at = ?
			WHERE user = ?`,
			string(state), progress, now(), user)
		return err
	}); err != nil {
		return core.Migration{}, fmt.Errorf("set drive state for %s: %w", user, err)
	}
	return db.GetMigration(ctx, user)
}

// SetPhotosState moves the Photos half. Entering the Takeout wait stamps the
// wait start, so the watcher can give up after a while; leaving it clears the
// stamp.
func (db *DB) SetPhotosState(ctx context.Context, user string, state core.PhotosState, progress string) (core.Migration, error) {
	// The stamp is set on entering the wait and cleared on any other state, so
	// it always means "waiting since", never a stale value from a past wait.
	waitSince := ""
	if state == core.PhotosAwaitingTakeout {
		waitSince = now()
	}
	if err := db.Tx(ctx, func(tx *sql.Tx) error {
		// photos_finished_at starts the staging retention: stamped when Photos
		// reaches done, failed or cancelled, kept by a later terminal write (the
		// window runs from when the work ended), cleared when Photos runs again,
		// so a retry that fails later gets a fresh window rather than an old
		// one that purges its archives at once.
		_, err := tx.ExecContext(ctx, `
			UPDATE migrations
			SET photos_state = ?, photos_progress = ?, updated_at = ?, photos_wait_since = ?,
			    photos_finished_at = CASE
			        WHEN ? NOT IN (?, ?, ?) THEN ''
			        WHEN photos_finished_at = '' THEN ?
			        ELSE photos_finished_at END
			WHERE user = ?`,
			string(state), progress, now(), waitSince,
			string(state), string(core.PhotosDone), string(core.PhotosFailed), string(core.PhotosCancelled),
			now(), user)
		return err
	}); err != nil {
		return core.Migration{}, fmt.Errorf("set photos state for %s: %w", user, err)
	}
	return db.GetMigration(ctx, user)
}

// SetQuotaEstimate records what the pre-copy scan learned: the source Drive
// size and the person's current Nextcloud usage. It is written before the copy
// starts and read back by the wizard to warn without blocking.
func (db *DB) SetQuotaEstimate(ctx context.Context, user string, driveSource, used, total int64) error {
	return db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			UPDATE migrations
			SET drive_source_bytes = ?, quota_used_bytes = ?, quota_total_bytes = ?, updated_at = ?
			WHERE user = ?`,
			driveSource, used, total, now(), user)
		return err
	})
}

// SetGoogleUsage records how much space the person's data takes up at Google,
// read from the OAuth quota. It is separate from SetQuotaEstimate because it is
// written by both halves: the Drive copy's pre-copy scan, and the Photos
// watcher, which reads the same figure for someone moving only Photos.
func (db *DB) SetGoogleUsage(ctx context.Context, user string, other, total int64) error {
	return db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			UPDATE migrations
			SET google_other_bytes = ?, google_total_bytes = ?, updated_at = ?
			WHERE user = ?`,
			other, total, now(), user)
		return err
	})
}

// SetPhotosParts records how many archives the person said their Takeout was
// split into.
func (db *DB) SetPhotosParts(ctx context.Context, user string, parts int) error {
	return db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			UPDATE migrations SET photos_parts_expected = ?, updated_at = ? WHERE user = ?`,
			parts, now(), user)
		return err
	})
}

// SetAutoImport records whether the import should start by itself once every
// declared part is on disk.
func (db *DB) SetAutoImport(ctx context.Context, user string, on bool) error {
	v := 0
	if on {
		v = 1
	}
	return db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			UPDATE migrations SET auto_import = ?, updated_at = ? WHERE user = ?`,
			v, now(), user)
		return err
	})
}

// Touch bumps updated_at without changing anything else. The credential sweep
// uses it after forgetting an abandoned migration's tokens, so the row falls
// out of ListIdle's window and is not re-selected (and re-forgotten) on every
// later sweep.
func (db *DB) Touch(ctx context.Context, user string) error {
	return db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE migrations SET updated_at = ? WHERE user = ?`, now(), user)
		return err
	})
}

// ListAutoImportWaiting returns the users whose Photos half is waiting for an
// upload AND who asked for the import to start by itself. The server-side sweep
// works from this, so a kiosk download started with the tab then closed still
// gets imported once every part has landed.
func (db *DB) ListAutoImportWaiting(ctx context.Context) ([]string, error) {
	rows, err := db.R.QueryContext(ctx,
		`SELECT user FROM migrations WHERE photos_state = ? AND auto_import = 1`,
		string(core.PhotosAwaitingUpload))
	if err != nil {
		return nil, fmt.Errorf("list auto-import waiting: %w", err)
	}
	defer rows.Close()

	var users []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, fmt.Errorf("scan auto-import waiting: %w", err)
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// InterruptedMigration is one person whose migration was in flight when the
// process stopped, and which track was running. It is the unit the recovery
// works in.
type InterruptedMigration struct {
	User string
	// Drive and Photos say which track was mid-work, so the recovery moves only
	// that one: a finished track is left exactly as it was.
	Drive  bool
	Photos bool
}

// ListInterrupted returns the rows whose drive or photos half is in a state
// that only the running process can leave. Those states are written by the
// runner and by nothing else, so after a restart they mean one thing: the work
// that set them is gone, and the row will never move again on its own.
//
// The states are the ones a long job sets while it runs: copying, verifying,
// downloading, importing. Consent and selection are excluded on purpose: the
// person leaves those by acting in a browser, and a restart does not orphan
// them.
func (db *DB) ListInterrupted(ctx context.Context) ([]InterruptedMigration, error) {
	const q = `
		SELECT user, drive_state, photos_state FROM migrations
		WHERE drive_state IN (?, ?) OR photos_state IN (?, ?)`
	rows, err := db.R.QueryContext(ctx, q,
		string(core.DriveCopying), string(core.DriveVerifying),
		string(core.PhotosDownloading), string(core.PhotosImporting))
	if err != nil {
		return nil, fmt.Errorf("list interrupted migrations: %w", err)
	}
	defer rows.Close()

	var out []InterruptedMigration
	for rows.Next() {
		var m InterruptedMigration
		var drive, photos string
		if err := rows.Scan(&m.User, &drive, &photos); err != nil {
			return nil, fmt.Errorf("scan interrupted migration: %w", err)
		}
		m.Drive = drive == string(core.DriveCopying) || drive == string(core.DriveVerifying)
		m.Photos = photos == string(core.PhotosDownloading) || photos == string(core.PhotosImporting)
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListIdle returns the users whose migration has not moved since before, and
// on which the runner is not working: every track is either finished, failed,
// not started, or waiting for the person. The credential sweeper forgets what
// these people gave, because nobody is coming back to use it.
//
// Waiting for Google's Takeout is work in flight, not idleness: the watcher
// moves that state itself, and gives up on its own after a week.
//
// Mid-setup states (consent_pending, selecting, not_started) are deliberately
// NOT excluded: cleaning up an abandoned setup is exactly this sweep's job —
// a finished or failed track has its credentials released on completion, so the
// only ones left for the idle sweep are people who connected and never started.
// They are safe because their updated_at is accurate: unlike awaiting_upload /
// awaiting_takeout, where files land over days without touching the row, a
// mid-setup row only sits still when the person has actually walked away, and
// the caller's threshold has a multi-day floor.
func (db *DB) ListIdle(ctx context.Context, before time.Time) ([]string, error) {
	// The state filter is in SQL; the "older than" is applied in Go. timeFormat
	// is RFC3339Nano, which drops trailing zeros from the fraction, so a
	// string comparison in SQL is not monotonic — fragile for a decision that
	// revokes credentials. parseTime + time.Before is exact.
	rows, err := db.R.QueryContext(ctx, `
		SELECT user, updated_at FROM migrations
		WHERE drive_state NOT IN (?, ?, ?)
		  AND photos_state NOT IN (?, ?, ?, ?, ?)`,
		string(core.DriveCopying), string(core.DriveImporting), string(core.DriveVerifying),
		// awaiting_upload is work in flight, not idleness: the files land over
		// days (site upload or kiosk download) without touching this row, and
		// forgetting the Immich key or revoking Google under an upload in
		// progress would strand it. Treated like awaiting_takeout.
		string(core.PhotosAwaitingUpload), string(core.PhotosAwaitingTakeout),
		string(core.PhotosDownloading), string(core.PhotosImporting), string(core.PhotosVerifying))
	if err != nil {
		return nil, fmt.Errorf("list idle migrations: %w", err)
	}
	defer rows.Close()

	var users []string
	for rows.Next() {
		var u, updatedAt string
		if err := rows.Scan(&u, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan idle migration: %w", err)
		}
		if parseTime(updatedAt).Before(before) {
			users = append(users, u)
		}
	}
	return users, rows.Err()
}

// ListAwaitingTakeout returns the users whose Photos half is waiting for a
// Takeout to appear in their Drive. The watcher reads this every tick rather
// than holding a goroutine per person, so a restart resumes the wait instead
// of losing it.
func (db *DB) ListAwaitingTakeout(ctx context.Context) ([]core.TakeoutWait, error) {
	rows, err := db.R.QueryContext(ctx,
		`SELECT user, photos_wait_since FROM migrations WHERE photos_state = ?`,
		string(core.PhotosAwaitingTakeout))
	if err != nil {
		return nil, fmt.Errorf("list awaiting takeout: %w", err)
	}
	defer rows.Close()

	var waits []core.TakeoutWait
	for rows.Next() {
		var w core.TakeoutWait
		var since string
		if err := rows.Scan(&w.User, &since); err != nil {
			return nil, fmt.Errorf("scan awaiting takeout: %w", err)
		}
		w.Since = parseTime(since)
		waits = append(waits, w)
	}
	return waits, rows.Err()
}

// FinishedMigration is one person whose Photos half has ended, and when.
// FinishedAt is zero for a row the stamp has not reached (one that ended
// before the column existed, or whose Photos ended while only both tracks
// ending stamped it); the sweeper treats that as "stamp it now", so an upgrade
// does not purge immediately nor hold the files forever.
type FinishedMigration struct {
	User       string
	FinishedAt time.Time
}

// ListFinished returns the users whose Photos half has reached a terminal
// state (done, failed or cancelled). The staging sweeper works from this.
// Staging holds only Photos archives, so the Drive half does not matter: a
// Photos-only person's Drive never ends, and waiting for it kept their files
// forever. A failed track is in too: its archives stay for a retry through
// the retention window, then go rather than staying for good.
func (db *DB) ListFinished(ctx context.Context) ([]FinishedMigration, error) {
	const q = `
		SELECT user, photos_finished_at FROM migrations
		WHERE photos_state IN (?, ?, ?)`
	rows, err := db.R.QueryContext(ctx, q,
		string(core.PhotosDone), string(core.PhotosFailed), string(core.PhotosCancelled))
	if err != nil {
		return nil, fmt.Errorf("list finished migrations: %w", err)
	}
	defer rows.Close()

	var out []FinishedMigration
	for rows.Next() {
		var m FinishedMigration
		var stamp string
		if err := rows.Scan(&m.User, &stamp); err != nil {
			return nil, fmt.Errorf("scan finished migration: %w", err)
		}
		m.FinishedAt = parseTime(stamp)
		out = append(out, m)
	}
	return out, rows.Err()
}

// StampFinished records that a Photos half has ended, if it has not been
// stamped already. CASE keeps the first stamp: the retention window is
// measured from when the work ended, and a later write must not reset it.
func (db *DB) StampFinished(ctx context.Context, user string) error {
	return db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			UPDATE migrations
			SET photos_finished_at = CASE WHEN photos_finished_at = '' THEN ? ELSE photos_finished_at END
			WHERE user = ?`, now(), user)
		return err
	})
}

// SetError records why a track stopped, without moving its state: the caller
// decides whether that is failed or still recoverable.
func (db *DB) SetError(ctx context.Context, user, message string) error {
	return db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			UPDATE migrations SET last_error = ?, updated_at = ? WHERE user = ?`,
			message, now(), user)
		return err
	})
}

// AddDriveProgress accumulates the Drive counters, so a resumed run continues
// the totals rather than restarting them.
func (db *DB) AddDriveProgress(ctx context.Context, user string, bytes, files int64) error {
	return db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			UPDATE migrations
			SET drive_bytes_copied = drive_bytes_copied + ?,
			    drive_files_copied = drive_files_copied + ?,
			    updated_at = ?
			WHERE user = ?`,
			bytes, files, now(), user)
		return err
	})
}

// SetPhotosAssets records how many assets the import added.
func (db *DB) SetPhotosAssets(ctx context.Context, user string, assets int64) error {
	return db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			UPDATE migrations SET photos_assets_added = ?, updated_at = ? WHERE user = ?`,
			assets, now(), user)
		return err
	})
}

// PutToken stores a sealed OAuth token, replacing any previous one for the
// same person and provider. The plaintext is sealed by the caller, so this
// layer never sees it.
func (db *DB) PutToken(ctx context.Context, t core.Token) error {
	if len(t.Sealed) == 0 {
		return errors.New("refusing to store an empty token")
	}
	ts := now()
	return db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO tokens (user, provider, sealed, scopes, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(user, provider) DO UPDATE SET
				sealed = excluded.sealed,
				scopes = excluded.scopes,
				updated_at = excluded.updated_at`,
			t.User, t.Provider, t.Sealed, t.Scopes, ts, ts); err != nil {
			return err
		}
		// Connecting a credential is activity: bump the migration so the idle
		// credential sweep does not revoke what the person is still setting up
		// (their state may sit at not_started while they wait for a Takeout).
		// A no-op when the row does not exist yet.
		_, err := tx.ExecContext(ctx,
			`UPDATE migrations SET updated_at = ? WHERE user = ?`, ts, t.User)
		return err
	})
}

// GetToken returns the sealed token, or ErrNoToken. The caller opens it.
func (db *DB) GetToken(ctx context.Context, user, provider string) (core.Token, error) {
	const q = `SELECT user, provider, sealed, scopes, created_at, updated_at
	           FROM tokens WHERE user = ? AND provider = ?`
	var t core.Token
	var createdAt, updatedAt string
	err := db.R.QueryRowContext(ctx, q, user, provider).Scan(
		&t.User, &t.Provider, &t.Sealed, &t.Scopes, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return core.Token{}, ErrNoToken
	}
	if err != nil {
		return core.Token{}, fmt.Errorf("read token for %s: %w", user, err)
	}
	t.CreatedAt = parseTime(createdAt)
	t.UpdatedAt = parseTime(updatedAt)
	return t, nil
}

// DeleteToken removes a token, used when a migration closes and the access is
// no longer needed.
func (db *DB) DeleteToken(ctx context.Context, user, provider string) error {
	return db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM tokens WHERE user = ? AND provider = ?`, user, provider)
		return err
	})
}

// ErrNoToken means the person has not authorised this provider.
var ErrNoToken = errors.New("no token for this user and provider")

// PutVerification records a check result for one person and track.
func (db *DB) PutVerification(ctx context.Context, v core.Verify) error {
	if v.User == "" {
		return errors.New("a verification must name the user it belongs to")
	}
	problems := ""
	if len(v.Problems) > 0 {
		b, err := json.Marshal(v.Problems)
		if err != nil {
			return fmt.Errorf("encode problems: %w", err)
		}
		problems = string(b)
	}
	return db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO verifications (user, track, checked, matched, mismatch, detail, problems, checked_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			v.User, string(v.Track), v.Checked, v.Matched, v.Mismatch, v.Detail, problems, now())
		return err
	})
}

// LatestVerification returns the most recent check for a person and track, so
// the closing page can show what was compared.
func (db *DB) LatestVerification(ctx context.Context, user string, track core.Track) (core.Verify, error) {
	const q = `SELECT user, track, checked, matched, mismatch, detail, problems, checked_at
	           FROM verifications WHERE user = ? AND track = ? ORDER BY checked_at DESC LIMIT 1`
	var v core.Verify
	var storedUser, storedTrack, problems, checkedAt string
	err := db.R.QueryRowContext(ctx, q, user, string(track)).Scan(
		&storedUser, &storedTrack, &v.Checked, &v.Matched, &v.Mismatch, &v.Detail, &problems, &checkedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return core.Verify{}, ErrNoVerification
	}
	if err != nil {
		return core.Verify{}, fmt.Errorf("read verification for %s: %w", track, err)
	}
	v.User = storedUser
	v.Track = core.Track(storedTrack)
	v.CheckedAt = parseTime(checkedAt)
	// The list is a detail on top of the counts: one that will not decode is
	// dropped rather than hiding the verification it belongs to.
	if problems != "" {
		_ = json.Unmarshal([]byte(problems), &v.Problems)
	}
	return v, nil
}

// ErrNoVerification means nothing has been checked yet.
var ErrNoVerification = errors.New("no verification recorded")
