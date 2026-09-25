// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcodellemarche/zlatan/internal/core"
	"github.com/marcodellemarche/zlatan/internal/oauth"
	"github.com/marcodellemarche/zlatan/internal/store"
)

// This is the one test that runs the watcher against the real store, not the
// in-memory fake: the point of the watcher is that the wait survives a restart
// because it lives in the database, and a fake cannot show that.
func TestWatcherCollectsATakeoutFromTheStore(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "zlatan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	if err := store.Migrate(ctx, db, log); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	cfg := testConfig()
	cfg.Google.TakeoutFolder = "Takeout"
	cfg.TakeoutMaxWait = time.Hour
	sealer, _ := core.NewSealer(core.Secret("token-key"))
	r := New(cfg, db, sealer, log).WithExecutor(&fakeExecutor{
		lines: []string{`[{"Name":"takeout-1.zip","Size":100}]`},
	})

	// A person connected to Google and waiting for the export.
	sealed, err := oauth.SealJSON(sealer, oauth.Tokens{AccessToken: "a", RefreshToken: "r", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.EnsureMigration(ctx, "marco", ""); err != nil {
		t.Fatal(err)
	}
	if err := db.PutToken(ctx, core.Token{User: "marco", Provider: "google", Sealed: sealed}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SetPhotosState(ctx, "marco", core.PhotosAwaitingTakeout, "waiting"); err != nil {
		t.Fatal(err)
	}

	r.pollTakeout(ctx)

	// The download goroutine is detached; wait for the state to leave the wait.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		m, err := db.GetMigration(ctx, "marco")
		if err != nil {
			t.Fatal(err)
		}
		if m.PhotosState != core.PhotosAwaitingTakeout {
			// It moved on (downloading, importing, or done). That is the point.
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the watcher never moved the person off the wait")
}

// A second tick must not start a second download of the same folder. The
// watcher claims the person (awaiting_takeout -> downloading) before it
// detaches the download, so once claimed there is nothing left to find.
func TestWatcherDoesNotStartTwoDownloads(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "zlatan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	if err := store.Migrate(ctx, db, log); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	cfg := testConfig()
	cfg.Google.TakeoutFolder = "Takeout"
	cfg.TakeoutMaxWait = time.Hour
	sealer, _ := core.NewSealer(core.Secret("token-key"))
	exec := &fakeExecutor{lines: []string{`[{"Name":"takeout-1.zip","Size":100}]`}}
	r := New(cfg, db, sealer, log).WithExecutor(exec)

	sealed, _ := oauth.SealJSON(sealer, oauth.Tokens{AccessToken: "a", RefreshToken: "r", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour)})
	if _, err := db.EnsureMigration(ctx, "marco", ""); err != nil {
		t.Fatal(err)
	}
	if err := db.PutToken(ctx, core.Token{User: "marco", Provider: "google", Sealed: sealed}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SetPhotosState(ctx, "marco", core.PhotosAwaitingTakeout, "waiting"); err != nil {
		t.Fatal(err)
	}

	r.pollTakeout(ctx)
	// The state is claimed synchronously, so a second tick sees nothing to do.
	r.pollTakeout(ctx)

	exec.mu.Lock()
	defer exec.mu.Unlock()
	var copies int
	for _, c := range exec.commands {
		if len(c.args) > 0 && c.args[0] == "copy" {
			copies++
		}
	}
	if copies > 1 {
		t.Fatalf("the folder was downloaded %d times; the claim must prevent a second start", copies)
	}
}
