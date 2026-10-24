// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.
package execution

import (
	"testing"
	"time"
)

// Without a floor the candidate is half the hold, or none below a step; with
// one it is halfway from the floor to the hold, and there is none once the
// gap is no wider than a step. A floor at or past the hold is no floor.
func TestCandidateReadHoldHalvesTheGapAboveTheFloor(t *testing.T) {
	const step = time.Minute
	for _, c := range []struct {
		name          string
		hold, floor   time.Duration
		floored       bool
		want          time.Duration
		wantCandidate bool
	}{
		{"no hold", 0, 0, false, 0, false},
		{"no floor", 300 * time.Second, 0, false, 150 * time.Second, true},
		{"no floor under two steps", 100 * time.Second, 0, false, 0, true},
		{"floor at no hold", 100 * time.Second, 0, true, 50 * time.Second, true},
		{"floor at half", 300 * time.Second, 150 * time.Second, true, 225 * time.Second, true},
		{"gap of a step", 300 * time.Second, 240 * time.Second, true, 0, false},
		{"gap under a step", 300 * time.Second, 250 * time.Second, true, 0, false},
		{"floor at the hold", 300 * time.Second, 300 * time.Second, true, 150 * time.Second, true},
	} {
		got, candidate := CandidateReadHold(c.hold, c.floor, c.floored, step)
		if got != c.want || candidate != c.wantCandidate {
			t.Errorf("%s: %v %v, want %v %v", c.name, got, candidate, c.want, c.wantCandidate)
		}
	}
}
