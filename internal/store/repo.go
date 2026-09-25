// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
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
		       last_error, created_at, updated_at
		FROM migrations WHERE user = ?`

	var m core.Migration
	var createdAt, updatedAt string
	err := db.R.QueryRowContext(ctx, q, user).Scan(
		&m.User, &m.Email, &m.DriveState, &m.PhotosState,
		&m.DriveProgress, &m.PhotosProgress,
		&m.DriveBytesCopied, &m.DriveFilesCopied, &m.PhotosAssetsAdded,
		&m.LastError, &createdAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return core.Migration{}, ErrNoMigration
	}
	if err != nil {
		return core.Migration{}, fmt.Errorf("read migration for %s: %w", user, err)
	}
	m.CreatedAt = parseTime(createdAt)
	m.UpdatedAt = parseTime(updatedAt)
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
		_, err := tx.ExecContext(ctx, `
			UPDATE migrations
			SET photos_state = ?, photos_progress = ?, updated_at = ?, photos_wait_since = ?
			WHERE user = ?`,
			string(state), progress, now(), waitSince, user)
		return err
	}); err != nil {
		return core.Migration{}, fmt.Errorf("set photos state for %s: %w", user, err)
	}
	return db.GetMigration(ctx, user)
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
		_, err := tx.ExecContext(ctx, `
			INSERT INTO tokens (user, provider, sealed, scopes, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(user, provider) DO UPDATE SET
				sealed = excluded.sealed,
				scopes = excluded.scopes,
				updated_at = excluded.updated_at`,
			t.User, t.Provider, t.Sealed, t.Scopes, ts, ts)
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
	return db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO verifications (user, track, checked, matched, mismatch, detail, checked_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			v.User, string(v.Track), v.Checked, v.Matched, v.Mismatch, v.Detail, now())
		return err
	})
}

// LatestVerification returns the most recent check for a person and track, so
// the closing page can show what was compared.
func (db *DB) LatestVerification(ctx context.Context, user string, track core.Track) (core.Verify, error) {
	const q = `SELECT user, track, checked, matched, mismatch, detail, checked_at
	           FROM verifications WHERE user = ? AND track = ? ORDER BY checked_at DESC LIMIT 1`
	var v core.Verify
	var storedUser, storedTrack, checkedAt string
	err := db.R.QueryRowContext(ctx, q, user, string(track)).Scan(
		&storedUser, &storedTrack, &v.Checked, &v.Matched, &v.Mismatch, &v.Detail, &checkedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return core.Verify{}, ErrNoVerification
	}
	if err != nil {
		return core.Verify{}, fmt.Errorf("read verification for %s: %w", track, err)
	}
	v.User = storedUser
	v.Track = core.Track(storedTrack)
	v.CheckedAt = parseTime(checkedAt)
	return v, nil
}

// ErrNoVerification means nothing has been checked yet.
var ErrNoVerification = errors.New("no verification recorded")
