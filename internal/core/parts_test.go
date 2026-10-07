// SPDX-License-Identifier: AGPL-3.0-or-later

package core

import (
	"reflect"
	"testing"
)

func TestCountParts(t *testing.T) {
	const a, b, c = "takeout-20261006T101500Z-001.zip", "takeout-20261006T101500Z-002.zip", "takeout-20261006T101500Z-003.zip"
	cases := []struct {
		name     string
		names    []string
		expected int
		complete bool
		missing  []int
		tooMany  bool
	}{
		{"nothing declared yet", []string{a}, 0, false, nil, false},
		{"one of one", []string{a}, 1, true, nil, false},
		{"all three, any order", []string{c, a, b}, 3, true, nil, false},
		{"says which part is missing", []string{a, c}, 3, false, []int{2}, false},
		{"nothing arrived yet", nil, 2, false, []int{1, 2}, false},
		// The person said 2, but part 3 is here: the count is wrong, and
		// importing 1 and 2 would leave part 3 out.
		{"a part above the count", []string{a, c}, 2, false, []int{2}, true},
		{"more files than declared", []string{a, b, c}, 2, false, nil, true},
		// A file WITHOUT Google's number sits beside a numbered one: it is not a
		// part (Google always numbers a split export), so it never fills the gap.
		// "takeout (1).zip" is a browser duplicate far more often than a renamed
		// part 2, and importing it as part 2 would lose part 2's real contents.
		{"a non-numbered file does not fill a gap", []string{a, "takeout (1).zip"}, 2, false, []int{2}, false},
		// Nothing numbered at all: the declared count is all there is to go on.
		{"renamed file, count short", []string{"photos.zip"}, 2, false, nil, false},
		// A same-name upload of different content is stored under a hash
		// prefix; the number at the end still reads.
		{"hash prefix keeps the number", []string{"0123456789ab-" + a, b}, 2, true, nil, false},
		// A duplicate of part 1 (resent under a hash prefix) is still one part:
		// it must not stand in for a part that never arrived.
		{"a resent part 1 is still one part", []string{a, "0123456789ab-" + a}, 2, false, []int{2}, false},
		{"a resent only part is complete", []string{a, "0123456789ab-" + a}, 1, true, nil, false},
		// A browser re-download adds " (1)" before .zip: still part 1, credited to
		// part 1, so a complete set with a re-download does not stall.
		{"browser (1) suffix is still its part", []string{"takeout-x-001 (1).zip", "takeout-x-002.zip"}, 2, true, nil, false},
		{"re-download of part 1 does not fill a gap", []string{"takeout-x-001.zip", "takeout-x-001 (1).zip"}, 2, false, []int{2}, false},
		// A "-000" is shaped like a part but is not a valid one: it must not be
		// credited, and it must not fall into the no-number fallback either (where
		// a file on disk would stand in for a part just by being there).
		{"a zero-numbered file is not a part", []string{"takeout-x-000.zip"}, 1, false, []int{1}, false},
		{"a zero-numbered file does not fill a gap", []string{"takeout-x-000.zip", "takeout-x-002.zip"}, 2, false, []int{1}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := CountParts(tc.names, tc.expected)
			if p.Complete() != tc.complete {
				t.Errorf("Complete() = %v, want %v (%+v)", p.Complete(), tc.complete, p)
			}
			if !reflect.DeepEqual(p.Missing, tc.missing) {
				t.Errorf("Missing = %v, want %v", p.Missing, tc.missing)
			}
			if p.TooMany != tc.tooMany {
				t.Errorf("TooMany = %v, want %v", p.TooMany, tc.tooMany)
			}
		})
	}
}
