// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// hostSystemEventRequest is what the test reads of a query service request.
type hostSystemEventRequest struct {
	QueryList []struct {
		DataSource string   `json:"data_source"`
		TableID    string   `json:"table_id"`
		FieldName  string   `json:"field_name"`
		Dimensions []string `json:"dimensions"`
		Conditions struct {
			FieldList []struct {
				FieldName string   `json:"field_name"`
				Op        string   `json:"op"`
				Value     []string `json:"value"`
			} `json:"field_list"`
		} `json:"conditions"`
	} `json:"query_list"`
	StartTime    string `json:"start_time"`
	EndTime      string `json:"end_time"`
	NotTimeAlign bool   `json:"not_time_align"`
}

// hostSystemEventStrategies are the built-in host system event strategies,
// one an event, each grouped as the platform's migration groups it
// (strategy_migration.SYSTEM_EVENT_TARGET_CONFIGS).
var hostSystemEventStrategies = []struct {
	id         int64
	name       string
	dimensions []string
}{
	{7301, "AgentLost", []string{"bk_target_ip", "bk_target_cloud_id", "bk_agent_id"}},
	{7302, "DiskReadonly", []string{"bk_target_ip", "bk_target_cloud_id", "position", "type", "fs"}},
	{7303, "DiskFull", []string{"bk_target_ip", "bk_target_cloud_id", "disk", "fstype", "file_system"}},
	{7304, "CoreFile", []string{"bk_target_ip", "bk_target_cloud_id", "executable_path", "executable", "signal"}},
	// Without the platform's constraint: every OOM event carries
	// CONSTRAINT_MEMCG there, so it would split no alert.
	{7305, "OOM", []string{"bk_target_ip", "bk_target_cloud_id", "process"}},
}

const hostSystemEventHost, hostSystemEventCloud = "192.0.2.10", "0"

// hostSystemEventRound is how the strategies are written and what the query
// service answers them.
type hostSystemEventRound struct {
	// global writes the strategies in a global business, grouped by
	// bk_biz_id as well; the query skips the space and is answered one
	// series a business in businesses, all of them on the same host.
	global     bool
	businesses []string
	// quiet answers zero on every step.
	quiet bool
}

type hostSystemEventOutcome struct {
	asked     map[string]int
	misrouted []string
	written   []contract.TriggerEventV1
}

