-- SPDX-License-Identifier: AGPL-3.0-or-later
-- +goose Up

-- When both tracks first reached a terminal state, so the staging sweeper can
-- apply the retention window. It cannot use updated_at: that moves for any
-- progress update, so a migration touched after it finished would look fresh
-- and its staging would never be purged. Stamped once and never cleared: the
-- point is "when did this person's migration end", not "is it still ended".
ALTER TABLE migrations ADD COLUMN finished_at TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE migrations DROP COLUMN finished_at;
