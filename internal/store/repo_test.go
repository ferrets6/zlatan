// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/marcodellemarche/zlatan/internal/core"
)

func newTestDB(t *testing.T) *DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "zlatan.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	log := slog.New(slog.NewTextHandler(discard{}, nil))
	if err := Migrate(context.Background(), db, log); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return db
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

func TestEnsureMigrationIsIdempotent(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	first, err := db.EnsureMigration(ctx, "marco", "marco@example.com")
	if err != nil {
		t.Fatalf("EnsureMigration: %v", err)
	}
	if first.DriveState != core.DriveNotStarted {
		t.Errorf("a new migration should start at not_started, got %q", first.DriveState)
	}

	second, err := db.EnsureMigration(ctx, "marco", "different@example.com")
	if err != nil {
		t.Fatalf("second EnsureMigration: %v", err)
	}
	if !second.CreatedAt.Equal(first.CreatedAt) {
		t.Error("a second EnsureMigration created a new row")
	}
}

func TestGetMigrationMissing(t *testing.T) {
	db := newTestDB(t)
	_, err := db.GetMigration(context.Background(), "nobody")
	if !errors.Is(err, ErrNoMigration) {
		t.Fatalf("want ErrNoMigration, got %v", err)
	}
}

func TestDriveAndPhotosStatesAreIndependent(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if _, err := db.EnsureMigration(ctx, "marco", ""); err != nil {
		t.Fatal(err)
	}

	if _, err := db.SetDriveState(ctx, "marco", core.DriveDone, "copied 4.0 GiB"); err != nil {
		t.Fatalf("SetDriveState: %v", err)
	}
	m, err := db.GetMigration(ctx, "marco")
	if err != nil {
		t.Fatal(err)
	}
	if m.DriveState != core.DriveDone {
		t.Errorf("drive state = %q", m.DriveState)
	}
	if m.PhotosState != core.PhotosNotStarted {
		t.Errorf("moving drive must not touch photos, got %q", m.PhotosState)
	}

	if _, err := db.SetPhotosState(ctx, "marco", core.PhotosAwaitingTakeout, "waiting for the Takeout"); err != nil {
		t.Fatalf("SetPhotosState: %v", err)
	}
	m, _ = db.GetMigration(ctx, "marco")
	if m.DriveState != core.DriveDone {
		t.Errorf("moving photos must not touch drive, got %q", m.DriveState)
	}
	if m.PhotosProgress != "waiting for the Takeout" {
		t.Errorf("photos progress = %q", m.PhotosProgress)
	}
}

// The Takeout wait stamp is set on entering the wait and cleared on any other
// state, and only people actually waiting are listed. The watcher leans on
// both: a stale stamp would expire a wait that just started, and a missing one
// would let it watch forever.
func TestTakeoutWaitStamp(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if _, err := db.EnsureMigration(ctx, "marco", ""); err != nil {
		t.Fatal(err)
	}

	// Not waiting: not listed, and no stamp.
	waits, err := db.ListAwaitingTakeout(ctx)
	if err != nil {
		t.Fatalf("ListAwaitingTakeout: %v", err)
	}
	if len(waits) != 0 {
		t.Fatalf("a fresh migration should not be waiting, got %v", waits)
	}

	// Entering the wait stamps it.
	if _, err := db.SetPhotosState(ctx, "marco", core.PhotosAwaitingTakeout, "waiting"); err != nil {
		t.Fatal(err)
	}
	waits, err = db.ListAwaitingTakeout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(waits) != 1 || waits[0].User != "marco" {
		t.Fatalf("expected marco waiting, got %v", waits)
	}
	if waits[0].Since.IsZero() {
		t.Error("the wait start should be stamped")
	}

	// Leaving the wait clears it.
	if _, err := db.SetPhotosState(ctx, "marco", core.PhotosImporting, "importing"); err != nil {
		t.Fatal(err)
	}
	waits, err = db.ListAwaitingTakeout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(waits) != 0 {
		t.Fatalf("a person no longer waiting must not be listed, got %v", waits)
	}
}

