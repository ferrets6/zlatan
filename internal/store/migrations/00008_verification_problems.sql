-- SPDX-License-Identifier: AGPL-3.0-or-later
-- +goose Up

-- The items a check found wrong, by name and reason, as a JSON array of
-- core.Problem. The counts alone said "1 pending" and nothing about which
-- file, so the person had no way to find it. '' means none recorded.
ALTER TABLE verifications ADD COLUMN problems TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE verifications DROP COLUMN problems;
