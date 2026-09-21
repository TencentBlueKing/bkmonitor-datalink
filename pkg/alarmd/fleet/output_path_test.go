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
	"strings"
	"testing"
	"time"
)

func boolPtr(value bool) *bool { return &value }

// cappedReplica is a replica whose output sink asked its brokers and came
// away on a version below record headers, written the way the cmd writes it:
// the negotiated version, the headers verdict, the brokers' answers and the
// record_headers check carrying the sentence.
func cappedReplica(name, negotiated string) ReplicaView {
	produce := int16(2)
	return ReplicaView{Replica: name, Dependencies: []Endpoint{{
		Role: EndpointOutputKafka, ProtocolVersion: "0.11.0.0", NegotiatedVersion: negotiated, ProduceVersion: &produce,
		HeadersSupported: boolPtr(false),
		Brokers:          []BrokerProtocol{{Address: "broker-3:9092", ID: 3, Answered: true, ProduceMinVersion: 0, ProduceMaxVersion: 2}},
		Checks: []EndpointCheck{
			{Name: EndpointCheckBrokerVersion, OK: true},
			{Name: EndpointCheckRecordHeaders, Detail: "broker-3:9092 (id 3) accepts Produce v0..v2; record headers need v3 (0.11.0.0), so the client speaks " + negotiated + " and no native event can leave"},
			{Name: EndpointCheckProduceVersion, OK: true},
		},
	}}}
}

// Whether the standard raw event can leave is decided from two facts the
// fleet already had and never put together: the leader's revisioned Plan
// count and the replicas' protocol choice. Each reason is reached by exactly
// one arrangement of the two, and a source that did not report the count is
// unknown rather than no.
func TestTheOutputPathIsDecidedFromRevisionedPlansAndTheProtocolChoice(t *testing.T) {
	source := func(plans, revisioned int, known bool) *SourceFacts {
		facts := NewSourceFacts(time.Unix(1000, 0), map[string]int{"ACCEPTED": plans}, nil)
		facts.Plans, facts.RevisionedPlans, facts.PlansKnown = plans, revisioned, known
		return facts
	}
	groups := func(words ...string) []OutputProtocolGroup {
		var result []OutputProtocolGroup
		for _, word := range words {
			result = append(result, OutputProtocolGroup{Protocol: OutputProtocolFacts{Configured: word, Explicit: word != "" && word != "auto"}, Replicas: []string{"pod-" + word}})
		}
		return result
	}
	for name, test := range map[string]struct {
		view      View
		reachable bool
		reason    string
		detail    string
	}{
		"no source round at all": {
			view: View{OutputProtocols: groups("auto")}, reason: OutputPathSourceUnknown},
		"a source round from a build that did not count": {
			view: View{Source: source(100, 0, false), OutputProtocols: groups("auto")}, reason: OutputPathSourceUnknown},
		"auto with no revisioned Plans": {
			view: View{Source: source(2401, 0, true), OutputProtocols: groups("auto")}, reason: OutputPathNoRevisionedPlans},
		"auto with revisioned Plans": {
			view: View{Source: source(2401, 7, true), OutputProtocols: groups("auto")}, reachable: true, reason: OutputPathReachable},
		"every replica forced legacy, revisions or not": {
			view: View{Source: source(2401, 7, true), OutputProtocols: groups("legacy")}, reason: OutputPathProtocolLegacy},
		"legacy beside auto during a rollout": {
			view: View{Source: source(2401, 7, true), OutputProtocols: groups("legacy", "auto")}, reachable: true, reason: OutputPathReachable},
		"native with revisioned Plans": {
			view: View{Source: source(2401, 7, true), OutputProtocols: groups("native")}, reachable: true, reason: OutputPathReachable},
		"native with none refuses them all": {
			view: View{Source: source(2401, 0, true), OutputProtocols: groups("native")}, reason: OutputPathNoRevisionedPlans},
		"replicas that published no choice are left out of the decision": {
			view: View{Source: source(2401, 7, true), OutputProtocols: groups("", "legacy")}, reason: OutputPathProtocolLegacy},
		"no replica published a choice at all": {
			view: View{Source: source(2401, 7, true), OutputProtocols: groups("")}, reachable: true, reason: OutputPathReachable},
		// The brokers' answer outranks the Plans: revisioned Plans exist and
		// the client would publish them natively, but the version it came
		// away with cannot carry the header.
		"brokers cap the client below record headers, revisions or not": {
			view:   View{Source: source(2401, 7, true), OutputProtocols: groups("auto"), PerReplica: []ReplicaView{cappedReplica("pod-a", "0.10.2.0")}},
			reason: OutputPathProtocolUnsupportedByBroker, detail: "accepts Produce v0..v2"},
		"one capped replica among ones that have not asked decides it": {
			view: View{Source: source(2401, 7, true), OutputProtocols: groups("auto"),
				PerReplica: []ReplicaView{{Replica: "pod-b", Dependencies: []Endpoint{{Role: EndpointOutputKafka}}}, cappedReplica("pod-a", "0.10.2.0")}},
			reason: OutputPathProtocolUnsupportedByBroker, detail: "accepts Produce v0..v2"},
		// A configured version below the line on a build that never asked is
		// the configuration's reading, not the brokers'.
		"headers unsupported by configuration alone is not the brokers' reason": {
			view: View{Source: source(2401, 7, true), OutputProtocols: groups("auto"),
				PerReplica: []ReplicaView{{Replica: "pod-a", Dependencies: []Endpoint{{Role: EndpointOutputKafka, ProtocolVersion: "0.10.2.0", HeadersSupported: boolPtr(false)}}}}},
			reachable: true, reason: OutputPathReachable},
		"brokers that take the header change nothing": {
			view: View{Source: source(2401, 0, true), OutputProtocols: groups("auto"),
				PerReplica: []ReplicaView{{Replica: "pod-a", Dependencies: []Endpoint{{Role: EndpointOutputKafka, NegotiatedVersion: "0.11.0.0", HeadersSupported: boolPtr(true)}}}}},
			reason: OutputPathNoRevisionedPlans},
		"forced legacy everywhere is the operator's reason before the brokers'": {
			view:   View{Source: source(2401, 7, true), OutputProtocols: groups("legacy"), PerReplica: []ReplicaView{cappedReplica("pod-a", "0.10.2.0")}},
			reason: OutputPathProtocolLegacy},
	} {
		name, test := name, test
		t.Run(name, func(t *testing.T) {
			facts := OutputPathOf(&test.view)
			if facts.StandardRawEventReachable != test.reachable || facts.Reason != test.reason {
				t.Fatalf("output path = %+v, want reachable %t, %s", facts, test.reachable, test.reason)
			}
			if test.detail == "" && facts.Detail != "" || test.detail != "" && !strings.Contains(facts.Detail, test.detail) {
				t.Fatalf("output path detail = %q, want %q (the replica's own sentence about its brokers, and nothing for the other reasons)", facts.Detail, test.detail)
			}
			if test.view.Source != nil && test.view.Source.PlansKnown && (facts.Plans != test.view.Source.Plans || facts.RevisionedPlans != test.view.Source.RevisionedPlans) {
				t.Fatalf("output path carries %d/%d plans, want the source's %d/%d", facts.Plans, facts.RevisionedPlans, test.view.Source.Plans, test.view.Source.RevisionedPlans)
			}
			known := false
			for _, word := range OutputPathReasons {
				known = known || word == facts.Reason
			}
			if !known {
				t.Fatalf("reason %q is not in OutputPathReasons", facts.Reason)
			}
		})
	}
}

