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
	entered   chan struct{}
	release   chan struct{}
	pages     atomic.Int32
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
	return controlplane.StrategyDirectoryRow{}, controlplane.ErrSnapshotUnavailable
}

func (d *stubDirectory) EffectivePlan(context.Context, controlplane.StrategyDirectoryRow) (controlplane.QueryGroupPlanObject, error) {
	return controlplane.QueryGroupPlanObject{}, controlplane.ErrCatalogObjectUnavailable
}

func (d *stubDirectory) EffectiveOutput(context.Context, controlplane.StrategyDirectoryRow) controlplane.OutputFormatFacts {
	return controlplane.OutputFormatFacts{}
}

// One page is built at a time: a second request while one is being built
// is refused as busy, not queued behind it, and the first is answered.
func TestOneDirectoryPageIsBuiltAtATime(t *testing.T) {
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
	if second.Code != http.StatusTooManyRequests || !json.Valid(second.Body.Bytes()) {
		t.Fatalf("second request = %d %s, want 429 while the first is built", second.Code, second.Body.String())
	}
	if first.Code != http.StatusOK {
		t.Fatalf("first request = %d %s, want its page", first.Code, first.Body.String())
	}
	// The flight is released with the answer.
	third := httptest.NewRecorder()
	handler.ServeHTTP(third, httptest.NewRequest(http.MethodGet, "/api/objects?scope=strategies", nil))
	if third.Code != http.StatusOK {
		t.Fatalf("a request after the first = %d, want answered", third.Code)
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
