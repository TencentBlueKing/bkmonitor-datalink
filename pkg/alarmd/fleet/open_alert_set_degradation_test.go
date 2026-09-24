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
	"encoding/json"
	"strings"
	"testing"
)

// A replica whose copy of the consumer's open alert set has been on its own
// past the staleness bound degrades the verdict by itself: no object is
// anomalous, and yet every recovery that replica holds on its own knowledge
// is an alert kept open past its due. The other states of the copy degrade
// nothing -- a copy that never loaded reads as a publisher not deployed, and
// a self-maintained copy inside its bound is the exposure the design
// accepts -- and a build without the gate publishes no facts at all, which
// is kept apart from a copy that is fine.
func TestAStaleOpenAlertSetDegradesTheVerdictOnItsOwn(t *testing.T) {
	for _, arm := range []struct {
		name  string
		facts *OpenAlertSetFacts
		want  Health
	}{
		{name: "no facts (a build without the gate)", facts: nil, want: HealthHealthy},
		{name: "never loaded", facts: &OpenAlertSetFacts{Mode: "never_loaded"}, want: HealthHealthy},
		{name: "self-maintained inside the bound", facts: &OpenAlertSetFacts{Mode: "self_maintained"}, want: HealthHealthy},
		{name: "self-maintained past the bound", facts: &OpenAlertSetFacts{Mode: "self_maintained", StaleBeyondBound: true}, want: HealthDegraded},
	} {
		t.Run(arm.name, func(t *testing.T) {
			snapshots := healthySnapshots()
			snapshots[1].OpenAlertSet = arm.facts
			view := Aggregate(Expectation{QueryGroups: 949, Known: true}, snapshots, replicas(), now, freshness)
			if view.Health != arm.want {
				t.Fatalf("health = %s, want %s (degradations %+v)", view.Health, arm.want, view.Degradations)
			}
			if arm.want == HealthDegraded {
				if len(view.Degradations) != 1 || view.Degradations[0] != (Degradation{Kind: DegradationOpenAlertSetStale, Replica: "pod-b"}) {
					t.Fatalf("degradations = %+v, want the stale copy named with its replica", view.Degradations)
				}
				if len(view.Anomalies) != 0 {
					t.Fatal("a replica-level degradation is not an object anomaly")
				}
			} else if len(view.Degradations) != 0 {
				t.Fatalf("degradations = %+v, want none", view.Degradations)
			}
		})
	}
}

// An incomplete view stays UNKNOWN even with a stale copy: the anomalies it
// does show are not the whole story, and neither is this.
func TestAStaleOpenAlertSetDoesNotOutrankAGap(t *testing.T) {
	snapshots := healthySnapshots()
	snapshots[1].OpenAlertSet = &OpenAlertSetFacts{Mode: "self_maintained", StaleBeyondBound: true}
	view := Aggregate(Expectation{QueryGroups: 949, Known: true}, snapshots[:1], replicas(), now, freshness)
	if view.Health != HealthUnknown {
		t.Fatalf("health = %s, want UNKNOWN with a replica missing", view.Health)
	}
}

