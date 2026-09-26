-- SPDX-License-Identifier: AGPL-3.0-or-later
-- +goose Up

-- What Zlatan learned before the Drive copy: the size of the source Drive and
-- the person's current Nextcloud usage. Kept on the row so the wizard can warn
-- on any later page load, not only at the moment of starting, and so the
-- estimate survives a restart. quota_total_bytes is -1 for an unlimited quota.
ALTER TABLE migrations ADD COLUMN drive_source_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE migrations ADD COLUMN quota_used_bytes   INTEGER NOT NULL DEFAULT 0;
ALTER TABLE migrations ADD COLUMN quota_total_bytes  INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE migrations DROP COLUMN quota_total_bytes;
ALTER TABLE migrations DROP COLUMN quota_used_bytes;
ALTER TABLE migrations DROP COLUMN drive_source_bytes;
