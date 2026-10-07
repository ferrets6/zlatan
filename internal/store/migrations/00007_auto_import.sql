-- SPDX-License-Identifier: AGPL-3.0-or-later
-- +goose Up

-- Whether the import should start by itself once every declared part is on
-- disk, for the Photos upload route (files sent to the site, or downloaded onto
-- the NAS by the kiosk browser). 0 = wait for an explicit "Start" (default);
-- 1 = a server-side sweep starts it when complete, even with the tab closed.
ALTER TABLE migrations ADD COLUMN auto_import INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE migrations DROP COLUMN auto_import;
