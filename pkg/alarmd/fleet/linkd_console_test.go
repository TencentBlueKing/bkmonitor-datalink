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

func consoleEntry(state, reason string) []Endpoint {
	return []Endpoint{{Role: EndpointLinkdConsole, Kind: "http", Configured: state != LinkdConsoleNotConfigured,
		Console: &LinkdConsoleFacts{State: state, Reason: reason}}}
}

func sourceWithStandardPlans(at time.Time, standard int) *SourceFacts {
	facts := NewSourceFacts(at, map[string]int{"ACCEPTED": 5}, nil)
	facts.Plans, facts.PlansKnown, facts.StandardPlans = 5, true, standard
	return facts
}

// The Console matters where events go to the alert link. A deployment whose
// Plans publish the standard raw event and has no Console cannot close those
// alerts once their strategy goes, and the first screen says so; one whose
// events all go the Python-compatible way hands its alerts to the Python
// backend, and the same unconfigured Console is its design -- listed in the
// dependency table, never on the first screen.
func TestTheConsoleNeedsAttentionOnlyWhereEventsGoToTheLink(t *testing.T) {
	cases := []struct {
		name      string
		standard  int
		state     string
		attention bool
	}{
		{"events go to the link, no Console", 46, LinkdConsoleNotConfigured, true},
		{"events go to the link, Console refused", 46, LinkdConsoleUnreadable, true},
		{"events go to the link, link behind", 46, LinkdConsoleLinkUnhealthy, true},
		{"events go to the link, Console answers", 46, LinkdConsoleReachable, false},
		{"events go to the link, not called yet", 46, LinkdConsoleNotCalled, false},
		{"every event the Python-compatible way, no Console", 0, LinkdConsoleNotConfigured, false},
	}
	for _, c := range cases {
		snapshots := []Snapshot{{Replica: "pod-a", TakenAt: now.Add(-10 * time.Second), Owned: 5, Determined: 5,
			Source: sourceWithStandardPlans(now.Add(-time.Minute), c.standard), Dependencies: consoleEntry(c.state, "")}}
		view := Aggregate(Expectation{QueryGroups: 5, Known: true}, snapshots, []string{"pod-a"}, now, freshness)
		standing := view.LinkdConsole
		if standing == nil || standing.Attention != c.attention || standing.Needed != (c.standard > 0) ||
			standing.StandardPlans != c.standard || standing.State != c.state || standing.Replica != "pod-a" {
			t.Errorf("%s: standing = %+v, want attention %v", c.name, standing, c.attention)
		}
	}
}

// Only the leader walks the roster, so only its entry can say the link is
// behind. When a follower published last, its entry -- reachable on the
// calibration alone -- must not stand for the deployment's.
func TestTheLeadersConsoleEntryStandsForTheDeployment(t *testing.T) {
	snapshots := []Snapshot{
		{Replica: "pod-leader", TakenAt: now.Add(-20 * time.Second), Owned: 3, Determined: 3,
			Source: sourceWithStandardPlans(now.Add(-time.Minute), 46), Dependencies: consoleEntry(LinkdConsoleLinkUnhealthy, "discovery_stale")},
		{Replica: "pod-follower", TakenAt: now.Add(-5 * time.Second), Owned: 2, Determined: 2,
			Dependencies: consoleEntry(LinkdConsoleReachable, "")},
	}
	view := Aggregate(Expectation{QueryGroups: 5, Known: true}, snapshots, []string{"pod-leader", "pod-follower"}, now, freshness)
	if view.DependenciesReplica != "pod-follower" {
		t.Fatalf("the fixture needs the follower to have published last; dependencies from %q", view.DependenciesReplica)
	}
	standing := view.LinkdConsole
	if standing == nil || standing.State != LinkdConsoleLinkUnhealthy || standing.Reason != "discovery_stale" ||
		standing.Replica != "pod-leader" || !standing.Attention {
		t.Fatalf("standing = %+v, want the leader's link_unhealthy", standing)
	}
}

// Without a leader round that counted Plans, or without a Console entry --
// an older build's -- there is no answer, and nothing is said.
func TestNoConsoleStandingWithoutBothHalves(t *testing.T) {
	older := NewSourceFacts(now.Add(-time.Minute), map[string]int{"ACCEPTED": 5}, nil)
	for name, snapshot := range map[string]Snapshot{
		"no plan count":    {Replica: "pod-a", TakenAt: now, Source: older, Dependencies: consoleEntry(LinkdConsoleNotConfigured, "")},
		"no console entry": {Replica: "pod-a", TakenAt: now, Source: sourceWithStandardPlans(now, 46)},
		"no source at all": {Replica: "pod-a", TakenAt: now, Dependencies: consoleEntry(LinkdConsoleNotConfigured, "")},
	} {
		view := Aggregate(Expectation{QueryGroups: 1, Known: true}, []Snapshot{snapshot}, []string{"pod-a"}, now, freshness)
		if view.LinkdConsole != nil {
			t.Errorf("%s: standing = %+v, want none", name, view.LinkdConsole)
		}
	}
}

// The standing reaches /api/health: the response is built field by field,
// and a field the view decides and the response does not copy renders as
// nothing on the page.
func TestTheConsoleStandingReachesTheHealthResponse(t *testing.T) {
	snapshots := []Snapshot{{Replica: "pod-a", TakenAt: now.Add(-10 * time.Second), Owned: 5, Determined: 5,
		Source: sourceWithStandardPlans(now.Add(-time.Minute), 46), Dependencies: consoleEntry(LinkdConsoleNotConfigured, "")}}
	body := requestJSON(t, handlerWith(t, snapshots, Expectation{QueryGroups: 5, Known: true}, []string{"pod-a"}), "/api/health")
	standing, ok := body["linkd_console"].(map[string]any)
	if !ok || standing["attention"] != true || standing["state"] != LinkdConsoleNotConfigured || standing["standard_plans"] != float64(46) {
		t.Fatalf("/api/health linkd_console = %#v", body["linkd_console"])
	}
}
