// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
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

	model "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// Under a declaring round every record lands in exactly one of five buckets,
// decided the way the round decided it: one record per bucket, and the
// buckets sum to the total. The plain tallies count under every policy; the
// buckets only under a declaring one, where the other policies fold to
// declared and undeclared.
func TestTheAssignmentScopeCensusPartitionsTheRecords(t *testing.T) {
	at := time.Unix(1_700_000_000, 0)
	digests := map[model.QueryGroupIdentity]string{"current": "c", "moving": "m", "stale": "s", "undeclared": "u", "pending-old": "p"}
	records := map[model.QueryGroupIdentity]ownership.AssignmentRecord{
		"current":    {ContentScope: "c"},
		"moving":     {ContentScope: "old-m", PendingContentScope: "m"},
		"stale":      {ContentScope: "old-s"},
		"undeclared": {},
		"unknown":    {ContentScope: "x"},
		// Names the current content but pends a change to yet another: the
		// round republishes the current one to cancel it; until then it is
		// not current.
		"pending-old": {ContentScope: "p", PendingContentScope: "elsewhere"},
	}
	facts := AssignmentScopeOf(at, AssignmentScopePolicyDeclared, digests, records)
	want := AssignmentScopeFacts{At: at, Policy: AssignmentScopePolicyDeclared, Total: 6, Declared: 5, Pending: 2,
		Current: 1, Moving: 1, Stale: 2, Undeclared: 1, ContentUnknown: 1}
	if *facts != want {
		t.Fatalf("declared census = %+v, want %+v", *facts, want)
	}
	if !facts.Consistent() {
		t.Fatalf("declared census does not sum: %+v", *facts)
	}

	for _, policy := range []string{AssignmentScopePolicyWithdrawn, AssignmentScopePolicyUntouched} {
		facts := AssignmentScopeOf(at, policy, digests, records)
		if facts.Policy != policy || facts.Total != 6 || facts.Declared != 5 || facts.Pending != 2 || facts.Undeclared != 1 ||
			facts.Current != 0 || facts.Moving != 0 || facts.Stale != 0 || facts.ContentUnknown != 0 {
			t.Fatalf("%s census = %+v, want the two tallies and no buckets", policy, *facts)
		}
		if !facts.Consistent() {
			t.Fatalf("%s census does not sum: %+v", policy, *facts)
		}
	}

	// An empty round is a consistent zero, not a nil.
	if facts := AssignmentScopeOf(at, AssignmentScopePolicyDeclared, nil, nil); facts == nil || facts.Total != 0 || !facts.Consistent() {
		t.Fatalf("empty census = %+v", facts)
	}
	// The identity is a check, not a tautology: a census whose buckets do not
	// sum fails it, and so does a nil.
	broken := *facts
	broken.Stale++
	if broken.Consistent() || (*AssignmentScopeFacts)(nil).Consistent() {
		t.Fatal("Consistent() passed a census whose buckets do not sum, or a nil")
	}
	for _, word := range AssignmentScopePolicies {
		if word != AssignmentScopePolicyDeclared && word != AssignmentScopePolicyWithdrawn && word != AssignmentScopePolicyUntouched {
			t.Fatalf("unknown policy word %q", word)
		}
	}
}

// The census reaches the verdict route from the newest leader's snapshot,
// with the replica that published it, and a follower's absence changes
// nothing.
func TestTheVerdictRouteCarriesTheNewestAssignmentScopeCensus(t *testing.T) {
	snapshots := healthySnapshots()
	older := &AssignmentScopeFacts{At: now.Add(-2 * time.Minute), Policy: AssignmentScopePolicyWithdrawn, Total: 5, Undeclared: 5}
	newer := &AssignmentScopeFacts{At: now.Add(-time.Minute), Policy: AssignmentScopePolicyDeclared, Total: 5, Declared: 5, Current: 4, Moving: 1}
	snapshots[0].AssignmentScope = older
	snapshots[1].AssignmentScope = newer
	handler := handlerWith(t, snapshots, Expectation{QueryGroups: 949, Known: true}, replicas())
	_, health := get(t, handler, "/api/health")
	scope, _ := health["assignment_scope"].(map[string]any)
	if scope == nil || scope["policy"] != AssignmentScopePolicyDeclared || scope["total"] != 5.0 || scope["current"] != 4.0 || scope["moving"] != 1.0 {
		t.Fatalf("assignment_scope = %v, want the newer leader's declaring census", health["assignment_scope"])
	}
	if health["assignment_scope_replica"] != snapshots[1].Replica {
		t.Fatalf("assignment_scope_replica = %v, want %s", health["assignment_scope_replica"], snapshots[1].Replica)
	}
	for index := range snapshots {
		snapshots[index].AssignmentScope = nil
	}
	handler = handlerWith(t, snapshots, Expectation{QueryGroups: 949, Known: true}, replicas())
	_, health = get(t, handler, "/api/health")
	if _, present := health["assignment_scope"]; present {
		t.Fatalf("assignment_scope present with no leader publishing one: %v", health["assignment_scope"])
	}
}

