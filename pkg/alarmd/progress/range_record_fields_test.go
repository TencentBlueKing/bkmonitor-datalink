// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package progress

import (
	"context"
	"encoding/json"
	"sort"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// deployedEmptyRoundFields are the empty-round summary's fields the range
// decoder of the running release knows. Range records are decoded with
// DisallowUnknownFields, so a range record carrying any other field is
// corrupt to a replica of that release -- during a rolling update, and after
// a rollback. A field joins this list only once every release that may run
// beside or after this one knows it.
var deployedEmptyRoundFields = map[string]bool{"quiet": true, "emptied_by_target": true, "content_scope": true}

func lastCompletionAt60(empty *execution.EmptyRoundSummary) *execution.LastCompletionSummary {
	return &execution.LastCompletionSummary{Slot: 0, CompletedAt: "1970-01-01T00:00:01Z",
		Kind: execution.CompletionFullEmpty, Contract: progressContract(), Empty: empty}
}

func emptyRoundFieldsIn(t *testing.T, raw []byte) []string {
	t.Helper()
	var record struct {
		Progress struct {
			LastCompletion *struct {
				Empty map[string]json.RawMessage `json:"empty"`
			} `json:"last_completion"`
		} `json:"progress"`
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	fields := []string{}
	if record.Progress.LastCompletion != nil {
		for name := range record.Progress.LastCompletion.Empty {
			fields = append(fields, name)
		}
	}
	sort.Strings(fields)
	return fields
}

// A range begun on a record whose last round was identity-emptied, or an
// unfilled count, writes no field the deployed range decoder rejects; what
// the round said otherwise is kept, and a round that said nothing else
// carries no summary. The single-Slot record, decoded leniently by every
// release, keeps all of it.
func TestARangeRecordCarriesNoEmptyRoundFieldTheDeployedDecoderRejects(t *testing.T) {
	ctx := context.Background()
	identity := execution.ProgressIdentity{QueryGroup: "q"}
	for _, tc := range []struct {
		name string
		said *execution.EmptyRoundSummary
		want *execution.EmptyRoundSummary
	}{
		{"identity and outside", &execution.EmptyRoundSummary{EmptiedByTarget: true, ContentScope: "c", IdentityRounds: 3},
			&execution.EmptyRoundSummary{EmptiedByTarget: true, ContentScope: "c"}},
		{"identity only", &execution.EmptyRoundSummary{ContentScope: "c", IdentityRounds: 3}, nil},
		{"unfilled only", &execution.EmptyRoundSummary{Unfilled: true, ContentScope: "c"}, nil},
		{"unfilled and quiet", &execution.EmptyRoundSummary{Quiet: true, Unfilled: true, ContentScope: "c"},
			&execution.EmptyRoundSummary{Quiet: true, ContentScope: "c"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			single := mustEncode(t, execution.ScheduleProgress{Identity: identity, NextSlot: 60, LastCompletion: lastCompletionAt60(tc.said)})
			said, err := json.Marshal(tc.said)
			if err != nil {
				t.Fatal(err)
			}
			var saidFields map[string]json.RawMessage
			if err := json.Unmarshal(said, &saidFields); err != nil {
				t.Fatal(err)
			}
			if kept := emptyRoundFieldsIn(t, single); len(kept) != len(saidFields) {
				t.Fatalf("the single-Slot record keeps %v of what the round said, %s", kept, said)
			}
			fake := &controlFake{value: single}
			store := mustStore(t, fake)
			request := execution.ExpiredRangeRequest{Projection: rangeFixture(t), OwnerFence: execution.OwnerFence{QueryGroup: "q", OwnerID: "worker", OwnerEpoch: 1, LeaseToken: "lease"}}
			if result, err := store.BeginRange(ctx, request); err != nil || result.Status != execution.ProgressCommitted {
				t.Fatalf("begin: %+v %v", result, err)
			}
			for _, field := range emptyRoundFieldsIn(t, fake.value) {
				if !deployedEmptyRoundFields[field] {
					t.Errorf("the range record carries %q, which the deployed range decoder rejects as unknown", field)
				}
			}
			load, err := store.LoadProgress(ctx, identity)
			if err != nil || load.Progress.UnfinishedRange == nil || load.Progress.LastCompletion == nil {
				t.Fatalf("load: %+v %v", load, err)
			}
			got := load.Progress.LastCompletion.Empty
			if (got == nil) != (tc.want == nil) || got != nil && *got != *tc.want {
				t.Fatalf("range record keeps %+v, want %+v", got, tc.want)
			}
		})
	}
}