// runHostSystemEventRound runs one scheduled round of the strategies through
// the production compiler and Worker on the consumer's protocol. The query
// service answers a strategy only when its request filters on the
// strategy's own event name, the way the shared table holds every event.
func runHostSystemEventRound(t *testing.T, round hostSystemEventRound) hostSystemEventOutcome {
	t.Helper()
	address, client := startPhaseTwoRedis(t)
	ctx := context.Background()
	ids := make([]string, 0, len(hostSystemEventStrategies))
	names := map[string]bool{}
	for _, event := range hostSystemEventStrategies {
		dimensions := event.dimensions
		if round.global {
			dimensions = append(append([]string(nil), dimensions...), contract.BusinessDimension)
		}
		var document map[string]any
		if err := json.Unmarshal(controlledG4StrategyDocument(t, event.id, "Threshold", "", "", dimensions,
			[]any{[]any{map[string]any{"method": "gte", "threshold": 1}}}), &document); err != nil {
			t.Fatal(err)
		}
		document["strategy_revision"] = 1
		if round.global {
			document["is_global_strategy"] = true
		}
		query := document["items"].([]any)[0].(map[string]any)["query_configs"].([]any)[0].(map[string]any)
		delete(query, "metric_field")
		query["data_source_label"], query["data_type_label"], query["agg_method"] = "custom", "event", "COUNT"
		query["result_table_id"], query["custom_event_name"] = "system_event", event.name
		body, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		if err := client.Set(ctx, fmt.Sprintf("alarm-config.strategy_%d", event.id), body, 0).Err(); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, strconv.FormatInt(event.id, 10))
		names[event.name] = true
	}
	if err := client.Set(ctx, "alarm-config.strategy_ids", "["+strings.Join(ids, ",")+"]", 0).Err(); err != nil {
		t.Fatal(err)
	}
	businesses := round.businesses
	if len(businesses) == 0 {
		businesses = []string{""}
	}

	base := controlledG4Base(t)
	var clock atomic.Int64
	clock.Store(base)
	var mu sync.Mutex
	outcome := hostSystemEventOutcome{asked: map[string]int{}}
	uq := &http.Client{Transport: controlledRoundTripper(func(request *http.Request) (*http.Response, error) {
		var payload hostSystemEventRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		space, skip, tenant := request.Header.Get("X-Bk-Scope-Space-Uid"), request.Header.Get("X-Bk-Scope-Skip-Space"), request.Header.Get("X-Bk-Tenant-Id")
		wantSpace, wantSkip := controlledG4SyntheticSpaceUID, ""
		if round.global {
			wantSpace, wantSkip = "", "alarmd"
		}
		if space != wantSpace || skip != wantSkip || tenant != "tenant-a" {
			outcome.misrouted = append(outcome.misrouted, fmt.Sprintf("space %q skip %q tenant %q", space, skip, tenant))
		}
		series := []any{}
		if len(payload.QueryList) != 1 {
			outcome.misrouted = append(outcome.misrouted, fmt.Sprintf("%d queries in one request", len(payload.QueryList)))
		} else if clause := payload.QueryList[0]; clause.TableID != "system_event" || clause.DataSource != "bkapm" || clause.FieldName != "_index" {
			outcome.misrouted = append(outcome.misrouted, fmt.Sprintf("read %s on %s by %s", clause.TableID, clause.DataSource, clause.FieldName))
		} else if !payload.NotTimeAlign {
			name := ""
			for _, field := range clause.Conditions.FieldList {
				if field.FieldName == "event_name" && field.Op == "eq" && len(field.Value) == 1 && names[field.Value[0]] {
					name = field.Value[0]
				}
			}
			outcome.asked[name]++
			start, err := strconv.ParseInt(payload.StartTime, 10, 64)
			if err != nil {
				return nil, err
			}
			end, err := strconv.ParseInt(payload.EndTime, 10, 64)
			if err != nil {
				return nil, err
			}
			// Elasticsearch answers a grouped count with a point every step,
			// zero where the step holds no document, keyed by the fields as
			// asked; the one event is in the last step.
			values := []any{}
			for bucket := start; bucket < end; bucket += 60 {
				count := 0
				if bucket+60 >= end && !round.quiet {
					count = 1
				}
				values = append(values, []any{bucket * 1000, count})
			}
			for _, business := range businesses {
				if name == "" {
					break
				}
				groupValues := make([]string, len(clause.Dimensions))
				for index, key := range clause.Dimensions {
					switch key {
					case "dimensions.bk_target_ip":
						groupValues[index] = hostSystemEventHost
					case "dimensions.bk_target_cloud_id":
						groupValues[index] = hostSystemEventCloud
					case "dimensions." + contract.BusinessDimension:
						groupValues[index] = business
					default:
						groupValues[index] = name + "-" + strings.TrimPrefix(key, "dimensions.")
					}
				}
				series = append(series, map[string]any{"name": "_result0", "columns": []string{"_time", "_value"}, "types": []string{"float", "float"},
					"group_keys": clause.Dimensions, "group_values": groupValues, "values": values})
			}
		}
		var buf bytes.Buffer
		if err := json.NewEncoder(&buf).Encode(map[string]any{"series": series, "status": nil, "trace_id": "host-system-event", "is_partial": false}); err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(&buf), Request: request}, nil
	})}

	cfg := controlledG4RuntimeConfig(address, "http://controlled-uq", "alarmd-host-system-event")
	cfg.PhaseTwo.Output.Protocol = config.OutputProtocolNative
	cfg.PhaseTwo.Linkd.ControlPlane = quietLinkControlPlane(t)
	sink := &recordingPhaseTwoEventSink{}
	bundle, err := openProductionPhaseTwoBundleWithDependencies(ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime), newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		}, phaseTwoProductionExternalDependencies{
			Now: func() time.Time { return time.Unix(clock.Load(), 0) }, HTTPClient: uq,
			PrepareEvents: preparedEvents(func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) { return sink, nil }),
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
	clock.Store(base + 1)
	if err := runScheduledOnceSettled(ctx, bundle); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(outcome.misrouted) > 0 {
		t.Fatalf("requests not for the system_event label in the strategy's scope: %v", outcome.misrouted)
	}
	for _, event := range hostSystemEventStrategies {
		if outcome.asked[event.name] == 0 {
			t.Fatalf("%s was never asked for by its own name: %v", event.name, outcome.asked)
		}
	}
	outcome.written = sink.snapshot()
	return outcome
}