// The sweep reaches the verdict route beside the census, from the newest
// leader's snapshot: the census counts the round's own Query Groups and never
// sees a retired record, so "swept and reclaimed six" has to stand next to
// "2401 of 2401 current" or the six are known to nobody.
func TestTheVerdictRouteCarriesTheNewestSweepBesideTheCensus(t *testing.T) {
	snapshots := healthySnapshots()
	snapshots[0].AssignmentSweep = &AssignmentSweepFacts{At: now.Add(-3 * time.Minute), Result: "success", Scanned: 2401}
	snapshots[1].AssignmentSweep = &AssignmentSweepFacts{At: now.Add(-time.Minute), Result: "success", Scanned: 2407, Retired: 6, Reclaimed: 6, DurationSeconds: 0.04}
	handler := handlerWith(t, snapshots, Expectation{QueryGroups: 949, Known: true}, replicas())
	_, health := get(t, handler, "/api/health")
	sweep, _ := health["assignment_sweep"].(map[string]any)
	if sweep == nil || sweep["result"] != "success" || sweep["scanned"] != 2407.0 || sweep["retired"] != 6.0 || sweep["reclaimed"] != 6.0 {
		t.Fatalf("assignment_sweep = %v, want the newer leader's sweep", health["assignment_sweep"])
	}
	if health["assignment_sweep_replica"] != snapshots[1].Replica {
		t.Fatalf("assignment_sweep_replica = %v, want %s", health["assignment_sweep_replica"], snapshots[1].Replica)
	}
	for index := range snapshots {
		snapshots[index].AssignmentSweep = nil
	}
	handler = handlerWith(t, snapshots, Expectation{QueryGroups: 949, Known: true}, replicas())
	_, health = get(t, handler, "/api/health")
	if _, present := health["assignment_sweep"]; present {
		t.Fatalf("assignment_sweep present with no leader publishing one: %v", health["assignment_sweep"])
	}
	// The sweep's own identity: every retired record was reclaimed, held or
	// changed; a nil is not consistent.
	if !(&AssignmentSweepFacts{Retired: 6, Reclaimed: 4, HeldByLease: 1, Changed: 1}).Consistent() ||
		(&AssignmentSweepFacts{Retired: 6, Reclaimed: 4}).Consistent() || (*AssignmentSweepFacts)(nil).Consistent() {
		t.Fatal("Consistent() on the sweep does not check retired == reclaimed + held + changed")
	}
}

// The leader's round, stage by stage, reaches the verdict route from the
// newest leader's snapshot, so where the round's time goes is one read.
func TestTheVerdictRouteCarriesTheNewestLeaderRound(t *testing.T) {
	snapshots := healthySnapshots()
	snapshots[0].LeaderRound = &LeaderRoundFacts{At: now.Add(-3 * time.Minute), Result: LeaderRoundCompleted, TotalSeconds: 9}
	snapshots[1].LeaderRound = &LeaderRoundFacts{At: now.Add(-time.Minute), Result: LeaderRoundCompleted, TotalSeconds: 0.5,
		Stages: []LeaderRoundStage{{Stage: LeaderRoundStageAssignmentSweep, Seconds: 0.2}}}
	handler := handlerWith(t, snapshots, Expectation{QueryGroups: 949, Known: true}, replicas())
	_, health := get(t, handler, "/api/health")
	round, _ := health["leader_round"].(map[string]any)
	stages, _ := round["stages"].([]any)
	if round == nil || round["total_seconds"] != 0.5 || len(stages) != 1 || health["leader_round_replica"] != snapshots[1].Replica {
		t.Fatalf("leader_round = %v from %v, want the newer leader's round", health["leader_round"], health["leader_round_replica"])
	}
}
