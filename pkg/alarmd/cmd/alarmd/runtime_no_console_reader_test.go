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
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// The link is read through its search and closed through its close by
// filter, nothing else: no Console listing, no active set, no admin
// strategy-index chain. A deployment built the production way, against a
// link that fails the case on any other route, runs its rounds, takes an
// alert from the link and gates on it; the guard is not vacuous, because at
// least one search must have arrived.
func TestTheDeploymentReadsTheLinkOnlyThroughItsSearchAndClose(t *testing.T) {
	var (
		mu      sync.Mutex
		foreign []string
	)
	var searches atomic.Int64
	address, client := startPhaseTwoRedis(t)
	link := lifecycleLink(client)
	strict := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/alerts/search":
			searches.Add(1)
		case "/api/v1/alerts/close":
		default:
			mu.Lock()
			foreign = append(foreign, r.Method+" "+r.URL.Path)
			mu.Unlock()
		}
		link(w, r)
	}))
	t.Cleanup(strict.Close)
	strategy := lifecycleStrategy{revision: 7, noDataOff: true, statusSetter: "recovery-nodata"}
	fixture := startLifecycleFixtureOn(t, address, client, strategy, nil, func(cfg *config.Config) {
		cfg.PhaseTwo.Linkd.ControlPlane = config.LinkdControlPlaneConfig{URL: strict.URL, JWTSecret: "example-secret"}
	})

	fixture.serve(false, 90, lifecycleHostA, lifecycleHostB)
	fixture.mustAttempt(1)
	opened := fixture.thresholdEventsFor(lifecycleHostB)
	if len(opened) != 1 || opened[0].EventKind != contract.TriggerEventAbnormal {
		t.Fatalf("round 1 sent B %+v, want one ABNORMAL", opened)
	}
	fixture.publishOpenAlert(opened[0].DedupeMD5)
	// A process that did not send the alert: only the link's word gates it.
	fixture.replace("alarmd-worker-guard-2")
	fixture.serve(false, 90, lifecycleHostA)
	fixture.mustAttempt(2)
	fixture.waitOpenAlertJudged(2, opened[0].DedupeMD5, true)
	for round := int64(3); round <= 5; round++ {
		fixture.mustAttempt(round)
		fixture.passActiveCopy()
	}
	mu.Lock()
	defer mu.Unlock()
	if len(foreign) != 0 {
		t.Fatalf("the deployment asked the link outside its search and close: %v", foreign)
	}
	if searches.Load() == 0 {
		t.Fatal("no search reached the link: the guard saw nothing")
	}
}

// removedLinkReaders are the spellings of the readers R1 removed: the
// packages, the Console's configuration and routes, the active sets'
// index and the admin strategy-index chain.
var removedLinkReaders = []string{
	"alarmd/openalerts", "alarmd/linkdtoken", "console_url", "ConsoleURL",
	"/api/modules/linkd", "strategy-index", "event-sources/", "ALARMD_LINKD_CONSOLE",
	"ReadSet(", "ListActiveAlerts(",
}

// No production source of the module names a removed reader again.
func TestNoProductionSourceNamesARemovedLinkReader(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	scanned := 0
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		scanned++
		for _, spelling := range removedLinkReaders {
			if strings.Contains(string(body), spelling) {
				relative, _ := filepath.Rel(root, path)
				t.Errorf("%s names %q, a reader R1 removed", relative, spelling)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if scanned < 100 {
		t.Fatalf("scanned %d sources under %s: the walk did not reach the module", scanned, root)
	}
}
