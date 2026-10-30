// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lifecycleprocess

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/lifecycle"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
)

func alarmdCloseAlert() domain.Alert {
	alert := storetest.Alert("tenant-a", "linkd-alert", "event", "linkd-fingerprint", "warning")
	alert.EventSourceID = "alarmd"
	alert.SourceAlertID = "0123456789abcdef0123456789abcdef"
	alert.Labels = domain.DimensionMap{"strategy_id": domain.NewStringScalar("123")}
	alert.Dimensions = domain.DimensionMap{"__NO_DATA_DIMENSION__": domain.NewBoolScalar(true)}
	alert.ExtraData = domain.JSONObject{"evaluation_family": json.RawMessage(`"no_data"`)}
	return alert
}

func TestAlarmdCloseMatchesOnlyConfiguredSourceAndExplicitNoData(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*domain.Alert)
		source string
		want   bool
	}{
		{"manual", nil, "manual", true},
		{"auto policy", nil, "auto_policy", true},
		{"work order", nil, "work_order", true},
		{"self heal", nil, "self_heal", true},
		{"system timeout", nil, "system", true},
		{"strategy change", nil, "strategy_change", false},
		{"source close", nil, "source", false},
		{"other channel with same facts", func(a *domain.Alert) { a.EventSourceID = "other" }, "manual", false},
		{"default channel is not configured channel", func(a *domain.Alert) { a.EventSourceID = "built_in_bk" }, "manual", false},
		{"missing marker", func(a *domain.Alert) { a.Dimensions = nil }, "manual", false},
		{"false marker", func(a *domain.Alert) { a.Dimensions["__NO_DATA_DIMENSION__"] = domain.NewBoolScalar(false) }, "manual", false},
		{"empty marker", func(a *domain.Alert) { a.Dimensions["__NO_DATA_DIMENSION__"] = domain.NewStringScalar("") }, "manual", false},
		{"truthy marker", func(a *domain.Alert) { a.Dimensions["__NO_DATA_DIMENSION__"] = domain.NewStringScalar("yes") }, "manual", true},
		{"missing family", func(a *domain.Alert) { a.ExtraData = nil }, "manual", false},
		{"ordinary family", func(a *domain.Alert) { a.ExtraData["evaluation_family"] = json.RawMessage(`"threshold"`) }, "manual", false},
		{"family null", func(a *domain.Alert) { a.ExtraData["evaluation_family"] = json.RawMessage(`null`) }, "manual", false},
		{"text alone", func(a *domain.Alert) {
			a.Dimensions = nil
			a.ExtraData = nil
			a.Title = "无数据"
			a.Content = "no_data"
		}, "manual", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != "/internal/v1/no-data-closures" || r.Header.Get("Authorization") != "" || r.Header.Get("Internal-Token") != "" {
					t.Errorf("unexpected request %s %s headers=%v", r.Method, r.URL.Path, r.Header)
				}
				var body map[string]string
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				want := map[string]string{"bk_tenant_id": "tenant-a", "strategy_id": "123", "source_alert_id": "0123456789abcdef0123456789abcdef"}
				if !reflect.DeepEqual(body, want) {
					t.Errorf("request = %v", body)
				}
				_, _ = io.WriteString(w, "{}")
			}))
			defer server.Close()
			prepare := newAlarmdClosePreparation(&config.AlarmdResource{EventSourceID: "alarmd", NoDataCloseEndpoint: server.URL + "/internal/v1/no-data-closures"}, nil)
			alert := alarmdCloseAlert()
			// 展示层的同名字段不能覆盖来源身份。
			alert.Enrich = domain.JSONObject{"strategy_id": json.RawMessage(`999`), "source_alert_id": json.RawMessage(`"wrong"`)}
			if tc.mutate != nil {
				tc.mutate(&alert)
			}
			if err := prepare(t.Context(), alert, domain.AlertEndOperation{ID: "operation", Source: tc.source}); err != nil {
				t.Fatal(err)
			}
			if (calls.Load() == 1) != tc.want {
				t.Fatalf("calls=%d want=%v", calls.Load(), tc.want)
			}
		})
	}
	for _, resource := range []*config.AlarmdResource{nil, {}, {EventSourceID: "alarmd", TimeoutMilliseconds: -1}} {
		if prepare := newAlarmdClosePreparation(resource, nil); prepare != nil {
			t.Fatal("missing endpoint must disable the entire preparation")
		}
	}
}

func TestAlarmdCloseMissingIdentityAndHTTPFailures(t *testing.T) {
	for _, tc := range []struct {
		name       string
		mutate     func(*domain.Alert)
		status     int
		wantStatus int
		wantCalls  int32
	}{
		{"no strategy", func(a *domain.Alert) { a.Labels = nil }, 200, 400, 0},
		{"invalid strategy", func(a *domain.Alert) { a.Labels["strategy_id"] = domain.NewStringScalar("1.5") }, 200, 400, 0},
		{"no source identity", func(a *domain.Alert) { a.SourceAlertID = "" }, 200, 400, 0},
		{"no tenant", func(a *domain.Alert) { a.BKTenantID = "" }, 200, 400, 0},
		{"service unavailable", nil, 503, 502, 1},
		{"rejected", nil, 400, 502, 1},
		{"not found", nil, 404, 502, 1},
		{"unexpected accepted", nil, 202, 502, 1},
		{"redirect", nil, 307, 502, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Location", "/redirect")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, "private-response-payload")
			}))
			defer server.Close()
			var output bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&output, nil))
			prepare := newAlarmdClosePreparation(&config.AlarmdResource{EventSourceID: "alarmd", NoDataCloseEndpoint: server.URL}, logger)
			alert := alarmdCloseAlert()
			if tc.mutate != nil {
				tc.mutate(&alert)
			}
			err := prepare(t.Context(), alert, domain.AlertEndOperation{ID: "op", Source: "manual"})
			var classified *CloseError
			if !errors.As(err, &classified) || classified.Status != tc.wantStatus || errors.Unwrap(err) == nil || calls.Load() != tc.wantCalls {
				t.Fatalf("failure=%v calls=%d", err, calls.Load())
			}
			if strings.Contains(output.String()+err.Error(), "private-response-payload") {
				t.Fatal("response body leaked")
			}
			var record map[string]any
			if err := json.Unmarshal(output.Bytes(), &record); err != nil {
				t.Fatal(err)
			}
			if record["operation"] != "alarmd_no_data_close" || record["operation_id"] != "op" || record["alert_id"] != alert.AlertID || record["error"] == nil {
				t.Fatalf("missing context: %v", record)
			}
		})
	}
}

