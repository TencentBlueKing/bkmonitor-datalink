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
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// stubDirectory answers every page with snapshot. With entered set, the
// first page says it has started and waits for release before answering;
// every later page answers at once, so one let in beside it is answered
// rather than held.
type stubDirectory struct {
	available bool
	snapshot  controlplane.StrategyDirectorySnapshot
	// catalog is the published catalog's strategies, paged as given.
	catalog         []controlplane.StrategyCatalogEntry
	catalogRevision string
	entered         chan struct{}
	release         chan struct{}
	pages           atomic.Int32
	// resolvable makes ResolveCurrent find the strategy; the first
	// EffectivePlan then waits on planRelease after saying planEntered.
	resolvable  bool
	planEntered chan struct{}
	planRelease chan struct{}
	plans       atomic.Int32
}

func (d *stubDirectory) Available() bool { return d.available }

func (d *stubDirectory) Page(context.Context, time.Time, string, string, string, int, int) controlplane.StrategyDirectorySnapshot {
	if d.pages.Add(1) == 1 && d.entered != nil {
		d.entered <- struct{}{}
		<-d.release
	}
	return d.snapshot
}

func (d *stubDirectory) ResolveCurrent(context.Context, time.Time, string, string, string, string, ...string) (controlplane.StrategyDirectoryRow, error) {
	if d.resolvable {
		return controlplane.StrategyDirectoryRow{}, nil
	}
	return controlplane.StrategyDirectoryRow{}, controlplane.ErrSnapshotUnavailable
}

func (d *stubDirectory) EffectivePlan(context.Context, controlplane.StrategyDirectoryRow) (controlplane.QueryGroupPlanObject, error) {
	if d.plans.Add(1) == 1 && d.planEntered != nil {
		d.planEntered <- struct{}{}
		<-d.planRelease
	}
	return controlplane.QueryGroupPlanObject{}, controlplane.ErrCatalogObjectUnavailable
}

func (d *stubDirectory) EffectiveOutput(context.Context, controlplane.StrategyDirectoryRow) controlplane.OutputFormatFacts {
	return controlplane.OutputFormatFacts{}
}

func (d *stubDirectory) StrategyCatalog(offset, limit int) controlplane.StrategyCatalogPage {
	if d.catalogRevision == "" {
		return controlplane.StrategyCatalogPage{Reason: "LEADER_CATALOG_NOT_READY", Strategies: []controlplane.StrategyCatalogEntry{}}
	}
	page := controlplane.StrategyCatalogPage{Revision: d.catalogRevision, Total: len(d.catalog), Strategies: []controlplane.StrategyCatalogEntry{}}
	for index := offset; index < len(d.catalog) && len(page.Strategies) < limit; index++ {
		page.Strategies = append(page.Strategies, d.catalog[index])
	}
	return page
}

// The catalog is read page by page with an opaque cursor until there is no
// next one, every strategy once; a cursor from a publication that has since
// been replaced is refused as stale rather than read against the new one.
func TestTheStrategyCatalogPagesByCursor(t *testing.T) {
	directory := &stubDirectory{available: true, catalogRevision: "rev-1:3"}
	for _, id := range []string{"101", "102", "103", "104", "105"} {
		directory.catalog = append(directory.catalog, controlplane.StrategyCatalogEntry{StrategyID: id, Tenant: "t", Business: "2"})
	}
	handler := WithStrategyDirectory(http.NotFoundHandler(), directory, nil, nil, "pod-a", time.Now)
	type page struct {
		controlplane.StrategyCatalogPage
		NextCursor string `json:"next_cursor"`
	}
	read := func(query string) (int, page) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/objects?scope=strategy_catalog"+query, nil))
		var body page
		_ = json.Unmarshal(recorder.Body.Bytes(), &body)
		return recorder.Code, body
	}
	var listed []string
	cursor, first := "", ""
	for pages := 1; ; pages++ {
		code, body := read("&limit=2&cursor=" + cursor)
		if code != http.StatusOK || body.Total != 5 || len(body.Strategies) > 2 {
			t.Fatalf("page %d = %d %+v", pages, code, body)
		}
		for _, entry := range body.Strategies {
			listed = append(listed, entry.StrategyID)
		}
		if pages == 1 {
			first = body.NextCursor
		}
		if body.NextCursor == "" {
			if pages != 3 {
				t.Fatalf("ended after %d pages, want 3", pages)
			}
			break
		}
		cursor = body.NextCursor
	}
	if fmt.Sprint(listed) != "[101 102 103 104 105]" {
		t.Fatalf("listed %v, want every strategy once in order", listed)
	}
	directory.catalogRevision = "rev-2:1"
	if code, _ := read("&limit=2&cursor=" + first); code != http.StatusConflict {
		t.Fatalf("a cursor from a replaced publication = %d, want 409", code)
	}
	if code, _ := read("&cursor=not-a-cursor"); code != http.StatusBadRequest {
		t.Fatalf("a cursor that does not decode = %d, want 400", code)
	}
	if code, _ := read("&limit=201"); code != http.StatusBadRequest {
		t.Fatalf("a limit over 200 = %d, want 400", code)
	}
	directory.catalogRevision = ""
	if code, body := read(""); code != http.StatusServiceUnavailable || body.Reason != "LEADER_CATALOG_NOT_READY" {
		t.Fatalf("no catalog = %d %+v, want 503 not ready", code, body)
	}
}

