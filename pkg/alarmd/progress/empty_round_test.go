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
	"bytes"
	"context"
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The summary of an empty round keeps what the round said about its
// emptiness - an event count of groups at rest, or every series withheld as
// outside the monitoring target - with the content it was said under, so a
// replica that restores the object reads its row without waiting for a
// round. It is written only for a whole, empty primary with one of the two
// set; any other round writes none, and a record without it re-encodes as it
// was.
func TestTheSummaryOfAnEmptyRoundKeepsWhatItSaidAndUnderWhichContent(t *testing.T) {
	empty := func(quiet, outside bool) *execution.PrimaryInputFact {
		return &execution.PrimaryInputFact{Completeness: execution.CompletenessFull, DataState: execution.DataStateEmpty,
			QuietWhenEmpty: quiet, EmptiedByTarget: outside}
	}
	cases := []struct {
		name    string
		kind    execution.CompletionKind
		primary *execution.PrimaryInputFact
		want    *execution.EmptyRoundSummary
	}{
		{"at rest", execution.CompletionFullEmpty, empty(true, false),
			&execution.EmptyRoundSummary{Quiet: true, ContentScope: "qg-object-a"}},
		{"outside the target", execution.CompletionFullEmpty, empty(false, true),
			&execution.EmptyRoundSummary{EmptiedByTarget: true, ContentScope: "qg-object-a"}},
		{"empty and neither", execution.CompletionFullEmpty, empty(false, false), nil},
		// Without a content to say it under, the word could never be
		// restored: a commit that declares none writes none.
		{"no content declared", execution.CompletionFullEmpty, empty(true, false), nil},
		// Only a whole, empty primary speaks about its emptiness: a flag on
		// a round with records is not copied.
		{"records", execution.CompletionFull,
			&execution.PrimaryInputFact{Completeness: execution.CompletenessFull, DataState: execution.DataStateData, QuietWhenEmpty: true}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &controlFake{missing: true}
			store := mustStore(t, fake)
			identity := execution.ProgressIdentity{QueryGroup: "q"}
			scope := "qg-object-a"
			if tc.name == "no content declared" {
				scope = ""
			}
			commit := execution.ProgressCommitRequest{
				Identity: identity, ExpectedNextSlot: 60, ContentScope: scope,
				OwnerFence: execution.OwnerFence{QueryGroup: "q", OwnerID: "worker", OwnerEpoch: 1, LeaseToken: "lease"},
				Completion: execution.SlotCompletion{Contract: progressContractAt(60), Kind: tc.kind, Primary: tc.primary,
					Result: observability.ResultSuccess},
			}
			if result, err := store.CommitProgress(context.Background(), commit); err != nil || result.Status != execution.ProgressCommitted {
				t.Fatalf("CommitProgress() = (%+v, %v)", result, err)
			}
			loaded, err := store.LoadProgress(context.Background(), identity)
			if err != nil || loaded.Progress == nil || loaded.Progress.LastCompletion == nil {
				t.Fatalf("LoadProgress() = (%+v, %v)", loaded, err)
			}
			if got := loaded.Progress.LastCompletion.Empty; !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("the summary's empty round = %+v, want %+v", got, tc.want)
			}
			if tc.want == nil {
				if bytes.Contains(fake.value, []byte(`"empty"`)) {
					t.Fatalf("a round with nothing to say about its emptiness wrote %s", fake.value)
				}
				reencoded, err := encode(*loaded.Progress)
				if err != nil || !bytes.Equal(reencoded, fake.value) {
					t.Fatalf("the record re-encodes as %s (%v), want it as written: %s", reencoded, err, fake.value)
				}
			}
		})
	}
}
