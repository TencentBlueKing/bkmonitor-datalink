// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
)

// The summary's word about an empty round reaches the tracker as written, and
// a summary without one restores none.
func TestTheEmptyRoundIsMappedFromTheCommittedSummary(t *testing.T) {
	summary := &execution.LastCompletionSummary{Slot: 1_700_000_060, Kind: execution.CompletionFullEmpty,
		Empty: &execution.EmptyRoundSummary{Quiet: true, EmptiedByTarget: true, ContentScope: "content-a"}}
	want := &fleet.RestoredEmptyRound{Quiet: true, EmptiedByTarget: true, ContentScope: "content-a"}
	if round := restoredRoundOf(summary); round == nil || !reflect.DeepEqual(round.Empty, want) {
		t.Fatalf("restored round = %+v, want the empty round %+v", round, want)
	}
	summary.Empty = nil
	if round := restoredRoundOf(summary); round == nil || round.Empty != nil {
		t.Fatalf("restored round = %+v, want no empty round from a summary without one", round)
	}
}

// A publish restores each object with the content its owner runs now, read
// from the owner, so the record's word about an empty round is restored only
// under the content it was said under.
func TestAPublishRestoresUnderTheContentTheOwnerRunsNow(t *testing.T) {
	at := time.Now()
	slot := at.Add(-24 * time.Hour)
	owned := []execution.QueryGroupIdentity{"qg-same", "qg-moved"}
	publisher := fleetPublisher{
		tracker:       fleet.NewTracker(nil, "pod", func() time.Time { return at }),
		owned:         func() []execution.QueryGroupIdentity { return owned },
		now:           func() time.Time { return at },
		restoreBudget: 8, staleAfter: 48 * time.Hour,
		contentScope: func(queryGroup execution.QueryGroupIdentity) string {
			if queryGroup == "qg-same" {
				return "content-a"
			}
			return "content-b"
		},
		restore: oneByOne(func(context.Context, execution.QueryGroupIdentity) (fleet.RestoredState, error) {
			return fleet.RestoredState{LastCompletion: "FULL_EMPTY_COMPLETED", NextSlot: at.Add(time.Hour),
				EmptyRunSince: slot.Add(-20 * 24 * time.Hour),
				LastRound: &fleet.RestoredRound{Slot: slot, Kind: "FULL_EMPTY_COMPLETED",
					Empty: &fleet.RestoredEmptyRound{Quiet: true, ContentScope: "content-a"}}}, nil
		}),
	}
	publisher.snapshot(context.Background())
	kinds := map[string]string{}
	for _, row := range publisher.tracker.NoData() {
		kinds[row.QueryGroup] = row.Kind
	}
	if kinds["qg-same"] != fleet.KindQuiet || kinds["qg-moved"] != fleet.KindEmptyEveryRound {
		t.Fatalf("restored rows %v, want qg-same quiet under its content and qg-moved on the empty line under other content", kinds)
	}
}
