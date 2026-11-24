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
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/activecopy"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The answer and pass words the replica publishes (the metric's help and
// the fleet's contract), spelled here rather than read from the copy.
var (
	activeCopyAnswerWords = []string{"own_sent", "search_hit", "search_miss", "young_miss", "unknown", "full"}
	activeCopyPassWords   = []string{"request", "link_busy", "link_unsupported", "read_failed", "hit", "miss", "foreign_item", "dropped"}
)

// activeCopyAgainst is the copy built the way the bundle builds it: the
// management client from the control-plane configuration, then
// newActiveCopy, against a link that answers the search with answer().
func activeCopyAgainst(t *testing.T, status int, answer func(body string) string) (*activecopy.Copy, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var bodies []string
	plane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/alerts/search" {
			t.Errorf("the copy asked %s; only the search is its route", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(body))
		mu.Unlock()
		if status != http.StatusOK {
			http.Error(w, "refused", status)
			return
		}
		_, _ = w.Write([]byte(answer(string(body))))
	}))
	t.Cleanup(plane.Close)
	var cfg config.Config
	cfg.PhaseTwo.Linkd.ControlPlane = config.LinkdControlPlaneConfig{URL: plane.URL, JWTSecret: "example-secret", EventSourceID: "source-a"}
	client, err := newLinkdManage(cfg, time.Now)
	if err != nil || client == nil {
		t.Fatalf("client %v (%v)", client, err)
	}
	copy, err := newActiveCopy(cfg, client, time.Now, nil)
	if err != nil || copy == nil {
		t.Fatalf("copy %v (%v)", copy, err)
	}
	return copy, &bodies
}

const openAlertsTestFingerprint = "0123456789abcdef0123456789abcdef"

// The replica's facts about its copy: every answer and pass word present
// from the start, zero included; the port turns the worker's Plans into the
// strategy keys the copy tracks and tells the maintenance which Plans this
// process evaluates; a lookup with no word is unknown and queued, and the
// pass that answers it reaches the facts as a request, a hit and a held
// alert. A deployment without the control plane publishes configured=false
// with the same words.
func TestActiveCopyFactsAndPortAdapter(t *testing.T) {
	none := activeCopyFactsSource(nil)()
	if none == nil || none.Configured || none.Known != 0 {
		t.Fatalf("facts without a copy = %+v", none)
	}
	for _, word := range activeCopyAnswerWords {
		if count, present := none.Answers[word]; !present || count != 0 {
			t.Fatalf("answers without a copy = %v, want %s at zero", none.Answers, word)
		}
	}
	if (openAlertCopyPort{}).Linked() {
		t.Fatal("a port without a copy says it is linked")
	}

	copy, bodies := activeCopyAgainst(t, http.StatusOK, func(string) string {
		return `{"items":[{"alert_id":"a1","bk_tenant_id":"default","event_source_id":"source-a","fingerprint":"` +
			openAlertsTestFingerprint + `","status":"active","labels":{"strategy_id":"101"}}],"next":""}`
	})
	var registered []execution.PlanIdentity
	port := openAlertCopyPort{copy: copy, registerOwned: func(_ execution.QueryGroupIdentity, plans []execution.PlanIdentity) {
		registered = append(registered, plans...)
	}}
	port.TrackPlans("qg-test", []execution.PlanIdentity{{TenantID: "default", BusinessID: "2", StrategyID: "101"}})
	if len(registered) != 1 || registered[0].StrategyID != "101" {
		t.Fatalf("registered %v, want the one Plan the worker named", registered)
	}
	facts := activeCopyFactsSource(copy)()
	if !facts.Configured || facts.Unsupported {
		t.Fatalf("facts before any pass = %+v", facts)
	}
	for _, word := range activeCopyPassWords {
		if count, present := facts.Outcomes[word]; !present || count != 0 {
			t.Fatalf("pass words before any pass = %v, want %s at zero", facts.Outcomes, word)
		}
	}
	if port.Contains("default", "101", openAlertsTestFingerprint) {
		t.Fatal("a fingerprint with no word passed the gate")
	}
	copy.Pass(context.Background())
	if len(*bodies) != 1 || !strings.Contains((*bodies)[0], openAlertsTestFingerprint) || !strings.Contains((*bodies)[0], `"source-a"`) {
		t.Fatalf("searches %v, want one naming the queued fingerprint and the event source", *bodies)
	}
	if !port.Contains("default", "101", openAlertsTestFingerprint) {
		t.Fatal("the alert the search found does not pass the gate")
	}
	facts = activeCopyFactsSource(copy)()
	if facts.Answers["unknown"] != 1 || facts.Answers["search_hit"] != 1 || facts.Outcomes["request"] != 1 || facts.Outcomes["hit"] != 1 || facts.Known != 1 {
		t.Fatalf("facts after the pass = %+v", facts)
	}
}

// A link that cannot search (501) reaches the facts as unsupported, the
// fleet's ACTIVE_COPY_UNSUPPORTED source, and nothing is judged.
func TestAnUnsupportedLinkReachesTheCopyFacts(t *testing.T) {
	copy, _ := activeCopyAgainst(t, http.StatusNotImplemented, nil)
	port := openAlertCopyPort{copy: copy}
	port.TrackPlans("qg-test", []execution.PlanIdentity{{TenantID: "default", StrategyID: "101"}})
	_ = port.Contains("default", "101", openAlertsTestFingerprint)
	copy.Pass(context.Background())
	facts := activeCopyFactsSource(copy)()
	if !facts.Unsupported || facts.Outcomes["link_unsupported"] != 1 {
		t.Fatalf("facts after a 501 = %+v", facts)
	}
	if held, judged := port.Holds("default", "101", openAlertsTestFingerprint); held || judged {
		t.Fatalf("held %v judged %v after a 501, want nothing judged", held, judged)
	}
}
