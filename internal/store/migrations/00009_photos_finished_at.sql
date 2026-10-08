-- SPDX-License-Identifier: AGPL-3.0-or-later
-- +goose Up

-- Staging holds only the Photos half's archives (Drive copies straight into
-- Nextcloud), so its retention follows Photos alone. Waiting for both tracks
-- kept a Photos-only person's archives forever: their Drive half never ends.
-- The column now means "when Photos last reached done, failed or cancelled",
-- cleared when Photos runs again. A stamp already here was set when both
-- tracks ended, so Photos had ended by then too, and it stays valid.
ALTER TABLE migrations RENAME COLUMN finished_at TO photos_finished_at;

-- The old stamp was never cleared when Photos ran again, so a row whose Photos
-- is running now may carry one from months ago, and its next ending would keep
-- it and be purged at once. A running Photos half has no window yet.
UPDATE migrations SET photos_finished_at = ''
WHERE photos_state NOT IN ('done', 'failed', 'cancelled');

-- +goose Down
ALTER TABLE migrations RENAME COLUMN photos_finished_at TO finished_at;
