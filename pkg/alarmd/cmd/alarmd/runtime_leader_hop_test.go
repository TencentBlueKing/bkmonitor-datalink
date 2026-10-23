// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/publicsurface"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream"
)

// A Leader with no catalog -- its source failing since it started, as when
// a strategy cache lapsed on 2026-10-19 -- behind a restricted public
// surface. Discovery names the Leader itself. Its strategy read used to hop
// to its own listener and relay that listener's refusal, "moved behind the
// CLI"; it must never dial itself, and answers why it has no catalog.
func TestALeaderWithoutACatalogAnswersWhyAndNeverHopsToItself(t *testing.T) {
	var hops atomic.Int32
	var listener http.Handler
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hops.Add(1)
		listener.ServeHTTP(w, r)
	}))
	defer server.Close()
	var recorded []string
	observe := func(route, result string, _ time.Duration) { recorded = append(recorded, route+"/"+result) }
	self := scriptedLeaderDiscovery{endpoint: viewstream.LeaderEndpoint{WorkerID: "pod-leader",
		Endpoint: strings.TrimPrefix(server.URL, "http://"), InternalToken: "leader-token"}}
	noCatalog := func(string) fleet.StrategyLookupFacts { return fleet.StrategyLookupFacts{} }
	failing := func() fleet.CatalogAbsenceFacts {
		return fleet.CatalogAbsenceFacts{Known: true, Role: "leader", Exit: "active_set_missing",
			Text: "alarmd controlplane: legacy Redis source incomplete"}
	}
	native := fleet.WithStrategyStanding(http.NotFoundHandler(), nil, noCatalog,
		leaderForwarderWithin(self, "pod-leader", nil, time.Second, "strategy", observe), nil, failing, "pod-leader", time.Now, time.Minute)
	// The listener refuses everyone but a peer with the token; a hop to
	// itself carrying it would be admitted and hop again, so the only right
	// number of hops is none.
	listener = publicAPI(native, nil, forwardedPeer(func() string { return "leader-token" }))

	response := httptest.NewRecorder()
	native.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/strategies/104", nil))
	var answer fleet.CatalogAbsence
	_ = json.Unmarshal(response.Body.Bytes(), &answer)
	if hops.Load() != 0 || strings.Contains(response.Body.String(), publicsurface.RestrictedCode) {
		t.Fatalf("the Leader dialed its own listener %d times and answered %d %s", hops.Load(), response.Code, response.Body.String())
	}
	if response.Code != http.StatusServiceUnavailable || answer.Error != "NOT_PUBLISHED" || answer.Exit != "active_set_missing" {
		t.Fatalf("answered %d %s, want NOT_PUBLISHED naming the failing refresh exit", response.Code, response.Body.String())
	}
	if len(recorded) != 1 || recorded[0] != "strategy/self" {
		t.Fatalf("recorded %v, want one strategy/self", recorded)
	}
}

// A follower's hop reaches the Leader's listener, which on a restricted
// surface refuses the route to the public. The hop presents the token the
// Leader's registration carries and is answered as the Leader answers; a
// request marked as forwarded without that token, or with another, is the
// public's and refused.
func TestARestrictedLeaderAdmitsAPeersHopByItsToken(t *testing.T) {
	leaderNative := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answered_by":"pod-leader"}`))
	})
	server := httptest.NewServer(publicAPI(leaderNative, nil, forwardedPeer(func() string { return "leader-token" })))
	defer server.Close()
	endpoint := strings.TrimPrefix(server.URL, "http://")

	for _, tc := range []struct {
		name, token string
		admitted    bool
	}{
		{"the Leader's token", "leader-token", true},
		{"another token", "stale-token", false},
		{"no token", "", false},
	} {
		forward := leaderForwarderWithin(scriptedLeaderDiscovery{endpoint: viewstream.LeaderEndpoint{WorkerID: "pod-leader",
			Endpoint: endpoint, InternalToken: tc.token}}, "pod-follower", nil, time.Second, "diagnosis", nil)
		response := httptest.NewRecorder()
		forwarded, refusal := forward(response, httptest.NewRequest(http.MethodGet, "/api/diagnose?board=1", nil))
		if !forwarded || refusal != "" {
			t.Fatalf("%s: forward = (%v, %q), want the hop made", tc.name, forwarded, refusal)
		}
		admitted := response.Code == http.StatusOK && strings.Contains(response.Body.String(), `"answered_by":"pod-leader"`)
		refused := response.Code == http.StatusForbidden && strings.Contains(response.Body.String(), publicsurface.RestrictedCode)
		if admitted != tc.admitted || (!tc.admitted && !refused) {
			t.Errorf("%s: answered %d %s, want admitted=%v", tc.name, response.Code, response.Body.String(), tc.admitted)
		}
	}

	// The token alone is not a hop: without the forwarded mark the request
	// is a reader's, and a reader of a restricted surface is refused.
	request := httptest.NewRequest(http.MethodGet, "/api/diagnose?board=1", nil)
	request.Header.Set(fleet.ForwardTokenHeader(), "leader-token")
	response := httptest.NewRecorder()
	publicAPI(leaderNative, nil, forwardedPeer(func() string { return "leader-token" })).ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Errorf("a token without the forwarded mark answered %d, want refused", response.Code)
	}
	// A process without a token of its own admits nobody.
	request = httptest.NewRequest(http.MethodGet, "/api/diagnose?board=1", nil)
	request.Header.Set(fleet.ForwardedHeader(), "pod-follower")
	response = httptest.NewRecorder()
	publicAPI(leaderNative, nil, forwardedPeer(func() string { return "" })).ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Errorf("a marked hop to a process without a token answered %d, want refused", response.Code)
	}
}
