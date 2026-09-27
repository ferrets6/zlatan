-- SPDX-License-Identifier: AGPL-3.0-or-later
-- +goose Up

-- How much space the person's data takes up at Google, read from the OAuth
-- quota (rclone about), so the wizard can show the size of what is being
-- moved.
--
-- Google reports Drive usage exactly, and that is already stored as
-- drive_source_bytes from the pre-copy scan. Photos has no separate figure:
-- rclone reports it inside "other", which is everything outside Drive (Gmail
-- plus Photos), so google_other_bytes is an upper bound for Photos, not its
-- size. google_total_bytes is -1 for an account with no quota limit.
ALTER TABLE migrations ADD COLUMN google_other_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE migrations ADD COLUMN google_total_bytes INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE migrations DROP COLUMN google_total_bytes;
ALTER TABLE migrations DROP COLUMN google_other_bytes;