// checkHostSystemEventAlert checks an alert is about the host and carries the
// event's other dimensions bare, as the platform's adapter leaves them
// (MonitorEventAdapter takes the host out of the dimensions the query
// service answered, prefix stripped); business is the bk_biz_id it carries,
// empty for none.
func checkHostSystemEventAlert(t *testing.T, name string, dimensions []string, business string, alert contract.TriggerEventV1) {
	t.Helper()
	if alert.Subject == nil || alert.Subject.Subject.Type != contract.MonitorSubjectHost ||
		alert.Subject.Subject.ID != hostSystemEventHost+"|"+hostSystemEventCloud {
		t.Fatalf("%s alert is about %+v, want the host %s|%s", name, alert.Subject, hostSystemEventHost, hostSystemEventCloud)
	}
	want := map[string]string{}
	for _, dimension := range dimensions {
		if dimension != "bk_target_ip" && dimension != "bk_target_cloud_id" {
			want[dimension] = name + "-" + dimension
		}
	}
	if business != "" {
		want[contract.BusinessDimension] = business
	}
	got := []string{}
	for key := range alert.Subject.Dimensions {
		got = append(got, key)
	}
	sort.Strings(got)
	if len(got) != len(want) {
		t.Fatalf("%s alert dimensions %v, want %v", name, got, want)
	}
	for dimension, wantValue := range want {
		var value string
		if err := json.Unmarshal(alert.Subject.Dimensions[dimension], &value); err != nil || value != wantValue {
			t.Fatalf("%s alert dimensions %v, want %s=%s", name, got, dimension, wantValue)
		}
	}
}

// Each host system event strategy of a business, written as a built-in one
// reads the system_event data label, asks the query service for its own
// event under the business's space, and a count of one opens an alert on the
// host the event names.
func TestEachHostSystemEventStrategyAlertsOnTheHostItNames(t *testing.T) {
	outcome := runHostSystemEventRound(t, hostSystemEventRound{})
	written := map[string]contract.TriggerEventV1{}
	for _, event := range outcome.written {
		if _, twice := written[event.PlanRef.StrategyID]; twice || event.EventKind != contract.TriggerEventAbnormal {
			t.Fatalf("written %v, want one ABNORMAL a strategy", controlledEventKinds(outcome.written))
		}
		written[event.PlanRef.StrategyID] = event
	}
	for _, event := range hostSystemEventStrategies {
		alert, ok := written[strconv.FormatInt(event.id, 10)]
		if !ok {
			t.Fatalf("%s opened no alert; written %v", event.name, controlledEventKinds(outcome.written))
		}
		if alert.AttributedBusinessID != "" {
			t.Fatalf("%s alert of an ordinary business is attributed to %s", event.name, alert.AttributedBusinessID)
		}
		checkHostSystemEventAlert(t, event.name, event.dimensions, "", alert)
	}
}

// A step the shared table holds no event of counts zero, and zero is under
// the threshold of one: steps without events open nothing.
func TestAHostSystemEventStrategyOpensNothingOnStepsWithoutEvents(t *testing.T) {
	outcome := runHostSystemEventRound(t, hostSystemEventRound{quiet: true})
	for _, event := range outcome.written {
		if event.EventKind == contract.TriggerEventAbnormal {
			t.Fatalf("written %v, want no ABNORMAL", controlledEventKinds(outcome.written))
		}
	}
}

// A global business's host system event strategy reads the system_event data
// label with the space skipped, so it sees every business's events of its
// tenant. Grouped by bk_biz_id, the same host's events in two businesses are
// two alerts, each filed under the business its events carry.
func TestAGlobalHostSystemEventStrategyAlertsOnceForEachBusinessOfTheHost(t *testing.T) {
	businesses := []string{"11", "12"}
	outcome := runHostSystemEventRound(t, hostSystemEventRound{global: true, businesses: businesses})
	written := map[string]contract.TriggerEventV1{}
	for _, event := range outcome.written {
		key := event.PlanRef.StrategyID + "/" + event.AttributedBusinessID
		if _, twice := written[key]; twice || event.EventKind != contract.TriggerEventAbnormal {
			t.Fatalf("written %v, want one ABNORMAL a strategy and business", controlledEventKinds(outcome.written))
		}
		written[key] = event
	}
	if len(written) != len(hostSystemEventStrategies)*len(businesses) {
		t.Fatalf("written %d alerts, want %d", len(written), len(hostSystemEventStrategies)*len(businesses))
	}
	for _, event := range hostSystemEventStrategies {
		for _, business := range businesses {
			alert, ok := written[strconv.FormatInt(event.id, 10)+"/"+business]
			if !ok {
				t.Fatalf("%s opened no alert for business %s; written %v", event.name, business, controlledEventKinds(outcome.written))
			}
			checkHostSystemEventAlert(t, event.name, event.dimensions, business, alert)
		}
	}
}
