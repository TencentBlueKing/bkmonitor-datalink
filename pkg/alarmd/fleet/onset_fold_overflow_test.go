// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"testing"
	"time"
)

// The minutes past the bound are folded by their objects, not by their count.
// A fixture whose overflow minutes hold one object each cannot tell the two
// apart, so this one gives every overflow minute three: the fold has to add
// up to the line with the listed minutes, Other and WithoutOnset together.
func TestAnOnsetFoldPastTheBoundStillAddsUpToItsLine(t *testing.T) {
	base := time.Date(2026, 9, 21, 7, 0, 0, 0, time.UTC)
	onsets := map[time.Time]int{}
	total := 0
	for i := 0; i < MaxOnsetFold+5; i++ {
		objects := 3
		if i < MaxOnsetFold {
			objects = 10 + i // the listed minutes, larger so they sort first
		}
		onsets[base.Add(time.Duration(i)*time.Minute)] = objects
		total += objects
	}
	fold := onsetFold(onsets, 2)
	if fold == nil || len(fold.Minutes) != MaxOnsetFold || fold.Distinct != MaxOnsetFold+5 {
		t.Fatalf("fold = %+v, want %d listed minutes of %d distinct", fold, MaxOnsetFold, MaxOnsetFold+5)
	}
	listed := 0
	for _, minute := range fold.Minutes {
		listed += minute.Objects
	}
	if listed+fold.Other+fold.WithoutOnset != total+2 {
		t.Fatalf("listed %d + other %d + without onset %d = %d, want the line's %d: an overflow bucket that counts "+
			"minutes instead of objects leaves the difference to the line invisible", listed, fold.Other, fold.WithoutOnset,
			listed+fold.Other+fold.WithoutOnset, total+2)
	}
	if fold.Other != 5*3 {
		t.Fatalf("other = %d, want the five overflow minutes' fifteen objects", fold.Other)
	}
}