// The facts and the degradation reach the page's JSON under their own names,
// and an absent age is absent rather than zero.
func TestOpenAlertSetFactsEncodeWithoutInventingAnAge(t *testing.T) {
	encoded, err := json.Marshal(Snapshot{Replica: "pod-a", OpenAlertSet: &OpenAlertSetFacts{Mode: "never_loaded"}})
	if err != nil {
		t.Fatal(err)
	}
	// The mode and the stale flag under their names; the ages absent, not
	// zero; the counts present at zero, since a count of nothing is an
	// answer and an absent count is not; whether calibration is configured
	// present at false, since "off" is a reading a deployment has to be
	// able to make.
	if !strings.Contains(string(encoded), `"open_alert_set":{"calibration_configured":false,"mode":"never_loaded","stale_beyond_bound":false,"available":false,"reader_fingerprint_version":"","tracked_sets":0,"loaded_sets":0,"members":0,"sent_in_set":0,"sent_not_in_set":0,"disjoint":false}`) ||
		strings.Contains(string(encoded), "authoritative_age_seconds") || strings.Contains(string(encoded), "heartbeat_age_seconds") {
		t.Fatalf("encoded = %s", encoded)
	}
	view := Aggregate(Expectation{QueryGroups: 949, Known: true}, func() []Snapshot {
		s := healthySnapshots()
		s[0].OpenAlertSet = &OpenAlertSetFacts{Mode: "self_maintained", StaleBeyondBound: true}
		return s
	}(), replicas(), now, freshness)
	encoded, err = json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"degradations":[{"kind":"OPEN_ALERT_SET_STALE","replica":"pod-a"}]`) {
		t.Fatalf("encoded view lacks the degradation: %s", encoded)
	}
}

// A replica whose sets carry none of its own alerts degrades the verdict
// under its own kind and names the replica: the gate there is answering
// from what that replica sent, and recoveries for series it did not send
// are still held. Sent counts alone, without the state, degrade nothing.
func TestSetsCarryingNoneOfOurAlertsDegradeTheVerdictByName(t *testing.T) {
	for _, arm := range []struct {
		name  string
		facts *OpenAlertSetFacts
		want  Health
	}{
		{name: "some of ours missing, one found", facts: &OpenAlertSetFacts{Mode: "self_maintained", SentInSet: 1, SentNotInSet: 5}, want: HealthHealthy},
		{name: "disjoint", facts: &OpenAlertSetFacts{Mode: "self_maintained", SentNotInSet: 103, Disjoint: true}, want: HealthDegraded},
	} {
		t.Run(arm.name, func(t *testing.T) {
			snapshots := healthySnapshots()
			snapshots[1].OpenAlertSet = arm.facts
			view := Aggregate(Expectation{QueryGroups: 949, Known: true}, snapshots, replicas(), now, freshness)
			if view.Health != arm.want {
				t.Fatalf("health = %s, want %s (degradations %+v)", view.Health, arm.want, view.Degradations)
			}
			if arm.want == HealthDegraded {
				if len(view.Degradations) != 1 || view.Degradations[0] != (Degradation{Kind: DegradationOpenAlertSetDisjoint, Replica: "pod-b"}) {
					t.Fatalf("degradations = %+v, want OPEN_ALERT_SET_DISJOINT on pod-b", view.Degradations)
				}
			} else if len(view.Degradations) != 0 {
				t.Fatalf("degradations = %+v, want none", view.Degradations)
			}
		})
	}
}

// A Leader failing to publish, or whose view reaches nobody, past the bound
// degrades the verdict by name and replica; a follower's facts and a Leader
// inside the bound degrade nothing.
func TestAViewThatCannotBePublishedOrReachesNobodyDegradesTheVerdict(t *testing.T) {
	for _, arm := range []struct {
		name  string
		facts *ViewStreamFacts
		want  []Degradation
	}{
		{name: "healthy leader", facts: &ViewStreamFacts{Leading: true}},
		{name: "failing inside the bound", facts: &ViewStreamFacts{Leading: true, PublishFailures: 3}},
		{name: "failing past the bound", facts: &ViewStreamFacts{Leading: true, PublishFailures: 20, PublishFailureReason: "activation_unreadable", PublishFailingBeyondBound: true},
			want: []Degradation{{Kind: DegradationViewPublishFailing, Replica: "pod-b", Text: "activation_unreadable"}}},
		{name: "nobody connected past the bound", facts: &ViewStreamFacts{Leading: true, NoSessionsBeyondBound: true},
			want: []Degradation{{Kind: DegradationViewStreamNoSessions, Replica: "pod-b"}}},
		{name: "a follower's leftovers", facts: &ViewStreamFacts{PublishFailingBeyondBound: true, NoSessionsBeyondBound: true}},
	} {
		t.Run(arm.name, func(t *testing.T) {
			snapshots := healthySnapshots()
			snapshots[1].ViewStream = arm.facts
			view := Aggregate(Expectation{QueryGroups: 949, Known: true}, snapshots, replicas(), now, freshness)
			var got []Degradation
			for _, degradation := range view.Degradations {
				if degradation.Kind == DegradationViewPublishFailing || degradation.Kind == DegradationViewStreamNoSessions {
					got = append(got, degradation)
				}
			}
			if len(got) != len(arm.want) || (len(got) == 1 && got[0] != arm.want[0]) {
				t.Fatalf("degradations = %+v, want %+v", got, arm.want)
			}
		})
	}
}
