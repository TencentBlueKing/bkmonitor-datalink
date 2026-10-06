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
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream"
)

// The control plane's answer maps onto the fleet's facts field for field:
// the publication, the Plans with their revisions and digests, every
// disposition with its scope, level, reason and field -- and the three
// booleans that tell the standings apart.
func TestTheStrategyLookupReachesTheFleetFieldForField(t *testing.T) {
	lookup := controlplane.StrategyLookup{
		Available: true, Found: true, Retained: true,
		Publication: controlplane.SnapshotPublicationRef{SnapshotRevision: "s1", PublicationEpoch: 7},
		Plans: []controlplane.StrategyPlanRef{{
			Plan:       execution.PlanIdentity{TenantID: "default", BusinessID: "2", StrategyID: "4101"},
			QueryGroup: "qg-a", ObjectDigest: "d-a", SnapshotRevision: "s1", QueryRevision: "q1", ScheduleRevision: "r1"}},
		Dispositions: []controlplane.ObjectDisposition{
			{SourceID: "4101", Scope: "PLAN", Disposition: controlplane.DispositionStaleConfig, Reason: "QUERY_CONFIG_INVALID", FieldPath: "items[0].query_configs[0]"},
			{SourceID: "4101", Scope: "LEVEL", LevelID: 2, Disposition: controlplane.DispositionAccepted}},
	}
	want := fleet.StrategyLookupFacts{
		Available: true, Found: true, Retained: true, Publication: fleet.StrategyPublication{SnapshotRevision: "s1", Epoch: 7},
		Plans: []fleet.StrategyPlanRef{{Tenant: "default", Business: "2", QueryGroup: "qg-a", ObjectDigest: "d-a",
			SnapshotRevision: "s1", QueryRevision: "q1", ScheduleRevision: "r1"}},
		Dispositions: []fleet.StrategyDisposition{
			{Scope: "PLAN", Disposition: "STALE_CONFIG", Reason: "QUERY_CONFIG_INVALID", FieldPath: "items[0].query_configs[0]"},
			{Scope: "LEVEL", LevelID: 2, Disposition: "ACCEPTED"}},
	}
	if got := strategyLookupFactsOf(lookup); !reflect.DeepEqual(got, want) {
		t.Fatalf("facts = %+v\nwant %+v", got, want)
	}
	if got := strategyLookupFactsOf(controlplane.StrategyLookup{}); got.Available || got.Found || len(got.Plans) != 0 {
		t.Fatalf("an unavailable lookup = %+v, want nothing", got)
	}
	if strategyLookupSource(nil) != nil {
		t.Fatal("a source without a reconciler answers")
	}
}

type scriptedLeaderDiscovery struct {
	endpoint viewstream.LeaderEndpoint
	miss     string
	err      error
}

func (discovery scriptedLeaderDiscovery) Leader(context.Context) (viewstream.LeaderEndpoint, string, error) {
	return discovery.endpoint, discovery.miss, discovery.err
}

