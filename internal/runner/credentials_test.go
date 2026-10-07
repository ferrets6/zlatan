// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"context"
	"testing"

	"github.com/marcodellemarche/zlatan/internal/config"
	"github.com/marcodellemarche/zlatan/internal/core"
	"github.com/marcodellemarche/zlatan/internal/immich"
	"github.com/marcodellemarche/zlatan/internal/nextcloud"
	"time"
)

func hasToken(store *fakeStore, provider string) bool {
	store.mu.Lock()
	defer store.mu.Unlock()
	_, ok := store.tokens[provider]
	return ok
}

// Each credential goes as soon as no track can use it any more: the Nextcloud
// password with the Drive half, the Immich key with the Photos half, and the
// Google token, which both use, only when both are done.
func TestReleaseFinishedForgetsWhatNoTrackNeeds(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	nc := &fakeNextcloud{}
	google := &fakeGoogle{}
	r := newRunner(t, store, &fakeExecutor{}).WithNextcloud(nc).WithGoogle(google)
	seedToken(t, store, sealerOf(t, r))
	seedNextcloud(t, store, sealerOf(t, r))
	seedImmich(t, store, sealerOf(t, r))

	// Drive done, Photos still waiting for the person's upload.
	store.migration.DriveState = core.DriveDone
	store.migration.PhotosState = core.PhotosAwaitingUpload
	r.releaseFinished(ctx, "marco")

	if hasToken(store, nextcloud.Provider) {
		t.Error("the Nextcloud app password outlived the Drive half")
	}
	if len(nc.revoked) != 1 || nc.revoked[0] != "app-password-1234" {
		t.Errorf("the app password was not deleted in Nextcloud: %v", nc.revoked)
	}
	if !hasToken(store, "google") || !hasToken(store, immich.Provider) {
		t.Fatal("Photos is not done: its credentials must stay")
	}

	// Photos done too: everything goes, and the Google grant is withdrawn.
	store.migration.PhotosState = core.PhotosDone
	r.releaseFinished(ctx, "marco")

	if hasToken(store, immich.Provider) || hasToken(store, "google") {
		t.Error("a credential outlived the migration")
	}
	if len(google.revoked) != 1 || google.revoked[0] != "1//refresh" {
		t.Errorf("the Google grant was not revoked: %v", google.revoked)
	}
}

// A failed track keeps its credentials, so "try again" needs no reconnect.
func TestReleaseFinishedKeepsCredentialsForARetry(t *testing.T) {
	store := newFakeStore()
	r := newRunner(t, store, &fakeExecutor{}).WithNextcloud(&fakeNextcloud{}).WithGoogle(&fakeGoogle{})
	seedToken(t, store, sealerOf(t, r))
	seedNextcloud(t, store, sealerOf(t, r))

	store.migration.DriveState = core.DriveFailed
	store.migration.PhotosState = core.PhotosDone
	r.releaseFinished(context.Background(), "marco")

	if !hasToken(store, "google") || !hasToken(store, nextcloud.Provider) {
		t.Error("a failed Drive half lost the credentials its retry needs")
	}
}

// Whoever stops using the service, halfway or after one half, must not leave
// a token here for good: the idle sweep forgets everything.
func TestSweepIdleForgetsEverything(t *testing.T) {
	store := newFakeStore()
	google := &fakeGoogle{}
	r := newRunner(t, store, &fakeExecutor{}).WithNextcloud(&fakeNextcloud{}).WithGoogle(google)
	seedToken(t, store, sealerOf(t, r))
	seedNextcloud(t, store, sealerOf(t, r))
	seedImmich(t, store, sealerOf(t, r))
	store.idle = []string{"marco"}

	r.sweepIdle(context.Background(), 0)

	for _, p := range []string{"google", nextcloud.Provider, immich.Provider} {
		if hasToken(store, p) {
			t.Errorf("the %s credential survived the idle sweep", p)
		}
	}
	if len(google.revoked) != 1 {
		t.Errorf("the Google grant was not revoked: %v", google.revoked)
	}
}

// The credential idle threshold must never drop below a floor: retention 0
// ("keep no staging") must not forget credentials the instant someone connects
// them.
func TestCredentialIdleHasAFloor(t *testing.T) {
	floor := time.Duration(config.DefaultRetentionDays) * 24 * time.Hour
	if got := credentialIdle(0); got != floor {
		t.Errorf("credentialIdle(0) = %v, want the floor %v", got, floor)
	}
	if got := credentialIdle(time.Hour); got != floor {
		t.Errorf("credentialIdle(1h) = %v, want the floor %v", got, floor)
	}
	long := 30 * 24 * time.Hour
	if got := credentialIdle(long); got != long {
		t.Errorf("credentialIdle(30d) = %v, want %v", got, long)
	}
}
