// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package inputslice

import (
	"errors"
	"strings"
	"testing"
)

// TestConditionBudgetIsNamed lifts the leaf limit, which every split costs at
// least two leaves of and which therefore stops a path at depth 31 long
// before the budget would, so that the budget itself is reached: two values
// that differ only 61 characters from the end are cut one character deeper
// per split, and the split to depth 43 is refused, since 10 + 48·43 = 2074
// passes 2048 while 10 + 48·42 = 2026 does not.
func TestConditionBudgetIsNamed(t *testing.T) {
	tail := strings.Repeat("a", 60)
	values := []string{"x" + tail, "y" + tail}
	tree, err := newTree([]string{"pod"}, 1, 1_000_000, FixedCut("pod"))
	if err != nil {
		t.Fatal(err)
	}
	leaf := tree.Pending()[0]
	for depth := 0; ; depth++ {
		children, err := tree.Split(leaf.ID, SampleOf(map[string][]string{"pod": values}))
		if err != nil {
			var concentrated *ValueConcentratedError
			if !errors.As(err, &concentrated) || concentrated.Reason != ReasonConditionBudget {
				t.Fatalf("at depth %d got %v, want the condition budget", depth, err)
			}
			if depth != 42 {
				t.Fatalf("refused at depth %d, want 42", depth)
			}
			break
		}
		next := -1
		for index, child := range children {
			if child.Slice.ConditionBytes() > ConditionBudget {
				t.Fatalf("child at depth %d takes %d bytes", depth+1, child.Slice.ConditionBytes())
			}
			matched, err := child.Slice.Matches(map[string]string{"pod": values[0]})
			if err != nil {
				t.Fatal(err)
			}
			if matched {
				next = index
			}
		}
		if next < 0 {
			t.Fatalf("no child at depth %d holds %q", depth+1, values[0])
		}
		leaf = children[next]
	}
}