// The sentence is on the verdict route beside the protocol groups: on the
// live shape -- every replica auto, a source that lists thousands and
// publishes no revision -- it says the standard path is unreachable and why.
func TestTheVerdictRouteSaysWhetherTheStandardRawEventCanLeave(t *testing.T) {
	snapshots := healthySnapshots()
	for index := range snapshots {
		snapshots[index].OutputProtocol = &OutputProtocolFacts{Configured: "auto"}
	}
	facts := NewSourceFacts(now.Add(-time.Minute), map[string]int{"ACCEPTED": 2477, "CONFIG_REJECTED": 318}, nil)
	facts.Plans, facts.RevisionedPlans, facts.PlansKnown = 2477, 0, true
	snapshots[0].Source = facts
	handler := handlerWith(t, snapshots, Expectation{QueryGroups: 949, Known: true}, replicas())
	_, health := get(t, handler, "/api/health")
	path, _ := health["output_path"].(map[string]any)
	if path == nil || path["standard_raw_event_reachable"] != false || path["reason"] != OutputPathNoRevisionedPlans ||
		path["plans"] != 2477.0 || path["revisioned_plans"] != 0.0 {
		t.Fatalf("output_path = %v, want unreachable for want of revisioned Plans, 2477/0", health["output_path"])
	}
	// The same deployment once the source publishes revisions.
	facts.RevisionedPlans = 2477
	handler = handlerWith(t, snapshots, Expectation{QueryGroups: 949, Known: true}, replicas())
	_, health = get(t, handler, "/api/health")
	if path, _ := health["output_path"].(map[string]any); path["standard_raw_event_reachable"] != true || path["reason"] != OutputPathReachable {
		t.Fatalf("output_path with revisions = %v, want reachable", health["output_path"])
	}
}
