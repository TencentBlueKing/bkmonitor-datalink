package kafka

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func TestLegacyRPCMatchesRealPythonAdapterFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/python-legacy-adapter.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Params struct {
			Tenant     string                     `json:"bk_tenant_id"`
			BusinessID int64                      `json:"bk_biz_id"`
			Strategies map[string]json.RawMessage `json:"strategies"`
			Events     []legacyRPCEvent           `json:"events"`
		} `json:"params"`
		Response struct {
			Topic  string                 `json:"topic"`
			Events []LegacyConvertedEvent `json:"events"`
		} `json:"response"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ALARMD_TEST_RPC_TOKEN", "fixture-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Params struct {
				Strategies map[string]json.RawMessage `json:"strategies"`
				Events     []legacyRPCEvent           `json:"events"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if len(request.Params.Strategies) != 1 || len(request.Params.Events) != 2 {
			t.Fatal("lost cross-language batch")
		}
		for i, event := range request.Params.Events {
			want := fixture.Params.Events[i]
			want.StrategyKey = event.StrategyKey
			gotJSON, _ := json.Marshal(event)
			wantJSON, _ := json.Marshal(want)
			if !bytes.Equal(gotJSON, wantJSON) {
				t.Fatalf("Go request differs from real Python adapter input: %s vs %s", gotJSON, wantJSON)
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"result": true, "data": map[string]any{"result": fixture.Response}})
	}))
	defer server.Close()
	converter, err := NewLegacyHTTPConverter(LegacyAdapterConfig{URL: server.URL, TokenEnv: "ALARMD_TEST_RPC_TOKEN", Topic: fixture.Response.Topic, Timeout: time.Second}, server.Client(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	var events []contract.TriggerEventV1
	for _, input := range fixture.Params.Events {
		values := make(map[string]json.RawMessage)
		for key, value := range input.Values {
			values[key] = value
		}
		values["value"] = input.Value
		event := contract.TriggerEventV1{EventID: input.EventID, TenantID: fixture.Params.Tenant, BusinessID: strconv.FormatInt(fixture.Params.BusinessID, 10), EventKind: input.EventKind, PrimaryLevelID: input.PrimaryLevelID, RecordRef: contract.TriggerRecordRefV1{SourceTime: input.SourceTime, Dimensions: input.Dimensions}, Observed: contract.TriggerObservedV1{Values: values}, LegacyOutput: &contract.LegacyEventContext{Configuration: contract.FreezeLegacyOutput(&contract.LegacyOutputContext{Strategy: fixture.Params.Strategies[input.StrategyKey], DimensionFields: input.DimensionFields, ItemID: strconv.FormatInt(input.ItemID, 10)}), AnomalyTimestamps: input.AnomalyTimestamps}}
		// Canonical UQ normalizes the primary value field to "value".
		fixture.Params.Events[len(events)].Values = values
		events = append(events, event)
	}
	converted, err := converter.ConvertBatch(context.Background(), events)
	if err != nil {
		t.Fatal(err)
	}
	for i, item := range converted {
		want := fixture.Response.Events[i]
		if item.EventID != want.EventID || item.DedupeMD5 != want.DedupeMD5 || string(item.Payload) != want.PayloadJSON {
			t.Fatal("Python payload bytes/identity were rewritten")
		}
	}
}

func legacyEventForTest(t *testing.T) contract.TriggerEventV1 {
	event := legacyTriggerEventGolden(t)
	event.LegacyOutput = &contract.LegacyEventContext{Configuration: contract.FreezeLegacyOutput(&contract.LegacyOutputContext{Strategy: json.RawMessage(`{"id":1001,"bk_biz_id":2,"update_time":1,"name":"frozen name"}`), DimensionFields: []string{"host"}, ItemID: "11"}), AnomalyTimestamps: []int64{event.RecordRef.SourceTime}}
	return event
}

func TestLegacyHTTPConversionAndFinalKafkaRouting(t *testing.T) {
	t.Setenv("ALARMD_TEST_RPC_TOKEN", "fixture-token")
	legacy := legacyEventForTest(t)
	native := triggerEventGolden(t)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/v4/kernel_rpc/call/" || r.Header.Get("Authorization") != "Bearer fixture-token" || r.Header.Get("X-Bk-Tenant-Id") != "default" {
			t.Error("incorrect authenticated RPC route")
		}
		var request struct {
			Function   string `json:"func_name"`
			BusinessID int64  `json:"bk_biz_id"`
			Params     struct {
				Strategies map[string]json.RawMessage `json:"strategies"`
				Events     []legacyRPCEvent           `json:"events"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Function != "alarmd_adapt_legacy_events" || request.BusinessID != 2 || len(request.Params.Strategies) != 1 || len(request.Params.Events) != 2 {
			t.Error("RPC did not group/deduplicate frozen strategy")
		}
		var result []LegacyConvertedEvent
		for _, event := range request.Params.Events {
			if len(event.AnomalyTimestamps) != 1 || event.AnomalyTimestamps[0] != legacy.RecordRef.SourceTime {
				t.Error("actual anomaly timestamps lost")
			}
			result = append(result, LegacyConvertedEvent{EventID: event.EventID, PayloadJSON: `{"strategy_id":1001,"status":"ABNORMAL"}`, DedupeMD5: "0260bae09d2ae3f75683bd06a76e9479"})
		}
		json.NewEncoder(w).Encode(map[string]any{"result": true, "data": map[string]any{"result": map[string]any{"topic": "alarmd_python-events-1", "events": result}}})
	}))
	defer server.Close()
	converter, err := NewLegacyHTTPConverter(LegacyAdapterConfig{URL: server.URL + "/api/v4/kernel_rpc/call/", TokenEnv: "ALARMD_TEST_RPC_TOKEN", Timeout: time.Second, Topic: "alarmd_python-events-1"}, server.Client(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	producer := &batchAwareSyncProducer{}
	sink, err := newTriggerEventSink("native", producer, &fakeCloser{})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	if err := sink.ConfigureLegacyOutput(converter, "alarmd_python-events-1", 1<<20); err != nil {
		t.Fatal(err)
	}
	if err := sink.WriteBatch(context.Background(), []contract.TriggerEventV1{legacy, native, legacy}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || producer.batchCalls != 1 {
		t.Fatal("expected one synchronous adapter call then final Kafka batch")
	}
	for i, msg := range producer.messages {
		if i == 1 {
			if msg.Topic != "native" {
				t.Error("native was routed through legacy")
			}
			continue
		}
		key, _ := msg.Key.Encode()
		if msg.Topic != "alarmd_python-events-1" || string(key) != "0260bae09d2ae3f75683bd06a76e9479" {
			t.Error("wrong legacy topic/key")
		}
	}
}

type legacyConverterFunc func(context.Context, []contract.TriggerEventV1) ([]LegacyConvertedEvent, error)

func (f legacyConverterFunc) ConvertBatch(ctx context.Context, events []contract.TriggerEventV1) ([]LegacyConvertedEvent, error) {
	return f(ctx, events)
}

func TestLegacyConversionFailureNeverPublishesOrFallsBack(t *testing.T) {
	producer := &batchAwareSyncProducer{}
	sink, _ := newTriggerEventSink("native", producer, &fakeCloser{})
	defer sink.Close()
	if err := sink.WriteBatch(context.Background(), []contract.TriggerEventV1{legacyTriggerEventGolden(t)}); err == nil {
		t.Fatal("old event lacking context silently fell back to native")
	}
	for _, failure := range []string{"error", "count", "identity", "payload", "dedupe"} {
		t.Run(failure, func(t *testing.T) {
			event := legacyEventForTest(t)
			producer := &batchAwareSyncProducer{}
			sink, _ := newTriggerEventSink("native", producer, &fakeCloser{})
			defer sink.Close()
			converter := legacyConverterFunc(func(context.Context, []contract.TriggerEventV1) ([]LegacyConvertedEvent, error) {
				item := LegacyConvertedEvent{EventID: event.EventID, Payload: json.RawMessage(`{}`), DedupeMD5: "0260bae09d2ae3f75683bd06a76e9479"}
				switch failure {
				case "error":
					return nil, fmt.Errorf("snapshot dependency failed")
				case "count":
					return nil, nil
				case "identity":
					item.EventID = "wrong"
				case "payload":
					item.Payload = json.RawMessage(`invalid`)
				case "dedupe":
					item.DedupeMD5 = "wrong"
				}
				return []LegacyConvertedEvent{item}, nil
			})
			sink.ConfigureLegacyOutput(converter, "alarmd_python-events-1", 1024)
			if err := sink.WriteBatch(context.Background(), []contract.TriggerEventV1{triggerEventGolden(t), event}); err == nil {
				t.Fatal("conversion failure accepted")
			}
			if producer.batchCalls != 0 || producer.singleCalls != 0 {
				t.Fatal("published before successful conversion")
			}
		})
	}
}

func TestLegacyHTTPRejectsTopicMismatchAndTimeout(t *testing.T) {
	t.Setenv("ALARMD_TEST_RPC_TOKEN", "fixture-token")
	for _, slow := range []bool{false, true} {
		t.Run(fmt.Sprint(slow), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if slow {
					time.Sleep(30 * time.Millisecond)
				}
				fmt.Fprint(w, `{"result":true,"data":{"result":{"topic":"unexpected","events":[]}}}`)
			}))
			defer server.Close()
			converter, err := NewLegacyHTTPConverter(LegacyAdapterConfig{URL: server.URL, TokenEnv: "ALARMD_TEST_RPC_TOKEN", Timeout: 5 * time.Millisecond, Topic: "alarmd_python-events-1"}, server.Client(), 1024)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := converter.ConvertBatch(context.Background(), []contract.TriggerEventV1{legacyEventForTest(t)}); err == nil {
				t.Fatal("invalid response/timeout accepted")
			}
		})
	}
}

func TestLegacyHTTPPayloadEscapingPreservesFinalByteLimit(t *testing.T) {
	t.Setenv("ALARMD_TEST_RPC_TOKEN", "fixture-token")
	for _, overLimit := range []bool{false, true} {
		t.Run(fmt.Sprint(overLimit), func(t *testing.T) {
			event := legacyEventForTest(t)
			payload, _ := json.Marshal(map[string]string{"text": strings.Repeat("\"\\", 10000)})
			limit := len(payload)
			if overLimit {
				limit--
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(map[string]any{"result": true, "data": map[string]any{"result": map[string]any{"topic": "alarmd_python-events-1", "events": []LegacyConvertedEvent{{EventID: event.EventID, PayloadJSON: string(payload), DedupeMD5: "0260bae09d2ae3f75683bd06a76e9479"}}}}})
			}))
			defer server.Close()
			converter, err := NewLegacyHTTPConverter(LegacyAdapterConfig{URL: server.URL, TokenEnv: "ALARMD_TEST_RPC_TOKEN", Timeout: time.Second, Topic: "alarmd_python-events-1"}, server.Client(), limit)
			if err != nil {
				t.Fatal(err)
			}
			producer := &batchAwareSyncProducer{}
			sink, _ := newTriggerEventSink("native", producer, &fakeCloser{})
			defer sink.Close()
			sink.ConfigureLegacyOutput(converter, "alarmd_python-events-1", limit)
			err = sink.WriteBatch(context.Background(), []contract.TriggerEventV1{event})
			if overLimit {
				if err == nil || producer.singleCalls != 0 {
					t.Fatal("oversized final payload accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, _ := producer.messages[0].Value.Encode()
			if !bytes.Equal(got, payload) {
				t.Fatal("escaped payload changed")
			}
		})
	}
}
