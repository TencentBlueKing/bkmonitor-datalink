// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// openStatementFor is a Plan's open statement at one Slot's version over
// scopes, expecting revision.
func openStatementFor(t *testing.T, revision uint64, scopes ...execution.GapScopeMutation) execution.PlanGapMutation {
	t.Helper()
	statement, err := execution.BuildPlanGapMutation(execution.PlanGapMutation{
		Identity:               execution.PlanGapIdentity{Plan: execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"}, StateGeneration: "state-v1"},
		ExpectedMarkerRevision: revision, ScheduleRevision: "plan-schedule-v1",
		ApplyVersion: execution.ApplyVersion{StateApplyEpoch: 1, EvaluationTime: 1_788_000_000, SlotDigest: "slot"},
		Scopes:       scopes,
	})
	if err != nil {
		t.Fatal(err)
	}
	return statement
}

func levelOpen(level uint32, reason string, required uint32) execution.GapScopeMutation {
	return execution.GapScopeMutation{Scope: execution.GapScope{LevelID: level, HasLevel: true}, Kind: execution.GapOpen,
		ReasonCode: execution.ReasonCode(reason), RequiredFullSlots: required}
}

// B3's growth is a second statement at the Slot's version, so it is judged
// by the rule the store extends a marker at its own version by
// (execution.ExtendSameSlotGap), not by the fold of the reasons: a slice
// that opens a scope the marker lacks grows it, one revision on, the scopes
// it held kept as they were; a slice that answers a held scope worse raises
// its reason, UNAVAILABLE over PARTIAL, as the store extends a marker at its
// version; one that answers it better writes nothing; a statement of another
// marker is refused by name.
func TestASliceGrowsTheMarkerOnlyByWhatTheStoreExtendsItWith(t *testing.T) {
	previous := openStatementFor(t, 3, levelOpen(5, contract.ReasonQueryPartial, 1))
	grown, grows, err := growOpenStatement(previous, openStatementFor(t, 3, levelOpen(6, contract.ReasonQueryUnavailable, 1)))
	if err != nil || !grows {
		t.Fatalf("grow = %v, %v; want the new scope added", grows, err)
	}
	if grown.ExpectedMarkerRevision != 4 || grown.ValidateDigest() != nil || len(grown.Scopes) != 2 ||
		grown.Scopes[0] != previous.Scopes[0] || grown.Scopes[1].Scope.LevelID != 6 {
		t.Fatalf("grown statement %+v, want both scopes, the held one unchanged, at revision 4", grown)
	}
	held := []execution.GapScopeState{{Scope: previous.Scopes[0].Scope, Status: execution.GapStatusGapped,
		ReasonCode: previous.Scopes[0].ReasonCode, RequiredFullSlots: 1}}
	if _, extended := execution.ExtendSameSlotGap(held, grown.Scopes); !extended {
		t.Fatal("the store would not extend the marker with the grown statement")
	}

	worse, grows, err := growOpenStatement(previous, openStatementFor(t, 3, levelOpen(5, contract.ReasonQueryUnavailable, 1)))
	if err != nil || !grows || len(worse.Scopes) != 1 || worse.Scopes[0].ReasonCode != execution.ReasonCode(contract.ReasonQueryUnavailable) {
		t.Fatalf("a worse answer on a held scope grows = %+v, %v, %v; want the scope raised to UNAVAILABLE", worse, grows, err)
	}
	if _, extended := execution.ExtendSameSlotGap(held, worse.Scopes); !extended {
		t.Fatal("the store would not extend the marker with the raised reason")
	}
	unavailable := openStatementFor(t, 3, levelOpen(5, contract.ReasonQueryUnavailable, 1))
	if _, grows, err := growOpenStatement(unavailable, openStatementFor(t, 3, levelOpen(5, contract.ReasonQueryPartial, 1))); err != nil || grows {
		t.Fatalf("a better answer on a held scope grows = %v, %v; want nothing written", grows, err)
	}
	other := openStatementFor(t, 3, levelOpen(6, contract.ReasonQueryPartial, 1))
	other.Identity.StateGeneration = "state-v2"
	if _, _, err := growOpenStatement(previous, other); err == nil {
		t.Fatal("grew a marker with another marker's statement")
	}
}

// A retry of a Slot whose earlier attempt already wrote the Plan's marker at
// this version is read again, in slices that need not be the first
// attempt's and with answers that need not be the same. What it proposes
// beyond the marker - a scope the marker lacks, a worse reason - is written,
// from the revision it read; what the marker already says is not written
// again. Before this the retry's statement was dropped whole at the Slot's
// version, and a scope only the retry answered went out with no marker over
// its events.
func TestARetrysStatementGrowsTheMarkerItsFirstAttemptWrote(t *testing.T) {
	written := openStatementFor(t, 3, levelOpen(5, contract.ReasonQueryPartial, 1))
	loaded := execution.GapLoadResult{Items: []execution.GapGuardSnapshot{{
		Identity: written.Identity, MarkerRevision: 4, PersistedApplyVersion: written.ApplyVersion,
		PersistedMutationDigest: written.MutationDigest, Status: execution.GapFound, LastScheduleRevision: written.ScheduleRevision,
		Scopes: []execution.GapScopeState{{Scope: written.Scopes[0].Scope, Status: execution.GapStatusGapped,
			ReasonCode: written.Scopes[0].ReasonCode, RequiredFullSlots: 1}},
	}}}
	for _, test := range []struct {
		name   string
		retry  execution.PlanGapMutation
		writes bool
	}{
		{"the same statement", openStatementFor(t, 3, levelOpen(5, contract.ReasonQueryPartial, 1)), false},
		{"a scope the marker lacks", openStatementFor(t, 3, levelOpen(5, contract.ReasonQueryPartial, 1), levelOpen(6, contract.ReasonQueryPartial, 1)), true},
		{"a worse reason", openStatementFor(t, 3, levelOpen(5, contract.ReasonQueryUnavailable, 1)), true},
		{"a better reason on a scope held worse", openStatementFor(t, 3, levelOpen(5, contract.ReasonQueryPartial, 1)), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			marker := loaded
			if test.name == "a better reason on a scope held worse" {
				marker = execution.GapLoadResult{Items: append([]execution.GapGuardSnapshot(nil), loaded.Items...)}
				marker.Items[0].Scopes = []execution.GapScopeState{{Scope: written.Scopes[0].Scope, Status: execution.GapStatusGapped,
					ReasonCode: execution.ReasonCode(contract.ReasonQueryUnavailable), RequiredFullSlots: 1}}
			}
			pending := uncommittedGapMutations(marker, []execution.PlanGapMutation{test.retry})
			if !test.writes {
				if len(pending) != 0 {
					t.Fatalf("pending = %+v, want nothing written again", pending)
				}
				return
			}
			if len(pending) != 1 || pending[0].ExpectedMarkerRevision != 4 || pending[0].ValidateDigest() != nil {
				t.Fatalf("pending = %+v, want the retry's statement from the revision it read, 4", pending)
			}
			if _, extends := execution.ExtendSameSlotGap(marker.Items[0].Scopes, pending[0].Scopes); !extends {
				t.Fatal("the store would not extend the marker with the retry's statement")
			}
		})
	}
}
