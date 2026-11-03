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
	"context"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/absentalerts"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/filterclose"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
)

// A strategy's edit close goes through the link's search, scoped to the
// tenant, this deployment's source, the strategy and active alerts, and
// excluding the current version; a link that answers 501 is counted
// filter_close_unsupported and nothing else is asked or closed.
func TestTheEditCloseTakesTheLinksSearch(t *testing.T) {
	stub := &stubControlPlane{bodies: map[string][]string{}}
	fixture := newEditFixture(t)
	fixture.loop.filter = newStubFilterRoute(t, stub)
	fixture.step()
	searches := stub.requests("/api/v1/alerts/search")
	if len(searches) == 0 {
		t.Fatal("the edit close did not search the link")
	}
	for _, field := range []string{`"bk_tenant_id":"system"`, `"event_source_id":"built_in_bk"`, `"labels.strategy_id":"10"`,
		`"status":"active"`, `"labels.strategy_version":"9"`} {
		if !strings.Contains(searches[0], field) {
			t.Fatalf("search %s does not carry %s", searches[0], field)
		}
	}
	stub.unsupported = true
	fixture.loop.clean = map[absentalerts.Key]int64{}
	fixture.step()
	if closes := stub.requests("/api/v1/alerts/close"); len(closes) != 0 ||
		fixture.loop.filter.closer.Stats()[filterclose.OutcomeUnsupported] != 1 {
		t.Fatalf("after a 501: closes %v, closer %+v", closes, fixture.loop.filter.closer.Stats())
	}
}

// An absent strategy's alerts close through the link's filter: a dry run,
// then the page's own dry run (its candidates, kept should its answer be
// lost) and the close, carrying the strategy-change command.
func TestAnAbsentStrategyClosesThroughTheLinksFilter(t *testing.T) {
	stub := &stubControlPlane{bodies: map[string][]string{}, matches: []string{"alert-1", "alert-2"}}
	bundle := &phaseTwoWorkerBundle{dependencies: phaseTwoWorkerBundleDependencies{Now: time.Now}}
	loop := newAbsentStrategyClose(bundle, nil, newStubFilterRoute(t, stub))
	key := absentalerts.Key{TenantID: "system", StrategyID: "10"}
	execution := loop.closeStrategy(context.Background(), absentalerts.Absent{Key: key}, time.Now())
	closes := stub.requests("/api/v1/alerts/close")
	if execution.word != filterclose.OutcomeJobDone || !execution.sent || len(closes) != 3 ||
		!strings.Contains(closes[0], `"dry_run":true`) || !strings.Contains(closes[1], `"dry_run":true`) ||
		!strings.Contains(closes[2], `"operation_source":"strategy_change"`) {
		t.Fatalf("word %s sent %v, requests %v", execution.word, execution.sent, closes)
	}
}

// Without the control plane's URL and secret there is no link client, no
// closer and no route: nothing reads or closes the link's alerts, and no
// other path stands in.
func TestWithoutTheControlPlaneThereIsNoFilterRoute(t *testing.T) {
	var cfg config.Config
	cfg.Redis.StatePrefix = "alarmd-test"
	cfg.PhaseTwo.Linkd.ControlPlane.URL = "http://192.0.2.10:8090"
	client, err := newLinkdManage(cfg, time.Now)
	if err != nil || client != nil {
		t.Fatalf("a control plane without its secret built a client %v (%v)", client, err)
	}
	server := redistest.Start(t)
	if server == nil {
		t.Skip("no redis-server")
	}
	closer, err := newFilterCloser(cfg, client, server.Client(), time.Now, nil, nil)
	if err != nil || closer != nil || newFilterRoute(cfg, closer) != nil {
		t.Fatalf("closer %v (%v)", closer, err)
	}
}