// Directory reads are an operator's, a handful next to the business work:
// a second request while a page is being built is answered too, not refused
// as busy.
func TestDirectoryPagesAreBuiltSideBySide(t *testing.T) {
	directory := &stubDirectory{available: true, snapshot: controlplane.StrategyDirectorySnapshot{Complete: true, Revision: "r"},
		entered: make(chan struct{}), release: make(chan struct{})}
	handler := WithStrategyDirectory(http.NotFoundHandler(), directory, nil, nil, "pod-a", time.Now)
	first := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/api/objects?scope=strategies", nil))
	}()
	<-directory.entered
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/api/objects?scope=strategies", nil))
	close(directory.release)
	<-done
	if second.Code != http.StatusOK || first.Code != http.StatusOK {
		t.Fatalf("first %d, second %d %s; want both answered while the first is built", first.Code, second.Code, second.Body.String())
	}
}

// Configuration reads run side by side too: one decoding a large group
// does not refuse the next.
func TestEffectiveConfigReadsRunSideBySide(t *testing.T) {
	directory := &stubDirectory{available: true, snapshot: controlplane.StrategyDirectorySnapshot{Complete: true, Revision: "r"},
		resolvable: true, planEntered: make(chan struct{}), planRelease: make(chan struct{})}
	handler := WithStrategyDirectory(http.NotFoundHandler(), directory, nil, nil, "pod-a", time.Now)
	path := "/api/objects?scope=strategies&strategy=7&include=effective_config"
	first := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, path, nil))
	}()
	<-directory.planEntered
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodGet, path, nil))
	close(directory.planRelease)
	<-done
	if second.Code == http.StatusTooManyRequests || first.Code == http.StatusTooManyRequests {
		t.Fatalf("first %d, second %d %s; want neither refused as busy", first.Code, second.Code, second.Body.String())
	}
	if directory.plans.Load() != 2 {
		t.Fatalf("%d configuration reads ran, want both", directory.plans.Load())
	}
}

// A process with no catalog to answer from says why, in the words a
// strategy's standing refuses with: the reason read from the control plane
// now, or unknown when there is nothing to read it from.
func TestANotReadyDirectorySaysWhyThereIsNoCatalog(t *testing.T) {
	directory := &stubDirectory{snapshot: controlplane.StrategyDirectorySnapshot{Reason: "LEADER_CATALOG_NOT_READY",
		Rows: []controlplane.StrategyDirectoryRow{}}}
	failing := 1800.0
	for _, testCase := range []struct {
		name    string
		absence CatalogAbsenceFunc
		want    CatalogAbsenceReason
	}{
		{name: "read", want: CatalogAbsencePublishFailing, absence: func() CatalogAbsenceFacts {
			return CatalogAbsenceFacts{Known: true, Role: "leader", Exit: "retain_executable", Text: "compile failed", FailingSeconds: &failing}
		}},
		{name: "nothing to read it from", want: CatalogAbsenceUnknown},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			WithStrategyDirectory(http.NotFoundHandler(), directory, nil, testCase.absence, "pod-a", time.Now).
				ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/objects?scope=strategies", nil))
			var body struct {
				Reason   string          `json:"reason"`
				NotReady *CatalogAbsence `json:"not_ready"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if recorder.Code != http.StatusServiceUnavailable || body.Reason != "LEADER_CATALOG_NOT_READY" || body.NotReady == nil ||
				body.NotReady.Reason != testCase.want || body.NotReady.Replica != "pod-a" || body.NotReady.Detail == "" {
				t.Fatalf("not ready = %d %s, want 503 with %s, the replica and a sentence", recorder.Code, recorder.Body.String(), testCase.want)
			}
		})
	}
	// A ready answer carries none.
	directory.snapshot = controlplane.StrategyDirectorySnapshot{Complete: true, Revision: "r", Rows: []controlplane.StrategyDirectoryRow{}}
	recorder := httptest.NewRecorder()
	WithStrategyDirectory(http.NotFoundHandler(), directory, nil, nil, "pod-a", time.Now).
		ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/objects?scope=strategies", nil))
	var body map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || body["not_ready"] != nil {
		t.Fatalf("ready answer = %s (%v), want no not_ready", recorder.Body.String(), err)
	}
}
