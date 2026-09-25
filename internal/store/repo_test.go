// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/marcodellemarche/migrate/internal/core"
)

func newTestDB(t *testing.T) *DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "migrate.db")
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

	if _, err := db.SetPhotosState(ctx, "marco", core.PhotosAwaitingShare, "waiting for the Takeout"); err != nil {
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
