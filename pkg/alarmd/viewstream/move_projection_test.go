// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package viewstream_test

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream"
)

// A Query Group moved to another Worker is in that Worker's view from the
// projection its record moves in, and in the old Worker's no more: its view
// entry arrives with its own Assignment, whichever batch of a rollout moved
// it. So a moved Query Group waits for its view one propagation from its own
// move, never from the rollout's start.
func TestAMovedQueryGroupIsInItsNewWorkersViewFromTheProjectionItMovesIn(t *testing.T) {
	published := map[string]viewstream.Content{"qg-1": content("obj-1", "s1"), "qg-2": content("obj-2", "s2")}
	before := desiredAt(publicationA, map[string]string{"qg-1": "w1", "qg-2": "w1"}, published)
	// A later batch moves qg-2 alone.
	after := desiredAt(publicationA, map[string]string{"qg-1": "w1", "qg-2": "w2"}, published)
	in := func(desired viewstream.Desired, worker, queryGroup string) bool {
		t.Helper()
		view, err := desired.Project(worker)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range view.Entries {
			if entry.QueryGroup == execution.QueryGroupIdentity(queryGroup) {
				return true
			}
		}
		return false
	}
	if !in(before, "w1", "qg-2") || in(before, "w2", "qg-2") {
		t.Fatal("before the move: qg-2 is not in exactly its first Worker's view")
	}
	if !in(after, "w2", "qg-2") {
		t.Fatal("the projection qg-2's record moves in does not carry it in its new Worker's view")
	}
	if in(after, "w1", "qg-2") {
		t.Fatal("the projection qg-2's record moves in still carries it in its old Worker's view")
	}
	if !in(after, "w1", "qg-1") {
		t.Fatal("a Query Group the batch did not move left its Worker's view")
	}
}