func TestAlarmdCloseTimeoutAndCancellation(t *testing.T) {
	for _, cancelRequest := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelRequest), func(t *testing.T) {
			entered := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				close(entered)
				<-r.Context().Done()
			}))
			defer server.Close()
			timeout := int64(100)
			if cancelRequest {
				timeout = 5000
			}
			prepare := newAlarmdClosePreparation(&config.AlarmdResource{EventSourceID: "alarmd", NoDataCloseEndpoint: server.URL, TimeoutMilliseconds: timeout}, nil)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- prepare(ctx, alarmdCloseAlert(), domain.AlertEndOperation{Source: "manual"}) }()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("request did not start")
			}
			if cancelRequest {
				cancel()
			}
			select {
			case err := <-done:
				want := context.DeadlineExceeded
				if cancelRequest {
					want = context.Canceled
				}
				if !errors.Is(err, want) {
					t.Fatalf("failure lost context: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("request did not stop")
			}
		})
	}
}

func TestAlarmdCloseConcurrentCallsKeepTenantAndIdentity(t *testing.T) {
	requests := make(chan map[string]string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		requests <- body
		_, _ = io.WriteString(w, "{}")
	}))
	defer server.Close()
	prepare := newAlarmdClosePreparation(&config.AlarmdResource{EventSourceID: "alarmd", NoDataCloseEndpoint: server.URL}, nil)
	var group sync.WaitGroup
	for i := range 4 {
		group.Go(func() {
			alert := alarmdCloseAlert()
			alert.BKTenantID = fmt.Sprintf("tenant-%d", i)
			alert.SourceAlertID = fmt.Sprintf("%032x", i)
			value, err := domain.NewNumberScalar(float64(i + 1))
			if err != nil {
				t.Error(err)
				return
			}
			alert.Labels["strategy_id"] = value
			if err := prepare(t.Context(), alert, domain.AlertEndOperation{Source: "manual"}); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	for range 4 {
		body := <-requests
		var index int
		if _, err := fmt.Sscanf(body["bk_tenant_id"], "tenant-%d", &index); err != nil {
			t.Fatal(err)
		}
		if body["strategy_id"] != fmt.Sprint(index+1) || body["source_alert_id"] != fmt.Sprintf("%032x", index) {
			t.Fatalf("mixed scope: %v", body)
		}
	}
}

func TestAlarmdClosePrecedesStateAndSkipsNotificationOnProjectionRetry(t *testing.T) {
	repo := memory.New()
	alert := alarmdCloseAlert()
	if _, err := repo.CreateAlert(t.Context(), alert); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	var status atomic.Int32
	status.Store(503)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		stored, err := repo.GetAlert(r.Context(), alert.BKTenantID, alert.AlertID)
		if err != nil || stored.Alert.Status != domain.AlertStatusActive {
			t.Errorf("close happened before remote confirmation: %+v %v", stored, err)
		}
		w.WriteHeader(int(status.Load()))
	}))
	defer server.Close()
	prepare := newAlarmdClosePreparation(&config.AlarmdResource{EventSourceID: "alarmd", NoDataCloseEndpoint: server.URL}, nil)
	processor, err := lifecycle.NewProcessor(repo, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, enrich.NoopEnricher{}, nil, config.DefaultSeverityConfig(), lifecycle.SystemClock{}, slog.New(slog.NewTextHandler(io.Discard, nil)), lifecycle.WithClosePreparation(prepare))
	if err != nil {
		t.Fatal(err)
	}
	projections := 0
	closer := &AlertCloser{run: processor.CloseAlert, projection: closeProjectionFunc(func(context.Context, string, string) error {
		projections++
		if projections == 1 {
			return errors.New("ES unavailable")
		}
		return nil
	})}
	command := validCloseCommand()
	command.AlertID = alert.AlertID
	if _, err := closer.CloseAlert(t.Context(), command); err == nil || projections != 0 {
		t.Fatalf("remote failure ignored: %v", err)
	}
	stored, err := repo.GetAlert(t.Context(), alert.BKTenantID, alert.AlertID)
	if err != nil || stored.Alert.Status != domain.AlertStatusActive {
		t.Fatalf("remote failure closed alert: %+v %v", stored, err)
	}
	status.Store(200)
	result, err := closer.CloseKACAlert(t.Context(), command)
	if err == nil || result.Alert.Status != domain.AlertStatusClosed || calls.Load() != 2 {
		t.Fatalf("projection failure = %+v %v calls=%d", result, err, calls.Load())
	}
	result, err = closer.CloseAlert(t.Context(), command)
	if err != nil || !result.AlreadyClosed || projections != 2 || calls.Load() != 2 {
		t.Fatalf("retry = %+v %v calls=%d projections=%d", result, err, calls.Load(), projections)
	}
}