// The forwarder hands the request to the Leader's listener once, marked so
// the Leader will not hand it on, with the query intact, and copies the
// Leader's status and body back as they are. Each way of having no Leader
// is the discovery's own word; a Leader that does not answer in time is
// a failed forward, not a hang.
func TestTheForwarderHandsTheRequestToTheLeaderOnceWithItsMark(t *testing.T) {
	var seen *http.Request
	leader := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		seen = request.Clone(context.Background())
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusOK)
		_, _ = response.Write([]byte(`{"standing":"WITHHELD","answered_by":"pod-leader"}`))
	}))
	defer leader.Close()
	endpoint := strings.TrimPrefix(leader.URL, "http://")
	forward := leaderForwarder(scriptedLeaderDiscovery{endpoint: viewstream.LeaderEndpoint{WorkerID: "pod-leader", Endpoint: endpoint}}, "pod-follower", nil, nil)
	response := httptest.NewRecorder()
	forwarded, refusal := forward(response, httptest.NewRequest(http.MethodGet, "/api/strategies/4101?tenant=default&business=2", nil))
	if !forwarded || refusal != "" {
		t.Fatalf("forward = (%v, %q), want forwarded", forwarded, refusal)
	}
	if seen == nil || seen.URL.Path != "/api/strategies/4101" || seen.URL.Query().Get("tenant") != "default" ||
		seen.Header.Get(fleet.ForwardedHeader()) != "pod-follower" {
		t.Fatalf("the Leader saw %v, want the same path and query, marked as forwarded by pod-follower", seen)
	}
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"answered_by":"pod-leader"`) ||
		response.Header().Get("Content-Type") != "application/json" || response.Header().Get("X-Alarmd-Answered-By") != "pod-leader" {
		t.Fatalf("relayed = %d %s %v, want the Leader's answer as it was", response.Code, response.Body.String(), response.Header())
	}
	// A refusal the Leader gives is relayed as the Leader gave it.
	refusing := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.WriteHeader(http.StatusServiceUnavailable)
		_, _ = response.Write([]byte(`{"error":"NOT_PUBLISHED"}`))
	}))
	defer refusing.Close()
	forward = leaderForwarder(scriptedLeaderDiscovery{endpoint: viewstream.LeaderEndpoint{WorkerID: "pod-leader", Endpoint: strings.TrimPrefix(refusing.URL, "http://")}}, "pod-follower", nil, nil)
	response = httptest.NewRecorder()
	if forwarded, _ := forward(response, httptest.NewRequest(http.MethodGet, "/api/strategies/4101", nil)); !forwarded || response.Code != http.StatusServiceUnavailable {
		t.Fatalf("a refusing Leader = forwarded %v code %d, want relayed as 503", forwarded, response.Code)
	}
	// No Leader, each in its own word; a discovery that failed is its own.
	for _, miss := range viewstream.DiscoveryMissReasons {
		forward = leaderForwarder(scriptedLeaderDiscovery{miss: miss}, "pod-follower", nil, nil)
		if forwarded, refusal := forward(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/strategies/4101", nil)); forwarded || refusal != miss {
			t.Errorf("miss %s: forward = (%v, %q), want not forwarded with the miss word", miss, forwarded, refusal)
		}
	}
	forward = leaderForwarder(scriptedLeaderDiscovery{err: errors.New("redis: i/o timeout")}, "pod-follower", nil, nil)
	if forwarded, refusal := forward(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/strategies/4101", nil)); forwarded || refusal != viewstream.MissDiscoveryFailed {
		t.Errorf("a failed discovery: forward = (%v, %q), want DISCOVERY_FAILED", forwarded, refusal)
	}
	// An endpoint nothing listens on: a failed forward, said as such.
	forward = leaderForwarder(scriptedLeaderDiscovery{endpoint: viewstream.LeaderEndpoint{WorkerID: "pod-gone", Endpoint: "127.0.0.1:1"}}, "pod-follower", nil, nil)
	if forwarded, refusal := forward(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/strategies/4101", nil)); forwarded || refusal != "FORWARD_FAILED" {
		t.Errorf("a Leader nothing listens on: forward = (%v, %q), want FORWARD_FAILED", forwarded, refusal)
	}
	if leaderForwarder(nil, "pod-follower", nil, nil) != nil {
		t.Fatal("a forwarder without discovery forwards")
	}
}

// Every hop is recorded under its route with what became of it: the reply
// says only FORWARD_FAILED, and a Leader that ran out the hop's bound, one
// nothing listens for and no Leader at all are different things to look at.
func TestEveryForwardIsRecordedWithWhatBecameOfIt(t *testing.T) {
	type hop struct {
		route, result string
		waited        time.Duration
	}
	var hops []hop
	observe := func(route, result string, waited time.Duration) { hops = append(hops, hop{route, result, waited}) }
	answering := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer answering.Close()
	slow := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		select {
		case <-request.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer slow.Close()
	at := func(server *httptest.Server) scriptedLeaderDiscovery {
		return scriptedLeaderDiscovery{endpoint: viewstream.LeaderEndpoint{WorkerID: "pod-leader", Endpoint: strings.TrimPrefix(server.URL, "http://")}}
	}
	for _, tc := range []struct {
		name      string
		discovery scriptedLeaderDiscovery
		want      string
	}{
		{"a Leader that answered, even with a refusal", at(answering), "answered"},
		{"a Leader slower than the bound", at(slow), "timeout"},
		{"nothing listening", scriptedLeaderDiscovery{endpoint: viewstream.LeaderEndpoint{WorkerID: "pod-gone", Endpoint: "127.0.0.1:1"}}, "refused"},
		{"no Leader", scriptedLeaderDiscovery{miss: viewstream.DiscoveryMissReasons[0]}, "no_leader"},
		{"discovery failed", scriptedLeaderDiscovery{err: errors.New("redis: i/o timeout")}, "no_leader"},
	} {
		hops = nil
		forward := leaderForwarderWithin(tc.discovery, "pod-follower", nil, 100*time.Millisecond, "diagnosis", observe)
		_, _ = forward(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/diagnose", nil))
		if len(hops) != 1 || hops[0].route != "diagnosis" || hops[0].result != tc.want {
			t.Errorf("%s: recorded %+v, want one diagnosis hop %s", tc.name, hops, tc.want)
		}
		if tc.want == "timeout" && (len(hops) != 1 || hops[0].waited < 100*time.Millisecond) {
			t.Errorf("%s: waited %+v, want at least the bound", tc.name, hops)
		}
	}
	// The reader leaving first is canceled, not a Leader that failed.
	hops = nil
	forward := leaderForwarderWithin(at(slow), "pod-follower", nil, time.Second, "diagnosis", observe)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	_, _ = forward(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/diagnose", nil).WithContext(ctx))
	if len(hops) != 1 || hops[0].result != "canceled" {
		t.Errorf("a reader that left: recorded %+v, want canceled", hops)
	}
	// The results are the metric's closed list.
	for _, result := range []string{"answered", "timeout", "refused", "error", "no_leader", "canceled"} {
		if !slices.Contains(metric.LeaderForwardResults, result) {
			t.Errorf("result %s is not in metric.LeaderForwardResults", result)
		}
	}
}
