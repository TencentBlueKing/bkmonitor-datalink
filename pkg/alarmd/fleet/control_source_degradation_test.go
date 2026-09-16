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

// A control source that has not refreshed successfully past the staleness
// bound degrades the verdict by itself: every object on the list keeps
// running from the last good catalog, and the strategies saved since are on
// no list at all. A replica that cannot acquire the leader lease and cannot
// learn who holds it, past the same bound, degrades it too: nobody may be
// refreshing, which every refresh counter reads as somebody else doing it.
// A round that is failing inside the bound, and a replica whose facts a
// build before this did not publish, degrade nothing.
func TestAStaleControlSourceDegradesTheVerdictOnItsOwn(t *testing.T) {
	seconds := func(value float64) *float64 { return &value }
	for _, arm := range []struct {
		name  string
		facts *ControlSourceFacts
		want  Health
		kinds []DegradationKind
	}{
		{name: "no facts (a build before them)", facts: nil, want: HealthHealthy},
		{name: "leader healthy", facts: &ControlSourceFacts{Role: "leader", Mode: "healthy", LastSuccessAgeSeconds: seconds(12)}, want: HealthHealthy},
		{name: "leader failing inside the bound", facts: &ControlSourceFacts{Role: "leader", Mode: "degraded_last_good",
			LastSuccessAgeSeconds: seconds(90), LastFailureExit: "active_set_invalid_id"}, want: HealthHealthy},
		{name: "leader failing past the bound", facts: &ControlSourceFacts{Role: "leader", Mode: "degraded_last_good",
			LastSuccessAgeSeconds: seconds(3600), StaleBeyondBound: true, LastFailureExit: "active_set_invalid_id"},
			want: HealthDegraded, kinds: []DegradationKind{DegradationControlSourceStale}},
		{name: "leader that never succeeded past the bound", facts: &ControlSourceFacts{Role: "leader", Mode: "never_succeeded",
			DegradedSecondsThisProcess: seconds(600), StaleBeyondBound: true}, want: HealthDegraded,
			kinds: []DegradationKind{DegradationControlSourceStale}},
		{name: "follower reading the same persisted staleness", facts: &ControlSourceFacts{Role: "follower", Mode: "healthy",
			LastSuccessAgeSeconds: seconds(3600), StaleBeyondBound: true}, want: HealthDegraded,
			kinds: []DegradationKind{DegradationControlSourceStale}},
		{name: "lease unacquired past the bound", facts: &ControlSourceFacts{Role: "unacquired", Mode: "healthy",
			LeaderAbsentBeyondBound: true}, want: HealthDegraded, kinds: []DegradationKind{DegradationControlLeaderAbsent}},
		{name: "both at once", facts: &ControlSourceFacts{Role: "unacquired", Mode: "never_succeeded",
			StaleBeyondBound: true, LeaderAbsentBeyondBound: true}, want: HealthDegraded,
			kinds: []DegradationKind{DegradationControlSourceStale, DegradationControlLeaderAbsent}},
	} {
		t.Run(arm.name, func(t *testing.T) {
			snapshots := healthySnapshots()
			snapshots[0].ControlSource = arm.facts
			view := Aggregate(Expectation{QueryGroups: 949, Known: true}, snapshots, replicas(), now, freshness)
			if view.Health != arm.want {
				t.Fatalf("health = %s, want %s (degradations %+v)", view.Health, arm.want, view.Degradations)
			}
			if len(view.Degradations) != len(arm.kinds) {
				t.Fatalf("degradations = %+v, want kinds %v", view.Degradations, arm.kinds)
			}
			for index, kind := range arm.kinds {
				got := view.Degradations[index]
				if got.Kind != kind || got.Replica != "pod-a" {
					t.Fatalf("degradation %d = %+v, want %s on pod-a", index, got, kind)
				}
				// The stale source's standing carries where its last round
				// stopped and what it said, as the replica has them -- a
				// follower reading persisted staleness has neither -- so the
				// line can name the failure and not only the bound.
				if kind == DegradationControlSourceStale && (got.Stage != arm.facts.LastFailureExit || got.Text != arm.facts.LastFailure) {
					t.Fatalf("degradation %d = %+v, want the source's last failure exit %q and text %q on it",
						index, got, arm.facts.LastFailureExit, arm.facts.LastFailure)
				}
			}
			if len(view.Anomalies) != 0 {
				t.Fatal("a replica-level degradation is not an object anomaly")
			}
		})
	}
}

// The facts travel on the encoded snapshot under the names the page reads,
// and the two ages are absent, not zero, when unknown.
func TestControlSourceFactsEncodeAbsentAgesAsAbsent(t *testing.T) {
	encoded, err := json.Marshal(Snapshot{ControlSource: &ControlSourceFacts{Role: "leader", Mode: "never_succeeded", StaleBeyondBound: true,
		LastFailureExit: "active_set_invalid_id", LastFailure: "element 3 of 979 is \"x\""}})
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, want := range []string{`"control_source":{`, `"role":"leader"`, `"mode":"never_succeeded"`, `"stale_beyond_bound":true`,
		`"leader_absent_beyond_bound":false`, `"last_failure_exit":"active_set_invalid_id"`, `"last_failure":"element 3 of 979 is \"x\""`} {
		if !strings.Contains(text, want) {
			t.Fatalf("encoded snapshot %s lacks %s", text, want)
		}
	}
	for _, absent := range []string{"last_success_age_seconds", "degraded_seconds_this_process"} {
		if strings.Contains(text, absent) {
			t.Fatalf("encoded snapshot %s carries %s for an unknown age; a zero here would read as just now", text, absent)
		}
	}
}

// A copy of the platform's settings that has been without the platform's
// publication past the bound degrades the verdict by itself; the other
// states, including a deployment that renders no source, degrade nothing.
func TestAStalePlatformSettingsCopyDegradesTheVerdictOnItsOwn(t *testing.T) {
	for _, arm := range []struct {
		name  string
		facts *PlatformSettingsFacts
		want  Health
	}{
		{name: "no facts (a build before them)", facts: nil, want: HealthHealthy},
		{name: "not configured", facts: &PlatformSettingsFacts{Mode: "not_configured"}, want: HealthHealthy},
		{name: "never loaded", facts: &PlatformSettingsFacts{Mode: "never_loaded"}, want: HealthHealthy},
		{name: "stale inside the bound", facts: &PlatformSettingsFacts{Mode: "stale", LastUnavailable: "connection refused"}, want: HealthHealthy},
		{name: "stale past the bound", facts: &PlatformSettingsFacts{Mode: "stale", StaleBeyondBound: true}, want: HealthDegraded},
	} {
		t.Run(arm.name, func(t *testing.T) {
			snapshots := healthySnapshots()
			snapshots[1].PlatformSettings = arm.facts
			view := Aggregate(Expectation{QueryGroups: 949, Known: true}, snapshots, replicas(), now, freshness)
			if view.Health != arm.want {
				t.Fatalf("health = %s, want %s (degradations %+v)", view.Health, arm.want, view.Degradations)
			}
			if arm.want == HealthDegraded {
				if len(view.Degradations) != 1 || view.Degradations[0] != (Degradation{Kind: DegradationPlatformSettingsStale, Replica: "pod-b"}) {
					t.Fatalf("degradations = %+v, want the stale copy named with its replica", view.Degradations)
				}
			} else if len(view.Degradations) != 0 {
				t.Fatalf("degradations = %+v, want none", view.Degradations)
			}
		})
	}
}
