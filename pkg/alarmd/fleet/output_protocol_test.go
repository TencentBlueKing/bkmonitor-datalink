// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package fleet

import (
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// Each replica's protocol choice rides on its own row and the view groups the
// replicas by it, the way it groups builds: one group is a deployment that
// agrees with itself, two is a rollout or a values file that changed under
// one replica. A replica that published no choice is its own group with an
// empty word, never folded into a word it may not be running -- and never
// synthesized into one deployment-level protocol.
func TestEachReplicaCarriesItsOwnOutputProtocolAndTheViewGroupsThem(t *testing.T) {
	snapshots := idleSnapshots()
	snapshots[0].TakenAt = now.Add(-5 * time.Second)
	// pod-a never chose: the process fell back to auto.
	snapshots[0].OutputProtocol = &OutputProtocolFacts{Configured: "auto"}
	// pod-b was told native, in so many words.
	snapshots[1].OutputProtocol = &OutputProtocolFacts{Configured: "native", Explicit: true}
	view := Aggregate(Expectation{QueryGroups: 0, Known: true}, snapshots, replicas(), now, freshness)
	rows := map[string]ReplicaView{}
	for _, row := range view.PerReplica {
		rows[row.Replica] = row
	}
	if a := rows["pod-a"].OutputProtocol; a == nil || a.Configured != "auto" || a.Explicit {
		t.Errorf("pod-a row protocol = %+v, want auto, not explicit", a)
	}
	if b := rows["pod-b"].OutputProtocol; b == nil || b.Configured != "native" || !b.Explicit {
		t.Errorf("pod-b row protocol = %+v, want native, explicit", b)
	}
	if len(view.OutputProtocols) != 2 {
		t.Fatalf("protocol groups = %+v, want two: the replicas disagree and no single word is made of them", view.OutputProtocols)
	}
	for _, group := range view.OutputProtocols {
		if len(group.Replicas) != 1 {
			t.Errorf("group %+v holds %d replicas, want 1", group.Protocol, len(group.Replicas))
		}
	}
	// The row's facts are a copy: a later change to the snapshot's does not
	// reach the row.
	snapshots[1].OutputProtocol.Configured = "changed"
	if rows["pod-b"].OutputProtocol.Configured == "changed" {
		t.Error("the per-replica row aliases the snapshot's protocol facts")
	}

	// Agreement is one group naming both replicas, most replicas first when
	// there is another.
	agreeing := idleSnapshots()
	agreeing[0].OutputProtocol = &OutputProtocolFacts{Configured: "auto"}
	agreeing[1].OutputProtocol = &OutputProtocolFacts{Configured: "auto"}
	view = Aggregate(Expectation{QueryGroups: 0, Known: true}, agreeing, replicas(), now, freshness)
	if len(view.OutputProtocols) != 1 || len(view.OutputProtocols[0].Replicas) != 2 || view.OutputProtocols[0].Protocol.Configured != "auto" {
		t.Fatalf("protocol groups = %+v, want one group of two on auto", view.OutputProtocols)
	}

	// An older build publishes no choice: no row fact, and its own group with
	// an empty word beside the one that did say.
	older := idleSnapshots()
	older[0].OutputProtocol = &OutputProtocolFacts{Configured: "auto"}
	view = Aggregate(Expectation{QueryGroups: 0, Known: true}, older, replicas(), now, freshness)
	for _, row := range view.PerReplica {
		if row.Replica == "pod-b" && row.OutputProtocol != nil {
			t.Errorf("pod-b published no protocol and its row carries %+v", row.OutputProtocol)
		}
	}
	if len(view.OutputProtocols) != 2 {
		t.Fatalf("protocol groups = %+v, want auto beside an empty-word group for the replica that said nothing", view.OutputProtocols)
	}
	var unreported *OutputProtocolGroup
	for index := range view.OutputProtocols {
		if view.OutputProtocols[index].Protocol == (OutputProtocolFacts{}) {
			unreported = &view.OutputProtocols[index]
		}
	}
	if unreported == nil || len(unreported.Replicas) != 1 || unreported.Replicas[0] != "pod-b" {
		t.Fatalf("protocol groups = %+v, want pod-b alone under the empty word", view.OutputProtocols)
	}
}

// The verdict route carries the groups, as a list even when empty, and the
// row facts; the words on it are the control plane's three and no others, so
// a reader pinned to controlplane.OutputProtocolChoices reads every word a
// current build can publish.
func TestTheVerdictRouteCarriesTheOutputProtocolGroups(t *testing.T) {
	snapshots := healthySnapshots()
	snapshots[0].OutputProtocol = &OutputProtocolFacts{Configured: "auto"}
	snapshots[1].OutputProtocol = &OutputProtocolFacts{Configured: "auto"}
	handler := handlerWith(t, snapshots, Expectation{QueryGroups: 949, Known: true}, replicas())
	_, health := get(t, handler, "/api/health")
	groups, ok := health["output_protocols"].([]any)
	if !ok || len(groups) != 1 {
		t.Fatalf("output_protocols = %v, want one group", health["output_protocols"])
	}
	group := groups[0].(map[string]any)
	protocol, _ := group["protocol"].(map[string]any)
	if protocol["configured"] != "auto" || protocol["explicit"] != false {
		t.Errorf("group protocol = %v, want auto, explicit false", protocol)
	}
	if members, _ := group["replicas"].([]any); len(members) != 2 {
		t.Errorf("group replicas = %v, want both", group["replicas"])
	}
	known := map[string]bool{}
	for _, word := range controlplane.OutputProtocolChoices {
		known[word] = true
	}
	if !known[protocol["configured"].(string)] {
		t.Errorf("configured = %q is not one of %v", protocol["configured"], controlplane.OutputProtocolChoices)
	}
	rows, _ := health["per_replica"].([]any)
	for _, item := range rows {
		row := item.(map[string]any)
		if facts, _ := row["output_protocol"].(map[string]any); facts == nil || facts["configured"] != "auto" {
			t.Errorf("%v row output_protocol = %v, want auto", row["replica"], row["output_protocol"])
		}
	}

	// No counted replica: an empty list, not null.
	handler = handlerWith(t, nil, Expectation{QueryGroups: 949, Known: true}, replicas())
	_, health = get(t, handler, "/api/health")
	if groups, ok := health["output_protocols"].([]any); !ok || len(groups) != 0 {
		t.Fatalf("output_protocols with no replicas = %v, want []", health["output_protocols"])
	}
}
