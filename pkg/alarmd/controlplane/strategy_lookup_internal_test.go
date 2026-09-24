// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"strconv"
	"sync"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A lookup running while a round replaces the index reads one whole
// publication or the other, never a mix: the index is built off to the side
// and swapped under the lock, and a reader holds only the read lock for the
// swap. Run under -race.
func TestALookupDuringAReplacementReadsOnePublicationWhole(t *testing.T) {
	state := &strategyLookupState{}
	publication := func(round int) *strategyIndex {
		groups := []QueryGroup{{Identity: execution.QueryGroupIdentity("qg-" + strconv.Itoa(round)),
			Plans: []FrozenPlan{{Identity: execution.PlanIdentity{TenantID: "t", BusinessID: "2", StrategyID: "7"}}}}}
		dispositions := []ObjectDisposition{{SourceID: "7", Scope: "PLAN", Disposition: DispositionAccepted}}
		return buildStrategyIndex(SnapshotPublicationRef{SnapshotRevision: execution.SnapshotRevision("rev-" + strconv.Itoa(round))}, groups, dispositions)
	}
	state.replace(publication(0))
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for reader := 0; reader < 4; reader++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				answer := state.lookup("7")
				if !answer.Available || !answer.Found || len(answer.Plans) != 1 || len(answer.Dispositions) != 1 {
					t.Errorf("lookup during replacement = %+v", answer)
					return
				}
				// The Plan's group and the publication are from the same round.
				round := string(answer.Publication.SnapshotRevision)[len("rev-"):]
				if string(answer.Plans[0].QueryGroup) != "qg-"+round {
					t.Errorf("lookup mixed publications: %s in %s", answer.Plans[0].QueryGroup, answer.Publication.SnapshotRevision)
					return
				}
			}
		}()
	}
	for round := 1; round <= 200; round++ {
		state.replace(publication(round))
	}
	close(stop)
	wg.Wait()
}

// Retained is the compiler's own word for a Plan kept in place of a document
// it could not rebuild, and nothing else: a strategy with one item accepted
// and another withheld has a current Plan and is not retained, while one
// under STALE_CONFIG or PENDING_REMOVAL is.
func TestRetainedIsTheCompilersWordForALastGoodPlanKeptInPlace(t *testing.T) {
	plan := execution.PlanIdentity{TenantID: "t", BusinessID: "2", StrategyID: "7"}
	groups := []QueryGroup{{Identity: "qg", Plans: []FrozenPlan{{Identity: plan}}}}
	for name, test := range map[string]struct {
		dispositions []ObjectDisposition
		retained     bool
	}{
		"one item accepted, another withheld": {dispositions: []ObjectDisposition{
			{SourceID: "7", Scope: "PLAN", Disposition: DispositionAccepted},
			{SourceID: "7", Scope: "PLAN", Disposition: DispositionUnsupported, Reason: "UNSUPPORTED_TARGET_PLAN"},
		}},
		"no-data half suspended beside an accepted Plan": {dispositions: []ObjectDisposition{
			{SourceID: "7", Scope: "PLAN", Disposition: DispositionAccepted},
			{SourceID: "7", Scope: "LEVEL", LevelID: 1, Disposition: DispositionCompatibilityIgnored, Reason: "NO_DATA_ROSTER_UNSUPPORTED"},
		}},
		"last good kept under a refusal": {dispositions: []ObjectDisposition{
			{SourceID: "7", Scope: "PLAN", Disposition: DispositionStaleConfig, Reason: "PLAN_INVALID"},
		}, retained: true},
		"kept one round past removal": {dispositions: []ObjectDisposition{
			{SourceID: "7", Scope: "PLAN", Disposition: DispositionPendingRemoval},
		}, retained: true},
	} {
		t.Run(name, func(t *testing.T) {
			answer := buildStrategyIndex(SnapshotPublicationRef{SnapshotRevision: "rev"}, groups, test.dispositions).lookup("7")
			if !answer.Found || len(answer.Plans) != 1 || answer.Retained != test.retained {
				t.Fatalf("lookup = found %v plans %d retained %v, want retained %v", answer.Found, len(answer.Plans), answer.Retained, test.retained)
			}
		})
	}
	// Without a Plan there is nothing retained, whatever the disposition.
	if answer := buildStrategyIndex(SnapshotPublicationRef{}, nil, []ObjectDisposition{{SourceID: "7", Disposition: DispositionStaleConfig}}).lookup("7"); answer.Retained || !answer.Found {
		t.Fatalf("a strategy with no Plan = %+v, want found and not retained", answer)
	}
}
