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
)

// Each replica's row carries what held its retained pool as it published,
// on the health route by its JSON names; a copy, so a later change to the
// snapshot does not reach it; and absent for a replica that published none,
// not filled in as an empty pool.
func TestTheReplicaRowCarriesWhatHeldItsRetainedPool(t *testing.T) {
	since := time.Unix(1_800_000_000, 0).UTC()
	holder := func(queryGroup string, bytes uint64) RetainedHolderRef {
		return RetainedHolderRef{QueryGroup: queryGroup, EvaluationTime: 1_800_000_000, Operation: "normal",
			Strategies: []StrategyRef{{StrategyID: "901", BusinessID: "2"}}, StrategiesTotal: 1, Since: since, Bytes: bytes,
			ByKind: map[string]uint64{"input": bytes}}
	}
	publish := func() *RetainedHolderFacts {
		oldest := holder("qg-old", 40)
		return &RetainedHolderFacts{Holders: []RetainedHolderRef{holder("qg-large", 700)}, Oldest: &oldest,
			Total: 2, AttributedBytes: 740, HeldBytes: 1_740}
	}
	snapshots := healthySnapshots()
	snapshots[0].RetainedHolders = publish()
	view := Aggregate(Expectation{QueryGroups: 949, Known: true}, snapshots, replicas(), now, freshness)
	rows := map[string]ReplicaView{}
	for _, row := range view.PerReplica {
		rows[row.Replica] = row
	}
	got := rows["pod-a"].RetainedHolders
	if got == nil || len(got.Holders) != 1 || got.Holders[0].QueryGroup != "qg-large" || got.Oldest == nil ||
		got.Oldest.QueryGroup != "qg-old" || got.Total != 2 || got.HeldBytes != 1_740 {
		t.Fatalf("pod-a retained holders = %+v, want what it published", got)
	}
	if rows["pod-b"].RetainedHolders != nil {
		t.Fatalf("pod-b published none and its row says %+v", rows["pod-b"].RetainedHolders)
	}
	published := snapshots[0].RetainedHolders
	published.Holders[0].QueryGroup, published.Holders[0].Strategies[0].StrategyID, published.Holders[0].ByKind["input"] = "qg-other", "1", 1
	published.Oldest.QueryGroup, published.Oldest.Strategies[0].StrategyID, published.Oldest.ByKind["input"] = "qg-other", "1", 1
	if got.Holders[0].QueryGroup != "qg-large" || got.Holders[0].Strategies[0].StrategyID != "901" || got.Holders[0].ByKind["input"] != 700 ||
		got.Oldest.QueryGroup != "qg-old" || got.Oldest.Strategies[0].StrategyID != "901" || got.Oldest.ByKind["input"] != 40 {
		t.Fatal("the replica row aliases the snapshot's retained holders")
	}
	snapshots[0].RetainedHolders = publish()

	handler := handlerWith(t, snapshots, Expectation{QueryGroups: 949, Known: true}, replicas())
	_, health := get(t, handler, "/api/health")
	perReplica, _ := health["per_replica"].([]any)
	var facts map[string]any
	for _, row := range perReplica {
		if fields := row.(map[string]any); fields["replica"] == "pod-a" {
			facts, _ = fields["retained_holders"].(map[string]any)
		}
	}
	holders, _ := facts["holders"].([]any)
	if len(holders) != 1 {
		t.Fatalf("health per_replica retained_holders = %v, want pod-a's one listed Slot", facts)
	}
	first, _ := holders[0].(map[string]any)
	byKind, _ := first["by_kind"].(map[string]any)
	strategies, _ := first["strategies"].([]any)
	listedOldest, _ := facts["oldest"].(map[string]any)
	if facts["total"] != float64(2) || facts["attributed_bytes"] != float64(740) || facts["held_bytes"] != float64(1_740) ||
		first["query_group"] != "qg-large" || first["evaluation_time"] != float64(1_800_000_000) || first["operation"] != "normal" ||
		first["since"] != "2027-01-15T08:00:00Z" || first["bytes"] != float64(700) || byKind["input"] != float64(700) ||
		len(strategies) != 1 || first["strategies_total"] != float64(1) || listedOldest["query_group"] != "qg-old" {
		t.Fatalf("health per_replica retained_holders = %v, want pod-a's by its JSON names", facts)
	}
}

// A holder is named by the first few strategies its object runs, all of
// them counted; an object this replica has not seen evaluate is named by an
// empty list, not null read as unknown.
func TestARetainedHolderIsNamedByItsFirstStrategies(t *testing.T) {
	strategies := make([]StrategyRef, RetainedHolderStrategiesListed+2)
	for index := range strategies {
		strategies[index] = StrategyRef{StrategyID: string(rune('a' + index)), BusinessID: "2"}
	}
	named := NameRetainedHolder(RetainedHolderRef{QueryGroup: "qg"}, strategies)
	if named.StrategiesTotal != RetainedHolderStrategiesListed+2 || len(named.Strategies) != RetainedHolderStrategiesListed ||
		named.Strategies[0].StrategyID != "a" {
		t.Fatalf("named %+v of %d, want the first %d of all counted", named.Strategies, named.StrategiesTotal, RetainedHolderStrategiesListed)
	}
	strategies[0].StrategyID = "z"
	if named.Strategies[0].StrategyID != "a" {
		t.Fatal("the holder aliases the strategies it was named from")
	}
	if unseen := NameRetainedHolder(RetainedHolderRef{QueryGroup: "qg"}, nil); unseen.Strategies == nil || unseen.StrategiesTotal != 0 {
		t.Fatalf("an unseen object is named %+v, want an empty list", unseen)
	}
}
