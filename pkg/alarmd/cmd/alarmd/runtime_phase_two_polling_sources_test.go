package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/go-redis/redis/v8"
)

// Each source must reach committed business events through the production
// compiler and Worker. Transport is synthetic; source-executed Python fixtures
// and UQ formatter tests separately verify the request/ES semantics.
func TestProductionPollingSources(t *testing.T) {
	for _, source := range []struct{ label, kind string }{
		{"custom", "time_series"}, {"prometheus", "time_series"}, {"bk_data", "time_series"},
		{"bk_log_search", "time_series"}, {"bk_log_search", "log"},
		{"bk_monitor", "log"}, {"custom", "event"}, {"bk_fta", "event"},
	} {
		for _, second := range []string{"normal", "unavailable", "partial"} {
			t.Run(source.label+"/"+source.kind+"/"+second, func(t *testing.T) {
				address, client := startPhaseTwoRedis(t)
				ctx := context.Background()
				var document map[string]any
				if err := json.Unmarshal(controlledG4StrategyDocument(t, 7101, "Threshold", "usage", "system.cpu", []string{"host"}, []any{[]any{map[string]any{"method": "gte", "threshold": 100}}}), &document); err != nil {
					t.Fatal(err)
				}
				item := document["items"].([]any)[0].(map[string]any)
				query := item["query_configs"].([]any)[0].(map[string]any)
				query["data_source_label"], query["data_type_label"] = source.label, source.kind
				if source.label == "bk_data" {
					item["expression"] = "a * 1"
				}
				if source.label == "prometheus" {
					query["promql"] = "sum by (host) (synthetic_usage)"
					delete(query, "agg_dimension")
				}
				if source.label == "bk_log_search" {
					query["index_set_id"], query["time_field"] = 71, "dtEventTimeStamp"
					delete(query, "result_table_id")
				}
				if source.kind == "event" || source.kind == "log" {
					query["agg_method"] = "COUNT"
				}
				if source.kind == "event" {
					query["custom_event_name"], query["result_table_id"] = "synthetic-event", "system_event"
				}
				dimension := "host"
				if source.label == "bk_fta" {
					dimension = "tags.host"
					query["agg_dimension"] = []string{dimension}
					delete(query, "metric_field")
					query["alert_name"] = "__ALL_EVENT_PLUGIN__"
				}
				body, err := json.Marshal(document)
				if err != nil {
					t.Fatal(err)
				}
				if err = client.Set(ctx, "alarm-config.strategy_ids", "[7101]", 0).Err(); err != nil {
					t.Fatal(err)
				}
				if err = client.Set(ctx, "alarm-config.strategy_7101", body, 0).Err(); err != nil {
					t.Fatal(err)
				}
				base := controlledG4Base(t)
				var clock atomic.Int64
				clock.Store(base)
				var mu sync.Mutex
				var observations []observability.Observation
				var calls int
				uq := &http.Client{Transport: controlledRoundTripper(func(request *http.Request) (*http.Response, error) {
					mu.Lock()
					calls++
					mu.Unlock()
					var payload map[string]any
					if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
						return nil, err
					}
					endKey := "end_time"
					if source.label == "bk_log_search" {
						clauses, ok := payload["query_list"].([]any)
						if !ok || len(clauses) != 1 || clauses[0].(map[string]any)["table_id"] != "bklog_index_set_71" {
							return nil, fmt.Errorf("log query did not use index_set_id: %v", payload)
						}
					}
					if source.label == "bk_fta" {
						clauses, ok := payload["query_list"].([]any)
						if !ok || len(clauses) != 1 || clauses[0].(map[string]any)["field_semantics"] != "fta_event_tags/v1" || payload["tsdb_map"] == nil {
							return nil, fmt.Errorf("FTA missing ES route or keyed tag semantics: %v", payload)
						}
						sourceFilter, ok := clauses[0].(map[string]any)["source_conditions"].(map[string]any)
						if !ok {
							return nil, fmt.Errorf("FTA missing independent source filter")
						}
						pluginFilter := false
						for _, raw := range sourceFilter["field_list"].([]any) {
							field := raw.(map[string]any)
							if field["field_name"] == "plugin_id" && field["op"] == "ne" && fmt.Sprint(field["value"]) == "[bkmonitor]" {
								pluginFilter = true
							}
						}
						if !pluginFilter {
							return nil, fmt.Errorf("FTA alert_name did not filter event plugins: %v", sourceFilter)
						}
					}
					if source.label == "prometheus" {
						if !strings.HasSuffix(request.URL.Path, "/promql") || payload["promql"] != query["promql"] {
							return nil, fmt.Errorf("PromQL did not use native transport")
						}
						endKey = "end"
					}
					end, err := strconv.ParseInt(fmt.Sprint(payload[endKey]), 10, 64)
					if err != nil {
						return nil, err
					}
					if second == "unavailable" && clock.Load() > base+1 {
						return &http.Response{StatusCode: 502, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("unavailable")), Request: request}, nil
					}
					value := 200
					if clock.Load() > base+1 {
						value = 0
					}
					series := []any{}
					// Two dynamic series expose loss of PromQL identity even when
					// an otherwise successful single-series test would pass.
					for index, host := range []string{"synthetic-a", "synthetic-b"} {
						series = append(series, map[string]any{"name": fmt.Sprintf("series%d", index), "columns": []string{"_time", "_value"}, "types": []string{"float", "float"}, "group_keys": []string{dimension}, "group_values": []string{host}, "values": []any{[]any{(end - 1) * 1000, value}}})
					}
					var buf bytes.Buffer
					if err := json.NewEncoder(&buf).Encode(map[string]any{"series": series, "status": nil, "trace_id": "polling-test", "is_partial": second == "partial" && clock.Load() > base+1}); err != nil {
						return nil, err
					}
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(&buf), Request: request}, nil
				})}
				cfg := controlledG4RuntimeConfig(address, "http://controlled-uq", "alarmd-polling-controlled")
				if source.label == "bk_fta" {
					cfg.PhaseTwo.Control.LegacyQueryRuntime.FTAEventStorage = &execution.QueryStorage{
						TableID: "fta.event", StorageID: "1", StorageType: "elasticsearch", DB: "bkfta_event_*_read", Measurement: "__default__",
						TimeField: execution.QueryTimeField{Name: "time", Type: "date", Unit: "millisecond"},
					}
				}
				events := &recordingPhaseTwoEventSink{}
				bundle, err := openProductionPhaseTwoBundleWithDependencies(ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime), newPhaseTwoApplicationHealth(),
					func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
						return controlplane.NewLegacyRedisStrategySource(client, prefix)
					}, phaseTwoProductionExternalDependencies{
						Now: func() time.Time { return time.Unix(clock.Load(), 0) }, HTTPClient: uq,
						AdditionalObserver: observability.ObserverFunc(func(_ context.Context, o observability.Observation) {
							mu.Lock()
							defer mu.Unlock()
							observations = append(observations, observability.NormalizeObservation(o))
						}),
						OpenEvents: func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) { return events, nil },
					})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := bundle.Shutdown(ctx); err != nil {
						t.Error(err)
					}
				})
				if err := bundle.Start(ctx); err != nil {
					t.Fatal(err)
				}
				for _, slot := range []int64{base, base + 60} {
					clock.Store(slot + 1)
					if err := runScheduledOnceSettled(ctx, bundle); err != nil {
						t.Fatal(err)
					}
				}
				written := events.snapshot()
				want := 4
				if second != "normal" {
					want = 2
				}
				mu.Lock()
				seen := append([]observability.Observation(nil), observations...)
				queried := calls
				mu.Unlock()
				// Decided, as the output lines count it; the recoveries have
				// no message under the Python-compatible protocol these
				// sources run under, so only the anomalies reach the sink.
				decided := decidedEventKinds(seen)
				if len(decided) != want || queried == 0 || len(bundle.queryGroups) != 1 {
					t.Fatalf("decided=%v written=%v calls=%d groups=%d stages=%v", decided, controlledEventKinds(written), queried, len(bundle.queryGroups), observedStages(seen))
				}
				for i, kind := range decided {
					expected := contract.TriggerEventAbnormal
					if i >= 2 {
						expected = contract.TriggerEventRecovery
					}
					if kind != expected {
						t.Fatalf("decided %d=%s want=%s", i, kind, expected)
					}
				}
				if got := controlledEventKinds(written); !reflect.DeepEqual(got, []string{contract.TriggerEventAbnormal, contract.TriggerEventAbnormal}) {
					t.Fatalf("written=%v, want the two anomalies only: a Python-compatible RECOVERY has no message", got)
				}
				assertObservedOrder(t, observedStages(seen), []observability.Stage{observability.StageEventACKed, observability.StageStateApplied, observability.StageProgressCommitted})
			})
		}
	}
}