func TestDriveProgressAccumulates(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if _, err := db.EnsureMigration(ctx, "marco", ""); err != nil {
		t.Fatal(err)
	}

	if err := db.AddDriveProgress(ctx, "marco", 100, 1); err != nil {
		t.Fatal(err)
	}
	if err := db.AddDriveProgress(ctx, "marco", 200, 2); err != nil {
		t.Fatal(err)
	}
	m, _ := db.GetMigration(ctx, "marco")
	if m.DriveBytesCopied != 300 || m.DriveFilesCopied != 3 {
		t.Errorf("counters = %d bytes / %d files, want 300 / 3", m.DriveBytesCopied, m.DriveFilesCopied)
	}
}

func TestTokenRoundTrip(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	tok := core.Token{
		User:     "marco",
		Provider: "google",
		Sealed:   []byte{1, 2, 3, 4, 5},
		Scopes:   "drive.readonly",
	}
	if err := db.PutToken(ctx, tok); err != nil {
		t.Fatalf("PutToken: %v", err)
	}

	got, err := db.GetToken(ctx, "marco", "google")
	if err != nil {
		t.Fatalf("GetToken: %v", err)
	}
	if string(got.Sealed) != string(tok.Sealed) {
		t.Errorf("sealed value changed: %v", got.Sealed)
	}
	if got.Scopes != "drive.readonly" {
		t.Errorf("scopes = %q", got.Scopes)
	}

	// A second Put replaces rather than duplicating.
	tok.Sealed = []byte{9, 9, 9}
	if err := db.PutToken(ctx, tok); err != nil {
		t.Fatal(err)
	}
	got, _ = db.GetToken(ctx, "marco", "google")
	if string(got.Sealed) != string([]byte{9, 9, 9}) {
		t.Errorf("the second Put did not replace: %v", got.Sealed)
	}

	if err := db.DeleteToken(ctx, "marco", "google"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetToken(ctx, "marco", "google"); !errors.Is(err, ErrNoToken) {
		t.Fatalf("want ErrNoToken after delete, got %v", err)
	}
}

func TestPutTokenRefusesEmpty(t *testing.T) {
	db := newTestDB(t)
	err := db.PutToken(context.Background(), core.Token{User: "x", Provider: "google"})
	if err == nil {
		t.Fatal("an empty token must be refused")
	}
}

func TestPutVerificationRequiresUser(t *testing.T) {
	db := newTestDB(t)
	err := db.PutVerification(context.Background(), core.Verify{Track: core.TrackDrive})
	if err == nil {
		t.Fatal("a verification with no user must be refused")
	}
}

func TestLatestVerification(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := db.LatestVerification(ctx, "marco", core.TrackDrive); !errors.Is(err, ErrNoVerification) {
		t.Fatalf("want ErrNoVerification, got %v", err)
	}

	if err := db.PutVerification(ctx, core.Verify{
		User: "marco", Track: core.TrackDrive, Checked: 10, Matched: 10, Detail: "sample ok",
	}); err != nil {
		t.Fatal(err)
	}
	v, err := db.LatestVerification(ctx, "marco", core.TrackDrive)
	if err != nil {
		t.Fatalf("LatestVerification: %v", err)
	}
	if v.Checked != 10 || v.Mismatch != 0 || !v.OK() {
		t.Errorf("verification = %+v", v)
	}

	// A verification for another person must not leak across.
	if _, err := db.LatestVerification(ctx, "federico", core.TrackDrive); !errors.Is(err, ErrNoVerification) {
		t.Fatalf("another user's verification leaked: %v", err)
	}

	// The files behind a mismatch come back as they were stored.
	problems := []core.Problem{
		{File: "takeout-1:Takeout/Foto da 2023/a(1).MP4", Pending: true, Reason: "missing metadata"},
		{File: "takeout-1:Takeout/b.jpg", Reason: "upload error error=500"},
	}
	if err := db.PutVerification(ctx, core.Verify{
		User: "marco", Track: core.TrackPhotos, Checked: 12, Matched: 10, Mismatch: 2, Problems: problems,
	}); err != nil {
		t.Fatal(err)
	}
	v, err = db.LatestVerification(ctx, "marco", core.TrackPhotos)
	if err != nil {
		t.Fatalf("LatestVerification: %v", err)
	}
	if !slices.Equal(v.Problems, problems) {
		t.Errorf("problems = %+v, want %+v", v.Problems, problems)
	}
}

func TestSchemaCheckRefusesNewerDatabase(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	// Pretend a future binary wrote a higher version.
	if _, err := db.W.ExecContext(ctx, "INSERT INTO goose_db_version (version_id, is_applied) VALUES (9999, 1)"); err != nil {
		t.Fatalf("seed a future version: %v", err)
	}
	err := CheckSchema(ctx, db)
	if !errors.Is(err, ErrSchemaNewer) {
		t.Fatalf("want ErrSchemaNewer, got %v", err)
	}
}

// Staging holds only the Photos archives, so its retention window follows the
// Photos half alone: the Drive half ending changes nothing, Photos ending (even
// failed, even with Drive never started) starts it, and Photos running again
// clears it so a retry that fails later is not purged on an old window.
func TestPhotosFinishedAtStartsTheStagingWindow(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := db.EnsureMigration(ctx, "marco", "marco@example.com"); err != nil {
		t.Fatalf("EnsureMigration: %v", err)
	}
	list := func() []FinishedMigration {
		t.Helper()
		finished, err := db.ListFinished(ctx)
		if err != nil {
			t.Fatalf("ListFinished: %v", err)
		}
		return finished
	}

	if _, err := db.SetDriveState(ctx, "marco", core.DriveDone, "done"); err != nil {
		t.Fatalf("SetDriveState: %v", err)
	}
	if got := list(); len(got) != 0 {
		t.Fatalf("Drive ending holds no staging and should not count, got %v", got)
	}

	// Photos fails with Drive back at selecting: a Photos-only person.
	if _, err := db.SetDriveState(ctx, "marco", core.DriveSelecting, ""); err != nil {
		t.Fatalf("SetDriveState: %v", err)
	}
	if _, err := db.SetPhotosState(ctx, "marco", core.PhotosFailed, "failed"); err != nil {
		t.Fatalf("SetPhotosState: %v", err)
	}
	got := list()
	if len(got) != 1 || got[0].FinishedAt.IsZero() {
		t.Fatalf("a failed Photos half should start the window, got %v", got)
	}
	stamp := got[0].FinishedAt

	// A later terminal write must not move the stamp: the window runs from
	// when the work ended, not from the last touch.
	if _, err := db.SetPhotosState(ctx, "marco", core.PhotosFailed, "still failed"); err != nil {
		t.Fatalf("SetPhotosState: %v", err)
	}
	if again := list(); !again[0].FinishedAt.Equal(stamp) {
		t.Error("a later terminal write reset the stamp")
	}

	// Backdate the stamp, so "a new window" below is told apart from the old
	// one by more than the clock's resolution.
	old := time.Now().Add(-30 * 24 * time.Hour).UTC()
	if _, err := db.W.ExecContext(ctx, `UPDATE migrations SET photos_finished_at = ? WHERE user = ?`,
		old.Format(timeFormat), "marco"); err != nil {
		t.Fatal(err)
	}

	// A retry clears it, and the next ending stamps afresh.
	if _, err := db.SetPhotosState(ctx, "marco", core.PhotosImporting, ""); err != nil {
		t.Fatalf("SetPhotosState: %v", err)
	}
	if got := list(); len(got) != 0 {
		t.Fatalf("a running Photos half must keep its staging, got %v", got)
	}
	if _, err := db.SetPhotosState(ctx, "marco", core.PhotosDone, "done"); err != nil {
		t.Fatalf("SetPhotosState: %v", err)
	}
	if got := list(); len(got) != 1 || !got[0].FinishedAt.After(old) {
		t.Errorf("the retry's ending should start a new window after %v, got %v", old, got)
	}
}

func TestSetQuotaEstimate(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if _, err := db.EnsureMigration(ctx, "marco", "marco@example.com"); err != nil {
		t.Fatalf("EnsureMigration: %v", err)
	}

	if err := db.SetQuotaEstimate(ctx, "marco", 60, 50, 100); err != nil {
		t.Fatalf("SetQuotaEstimate: %v", err)
	}
	if err := db.SetGoogleUsage(ctx, "marco", 30, 200); err != nil {
		t.Fatalf("SetGoogleUsage: %v", err)
	}
	m, err := db.GetMigration(ctx, "marco")
	if err != nil {
		t.Fatalf("GetMigration: %v", err)
	}
	if m.DriveSourceBytes != 60 || m.QuotaUsedBytes != 50 || m.QuotaTotalBytes != 100 {
		t.Errorf("quota estimate = %d/%d/%d, want 60/50/100",
			m.DriveSourceBytes, m.QuotaUsedBytes, m.QuotaTotalBytes)
	}
	if m.GoogleOtherBytes != 30 || m.GoogleTotalBytes != 200 {
		t.Errorf("Google usage = %d/%d, want 30/200",
			m.GoogleOtherBytes, m.GoogleTotalBytes)
	}
}

func TestListInterruptedFindsInFlightWork(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	// A copy in flight, and a Photos import in flight: both are states only
	// the running process writes, so after a restart they are orphaned.
	if _, err := db.EnsureMigration(ctx, "marco", "marco@example.com"); err != nil {
		t.Fatalf("EnsureMigration: %v", err)
	}
	if _, err := db.SetDriveState(ctx, "marco", core.DriveCopying, "copying"); err != nil {
		t.Fatalf("SetDriveState: %v", err)
	}
	if _, err := db.SetPhotosState(ctx, "marco", core.PhotosImporting, "importing"); err != nil {
		t.Fatalf("SetPhotosState: %v", err)
	}

	// A second person waiting on Google: that is resumed by the watcher, not
	// by the recovery, so it must not appear.
	if _, err := db.EnsureMigration(ctx, "federico", "federico@example.com"); err != nil {
		t.Fatalf("EnsureMigration: %v", err)
	}
	if _, err := db.SetPhotosState(ctx, "federico", core.PhotosAwaitingTakeout, "waiting"); err != nil {
		t.Fatalf("SetPhotosState: %v", err)
	}

	got, err := db.ListInterrupted(ctx)
	if err != nil {
		t.Fatalf("ListInterrupted: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListInterrupted returned %d rows, want 1: %+v", len(got), got)
	}
	if got[0].User != "marco" || !got[0].Drive || !got[0].Photos {
		t.Errorf("got %+v, want marco with both tracks in flight", got[0])
	}
}

func TestListInterruptedIgnoresRestingStates(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if _, err := db.EnsureMigration(ctx, "marco", "marco@example.com"); err != nil {
		t.Fatalf("EnsureMigration: %v", err)
	}
	// not_started, selecting, consent_pending and awaiting_takeout are all
	// reachable without a running job, so a restart must not touch them.
	for _, s := range []core.DriveState{core.DriveNotStarted, core.DriveSelecting, core.DriveConsentPending} {
		if _, err := db.SetDriveState(ctx, "marco", s, ""); err != nil {
			t.Fatalf("SetDriveState(%s): %v", s, err)
		}
		got, err := db.ListInterrupted(ctx)
		if err != nil {
			t.Fatalf("ListInterrupted: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("drive state %q should not be seen as interrupted", s)
		}
	}
}

// Upgrading keeps the stamp of a Photos half that has ended, and clears the one
// of a Photos half running again: the old stamp was never cleared on a retry,
// and kept, it would purge that person's archives the moment they end.
func TestPhotosFinishedAtMigrationClearsRunningRows(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "zlatan.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	p, err := newProvider(db.W)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, 8); err != nil {
		t.Fatalf("migrate to 8: %v", err)
	}
	old := "2026-07-01T00:00:00Z"
	for user, photos := range map[string]string{"ended": "failed", "running": "importing"} {
		if _, err := db.W.ExecContext(ctx, `
			INSERT INTO migrations (user, email, created_at, updated_at, drive_state, photos_state, finished_at)
			VALUES (?, '', ?, ?, 'done', ?, ?)`, user, old, old, photos, old); err != nil {
			t.Fatal(err)
		}
	}

	if err := Migrate(ctx, db, slog.New(slog.NewTextHandler(discard{}, nil))); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	got := map[string]string{}
	rows, err := db.R.QueryContext(ctx, `SELECT user, photos_finished_at FROM migrations`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var u, stamp string
		if err := rows.Scan(&u, &stamp); err != nil {
			t.Fatal(err)
		}
		got[u] = stamp
	}
	if got["ended"] != old || got["running"] != "" {
		t.Errorf("stamps after upgrade = %v, want ended kept and running cleared", got)
	}
}
