package uq

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/lookback"
)

func sharedTestSeries() responseSeries {
	return responseSeries{Columns: []string{"_time", "_value"}, Types: []string{"float", "float"},
		GroupKeys: []string{"bk_target_ip"}, GroupValues: []json.RawMessage{json.RawMessage(`"127.0.0.1"`)},
		Values: [][]json.RawMessage{{json.RawMessage(`1700123000000`), json.RawMessage(`12.34`)}, {json.RawMessage(`1700124000000`), json.RawMessage(`13`)}}}
}

func sharedTestBody(t *testing.T, series []responseSeries, metadata map[string]any) string {
	t.Helper()
	var body strings.Builder
	seq := 0
	write := func(key string, value any) {
		raw, err := json.Marshal(map[string]any{"seq": seq, key: value})
		if err != nil {
			t.Fatal(err)
		}
		body.Write(raw)
		body.WriteByte('\n')
		seq++
	}
	write("v", 1)
	var points int
	for i, item := range series {
		write("schemas", []sharedSchema{{ID: uint32(i), Columns: item.Columns, Types: item.Types, GroupKeys: item.GroupKeys}})
		write("series", []any{map[string]any{"s": i, "g": item.GroupValues, "r": item.Values}})
		points += len(item.Values)
	}
	footer := map[string]any{"series": len(series), "points": points}
	for key, value := range metadata {
		footer[key] = value
	}
	write("end", footer)
	return body.String()
}

func TestSharedSchemaPreservesLegacyBusinessSemantics(t *testing.T) {
	series := sharedTestSeries()
	second := series
	second.GroupKeys = []string{"extra", "bk_target_ip"}
	second.GroupValues = []json.RawMessage{json.RawMessage(`true`), json.RawMessage(`null`)}
	second.Values = [][]json.RawMessage{{json.RawMessage(`1700123000000`), json.RawMessage(`9007199254740993`)}}
	for _, sample := range []struct {
		name     string
		series   []responseSeries
		metadata map[string]any
	}{
		{"full", []responseSeries{series, second}, map[string]any{"is_partial": false, "result_table_id": []string{"table"}, "trace_id": "trace"}},
		{"empty", []responseSeries{}, map[string]any{"is_partial": false}},
		{"null value", []responseSeries{func() responseSeries {
			s := series
			s.Values = [][]json.RawMessage{{json.RawMessage(`1700123000000`), json.RawMessage(`null`)}}
			return s
		}()}, map[string]any{"is_partial": false}},
		{"partial", []responseSeries{series}, map[string]any{"is_partial": true}},
		{"status partial", []responseSeries{series}, map[string]any{"is_partial": false, "status": responseStatus{Code: queryTSPartial, Message: "partial"}}},
		{"missing partial", []responseSeries{series}, map[string]any{}},
		{"allowed status", []responseSeries{series}, map[string]any{"is_partial": false, "status": responseStatus{Code: spaceTableIDFieldIsNotExists}}},
		{"unknown status", []responseSeries{series}, map[string]any{"is_partial": false, "status": responseStatus{Code: "UNKNOWN_FAILURE"}}},
		{"empty status", []responseSeries{}, map[string]any{"is_partial": false, "status": responseStatus{Code: spaceTableIDFieldIsNotExists}}},
	} {
		t.Run(sample.name, func(t *testing.T) {
			client := fixtureClient(t, http.StatusOK, "", DefaultLimits())
			client.now = func() time.Time { return time.Unix(1700125000, 0) }
			attempt := validAttempt(t)
			legacy := map[string]any{"series": sample.series}
			for key, value := range sample.metadata {
				legacy[key] = value
			}
			raw, err := json.Marshal(legacy)
			if err != nil {
				t.Fatal(err)
			}
			oldSink, newSink := &collectingSink{}, &collectingSink{}
			oldDone, err := client.decode(context.Background(), bytes.NewReader(raw), attempt, oldSink)
			if err != nil {
				t.Fatal(err)
			}
			newDone, err := client.decodeShared(context.Background(), strings.NewReader(sharedTestBody(t, sample.series, sample.metadata)), queryIdentity{Spec: attempt.Spec, AttemptNo: attempt.AttemptNo}, newSink)
			if err != nil {
				t.Fatal(err)
			}
			for i := range oldSink.batches {
				oldSink.batches[i].Delivery.Bytes = 0
				if i >= len(newSink.batches) {
					t.Fatal("shared delivery omitted a series")
				}
				if newSink.batches[i].Delivery.Bytes == 0 {
					t.Fatal("shared delivery has no retained/lookback charge")
				}
				newSink.batches[i].Delivery.Bytes = 0
			}
			if !reflect.DeepEqual(oldSink.batches, newSink.batches) {
				t.Fatalf("canonical delivery differs: old=%+v new=%+v", oldSink.batches, newSink.batches)
			}
			oldDone.Stats, newDone.Stats = execution.ProviderStats{}, execution.ProviderStats{}
			oldDone.Delivery.Bytes, newDone.Delivery.Bytes = 0, 0
			if !reflect.DeepEqual(oldDone, newDone) {
				t.Fatalf("business completion differs: old=%+v new=%+v", oldDone, newDone)
			}
		})
	}
}

type sharedLookbackSink struct {
	read *lookback.Read
	kept collectingSink
}

func (sink *sharedLookbackSink) ConsumeProviderSeries(ctx context.Context, batch execution.ProviderSeriesBatch) error {
	sink.read.Series(batch.Dataset, batch.Delivery.Bytes)
	return sink.kept.ConsumeProviderSeries(ctx, batch)
}

// The codec's conservative delivered bytes, including rows filtered at End,
// must reach the existing lookback consumer without becoming wire-body bytes.
func TestSharedSchemaLookbackReceivesExpandedDeliveryBytes(t *testing.T) {
	client := fixtureClient(t, http.StatusOK, "", DefaultLimits())
	client.now = func() time.Time { return time.Unix(1700125000, 0) }
	attempt := validAttempt(t)
	engine, err := lookback.New(lookback.Options{Now: client.now,
		Recheck: func(context.Context, execution.PhysicalQuerySpec, execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
			return execution.ProviderCompletion{}, nil
		}, Permit: func() (func(), <-chan struct{}, string) { return func() {}, nil, "" },
		Owns: func(execution.QueryGroupIdentity) bool { return true }, Owned: func() int { return 1 }})
	if err != nil {
		t.Fatal(err)
	}
	sink := &sharedLookbackSink{read: engine.Begin(lookback.Query{Contract: execution.FrozenExecutionContractRef{Slot: attempt.Slot},
		Spec: attempt.Spec, Operation: execution.OperationNormal, AttemptNo: 1, Secondary: true})}
	series := sharedTestSeries()
	done, err := client.decodeShared(context.Background(), strings.NewReader(sharedTestBody(t, []responseSeries{series}, map[string]any{"is_partial": false})),
		queryIdentity{Spec: attempt.Spec, AttemptNo: 1}, sink)
	sink.read.Complete(done, err)
	if err != nil {
		t.Fatal(err)
	}
	expanded, err := expandedSeriesBytes(series, uint64(client.limits.MaxSeriesBytes))
	if err != nil {
		t.Fatal(err)
	}
	var bytes uint64
	for _, source := range engine.Stats().Sources {
		bytes += source.FirstReadBytes
	}
	if bytes != expanded || done.Delivery.Bytes != expanded || len(sink.kept.batches) != 1 || done.Delivery.Records != 1 {
		t.Fatalf("lookback=%d expanded=%d delivered=%+v", bytes, expanded, done.Delivery)
	}
}

func TestSharedSchemaFaultsDoNotCompleteOrRetry(t *testing.T) {
	good := sharedTestBody(t, []responseSeries{sharedTestSeries()}, map[string]any{"is_partial": false})
	cases := map[string]string{
		"missing footer":      good[:strings.LastIndex(good, `{"end"`)],
		"missing LF":          strings.TrimSuffix(good, "\n"),
		"footer then payload": good + "{\"seq\":4,\"series\":[]}\n",
		"footer then garbage": good + "garbage\n",
		"blank":               good + "\n",
		"duplicate seq":       strings.Replace(good, `"seq":2`, `"seq":1`, 1),
		"seq null":            strings.Replace(good, `"seq":0`, `"seq":null`, 1),
		"seq missing":         strings.Replace(good, `"seq":0,`, "", 1),
		"version null":        strings.Replace(good, `"v":1`, `"v":null`, 1),
		"version missing":     strings.Replace(good, `,"v":1`, "", 1),
		"future version":      strings.Replace(good, `"v":1`, `"v":2`, 1),
		"schema id null":      strings.Replace(good, `"id":0`, `"id":null`, 1),
		"schema id missing":   strings.Replace(good, `"id":0,`, "", 1),
		"unknown schema":      strings.Replace(good, `"s":0`, `"s":1`, 1),
		"null schema ref":     strings.Replace(good, `"s":0`, `"s":null`, 1),
		"wrong points":        strings.Replace(good, `"points":2`, `"points":1`, 1),
		"null counts":         strings.Replace(good, `"points":2`, `"points":null`, 1),
		"missing counts":      strings.Replace(good, `"points":2,`, "", 1),
		"null partial":        strings.Replace(good, `"is_partial":false`, `"is_partial":null`, 1),
		"string partial":      strings.Replace(good, `"is_partial":false`, `"is_partial":"false"`, 1),
		"row width":           strings.Replace(good, `1700123000000,12.34`, `1700123000000`, 1),
		"group width":         strings.Replace(good, `"g":["127.0.0.1"]`, `"g":[]`, 1),
		"duplicate key":       strings.Replace(good, `"seq":0`, `"seq":0,"seq":0`, 1),
		"invalid utf8":        strings.Replace(good, `127.0.0.1`, string([]byte{0xff}), 1),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			var posts atomic.Uint32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				posts.Add(1)
				w.Header().Set("Content-Type", SharedSchemaMediaType)
				_, _ = io.WriteString(w, body)
			}))
			defer server.Close()
			client, err := NewClientWithLimits(server.URL, "alarmd", server.Client(), DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			done, err := client.Execute(context.Background(), validAttempt(t), &collectingSink{})
			if err == nil || done.Ref != "" || posts.Load() != 1 {
				t.Fatalf("fault completed or queried again: done=%+v err=%v posts=%d", done, err, posts.Load())
			}
			var named interface{ QueryFailure() (string, string) }
			if !errors.As(err, &named) {
				t.Fatalf("protocol fault is unnamed: %v", err)
			}
		})
	}
}

func TestSharedSchemaNegotiationPreservesRequestAndSameResponseFallback(t *testing.T) {
	attempt := validAttempt(t)
	_, original, err := buildWireRequest(attempt.Spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, media := range []string{"application/json; charset=utf-8", ""} {
		t.Run(media, func(t *testing.T) {
			var posts atomic.Uint32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				posts.Add(1)
				raw, _ := io.ReadAll(r.Body)
				if !bytes.Equal(raw, original) || r.Header.Get("Content-Type") != "application/json" || r.Header.Get(headerTenant) != attempt.Spec.PlanFacts.TenantID || r.Header.Get(headerSpace) != attempt.Spec.PlanFacts.SpaceScope {
					t.Error("codec changed request contract")
				}
				if r.Header.Get("Accept") != sharedSchemaAccept {
					t.Errorf("unexpected negotiation %q", r.Header.Get("Accept"))
				}
				if media != "" {
					w.Header().Set("Content-Type", media)
				} else {
					// An old provider may omit Content-Type. Suppress net/http
					// sniffing so the response really has no media type.
					w.Header()["Content-Type"] = nil
				}
				_, _ = io.WriteString(w, `{"series":[],"is_partial":false}`)
			}))
			defer server.Close()
			client, err := NewClientWithLimits(server.URL, "alarmd", server.Client(), DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			done, err := client.Execute(context.Background(), attempt, &collectingSink{})
			if err != nil || done.Completeness != execution.CompletenessFull || posts.Load() != 1 || !done.Stats.SharedSchemaRequested || done.Stats.ResponseCodec != "legacy_json" {
				t.Fatalf("fallback=%+v err=%v posts=%d", done, err, posts.Load())
			}
		})
	}
	for _, media := range []string{SharedSchemaMediaType, "application/vnd.bkmonitor.uq.shared-schema.v2+ndjson", "application/octet-stream"} {
		_, err := selectSharedDecoder(media, false)
		if media == "application/octet-stream" {
			if err != nil {
				t.Fatal("disabled path changed legacy type tolerance")
			}
			continue
		}
		if err == nil {
			t.Fatalf("unsolicited new format accepted: %s", media)
		}
		_, err = selectSharedDecoder(media, true)
		if media != SharedSchemaMediaType && err == nil {
			t.Fatal("unknown opted-in format accepted")
		}
	}
}

func TestSharedSchemaDefaultExecuteConsumesV1Once(t *testing.T) {
	var posts atomic.Uint32
	body := sharedTestBody(t, []responseSeries{sharedTestSeries()}, map[string]any{"is_partial": false})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		if r.Header.Get("Accept") != sharedSchemaAccept || r.URL.Path != "/query/ts" {
			t.Errorf("ordinary Execute did not negotiate: %s %q", r.URL.Path, r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", SharedSchemaMediaType)
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "alarmd", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	sink := &collectingSink{}
	done, err := client.Execute(context.Background(), validAttempt(t), sink)
	if err != nil || done.Completeness != execution.CompletenessFull || posts.Load() != 1 ||
		!done.Stats.SharedSchemaRequested || done.Stats.ResponseCodec != "shared_json_v1" ||
		done.Delivery.Series != 1 || done.Delivery.Records != 1 || len(sink.batches) != 1 {
		t.Fatalf("shared=%+v err=%v posts=%d delivered=%d", done, err, posts.Load(), len(sink.batches))
	}
}

func TestSharedSchemaDefaultExecuteRejectsUnknownResponseWithoutRetry(t *testing.T) {
	for _, media := range []string{"application/vnd.bkmonitor.uq.shared-schema.v2+ndjson", "application/octet-stream", "invalid/media; broken"} {
		t.Run(media, func(t *testing.T) {
			var posts atomic.Uint32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				posts.Add(1)
				w.Header().Set("Content-Type", media)
				_, _ = io.WriteString(w, `{"series":[],"is_partial":false}`)
			}))
			defer server.Close()
			client, err := NewClient(server.URL, "alarmd", server.Client())
			if err != nil {
				t.Fatal(err)
			}
			sink := &collectingSink{}
			done, err := client.Execute(context.Background(), validAttempt(t), sink)
			var named interface{ QueryFailure() (string, string) }
			if err == nil || !errors.As(err, &named) || done.Ref != "" || posts.Load() != 1 || len(sink.batches) != 0 {
				t.Fatalf("unknown response consumed or retried: done=%+v err=%v posts=%d", done, err, posts.Load())
			}
		})
	}
}

func TestSharedSchemaExcludedExecuteEntriesRemainLegacy(t *testing.T) {
	for _, entry := range []string{"promql", "polling"} {
		t.Run(entry, func(t *testing.T) {
			attempt := validAttempt(t)
			facts := attempt.Spec.PlanFacts
			facts.QueryRevision = ""
			path := "/query/ts"
			if entry == "promql" {
				facts.QueryList, facts.MetricMerge = nil, ""
				facts.PromQL = &execution.PromQLQuery{Expression: "up", Match: "{job='fixture'}"}
				facts.Normalization.DatasetContract.IdentityFields = []string{}
				facts.Normalization.DatasetContract.DynamicDimensions = true
				path = "/query/ts/promql"
			} else {
				facts.Normalization.Version = "uq-polling-normalization-v1"
			}
			var err error
			facts, err = execution.BuildQueryPlanFacts(facts)
			if err != nil {
				t.Fatal(err)
			}
			spec := attempt.Spec
			spec.Digest, spec.PlanFacts = "", facts
			attempt.Spec, err = execution.BuildPhysicalQuerySpec(spec)
			if err != nil {
				t.Fatal(err)
			}
			var posts atomic.Uint32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				posts.Add(1)
				if r.Header.Get("Accept") != "" || r.URL.Path != path {
					t.Errorf("excluded Execute negotiated: %s %q", r.URL.Path, r.Header.Get("Accept"))
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"series":[],"is_partial":false}`)
			}))
			defer server.Close()
			client, err := NewClient(server.URL, "alarmd", server.Client())
			if err != nil {
				t.Fatal(err)
			}
			done, err := client.Execute(context.Background(), attempt, &collectingSink{})
			if err != nil || done.Completeness != execution.CompletenessFull || done.Stats.SharedSchemaRequested || posts.Load() != 1 {
				t.Fatalf("excluded entry=%+v err=%v posts=%d", done, err, posts.Load())
			}
		})
	}
}

func TestExpandedDeliveryLengthIsConservativeAndBounded(t *testing.T) {
	for _, text := range []string{"", "plain", "<>&", "业务", "line\u2028sep\u2029", "quote\"slash\\", "\t\n\r\x01", string([]byte{0xff})} {
		got := jsonStringBytes(text)
		raw, err := json.Marshal(text)
		if err != nil || got != uint64(len(raw)) {
			t.Fatalf("string length=%d want=%d err=%v", got, len(raw), err)
		}
	}
	for _, raw := range []string{`null`, `true`, `9007199254740993`, `-0.001e+200`, `"<>&\u2028"`, " \"a\u2028\u2029\" "} {
		series := sharedTestSeries()
		series.GroupValues = []json.RawMessage{json.RawMessage(raw)}
		got, err := expandedSeriesBytes(series, math.MaxUint64)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(series)
		if err != nil {
			t.Fatal(err)
		}
		if got < uint64(len(encoded)) {
			t.Fatalf("underestimated scalar %q: got=%d actual=%d", raw, got, len(encoded))
		}
		if _, err := expandedSeriesBytes(series, got-1); !errors.Is(err, ErrSeriesBytesExceeded) {
			t.Fatal("length limit not enforced")
		}
		if _, err := expandedSeriesBytes(series, got); err != nil {
			t.Fatal("exact length rejected", err)
		}
	}
}

func TestSharedSchemaDeterministicLimitsConserveDelivery(t *testing.T) {
	series := sharedTestSeries()
	good := sharedTestBody(t, []responseSeries{series, series}, map[string]any{"is_partial": false})
	limits := DefaultLimits()
	limits.MaxSeries = 1
	client := fixtureClient(t, http.StatusOK, "", limits)
	sink := &collectingSink{}
	done, err := client.decodeShared(context.Background(), strings.NewReader(good), queryIdentity{Spec: validAttempt(t).Spec, AttemptNo: 1}, sink)
	if err != nil || done.Completeness != execution.CompletenessUnavailable || len(sink.batches) != 1 || done.Delivery != sink.batches[0].Delivery || done.RouteFacts.Attempts[0].Detail != "response=limit_total_series" {
		t.Fatalf("limit outcome=%+v err=%v", done, err)
	}
	base := `{"seq":0,"v":1,"padding":"` + `"}` + "\n"
	exact := `{"seq":0,"v":1,"padding":"` + strings.Repeat("x", sharedFrameBytes-len(base)) + `"}` + "\n"
	client = fixtureClient(t, http.StatusOK, "", DefaultLimits())
	identity := queryIdentity{Spec: validAttempt(t).Spec, AttemptNo: 1}
	end := "{\"seq\":1,\"end\":{\"series\":0,\"points\":0,\"is_partial\":false}}\n"
	if done, err := client.decodeShared(context.Background(), strings.NewReader(exact+end), identity, &collectingSink{}); err != nil || done.Completeness != execution.CompletenessFull {
		t.Fatal("exact frame boundary rejected", err)
	}
	if done, err := client.decodeShared(context.Background(), strings.NewReader(" "+exact+end), identity, &collectingSink{}); err != nil || done.Completeness != execution.CompletenessUnavailable || done.RouteFacts.Attempts[0].Detail != "response=limit_frame_bytes" {
		t.Fatal("oversized frame not named", done, err)
	}
	limits = DefaultLimits()
	limits.MaxRecords = 1
	client = fixtureClient(t, http.StatusOK, "", limits)
	if done, err := client.decodeShared(context.Background(), strings.NewReader(sharedTestBody(t, []responseSeries{series}, map[string]any{"is_partial": false})), identity, &collectingSink{}); err != nil || done.Completeness != execution.CompletenessUnavailable || done.RouteFacts.Attempts[0].Detail != "response=limit_total_records" {
		t.Fatal("raw end row escaped record budget", done, err)
	}
}

func TestSharedSchemaHTTPBodyUsesConfiguredProductionLimit(t *testing.T) {
	body := sharedTestBody(t, []responseSeries{sharedTestSeries()}, map[string]any{"is_partial": false})
	for _, maximum := range []int64{int64(len(body)), int64(len(body) - 1)} {
		t.Run(fmt.Sprint(maximum), func(t *testing.T) {
			var posts atomic.Uint32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				posts.Add(1)
				w.Header().Set("Content-Type", SharedSchemaMediaType)
				_, _ = io.WriteString(w, body)
			}))
			defer server.Close()
			limits := DefaultLimits()
			limits.MaxBodyBytes, limits.MaxSeriesBytes = maximum, maximum
			client, err := NewClientWithLimits(server.URL, "alarmd", server.Client(), limits)
			if err != nil {
				t.Fatal(err)
			}
			sink := &collectingSink{}
			done, err := client.Execute(context.Background(), validAttempt(t), sink)
			var delivered execution.SeriesDelivery
			for _, batch := range sink.batches {
				delivered, err = execution.AccumulateSeriesDelivery(delivered, batch.Delivery)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err != nil || posts.Load() != 1 || done.Delivery != delivered {
				t.Fatalf("body limit lost delivery or re-queried: done=%+v err=%v posts=%d", done, err, posts.Load())
			}
			if maximum == int64(len(body)) {
				if done.Completeness != execution.CompletenessFull || len(sink.batches) != 1 {
					t.Fatal("exact body bound rejected")
				}
			} else if done.Completeness != execution.CompletenessUnavailable || done.RouteFacts.Attempts[0].Detail != "response=limit_response_bytes" {
				t.Fatal("body bound bypassed or not named", done)
			}
		})
	}
}

type cancelSharedSink struct {
	cancel   context.CancelFunc
	received int
}

func (sink *cancelSharedSink) ConsumeProviderSeries(ctx context.Context, _ execution.ProviderSeriesBatch) error {
	sink.received++
	sink.cancel()
	return ctx.Err()
}

func TestSharedSchemaCallerCancelStopsDeliveryAndReleasesHTTP(t *testing.T) {
	body := sharedTestBody(t, []responseSeries{sharedTestSeries(), sharedTestSeries()}, map[string]any{"is_partial": false})
	var posts atomic.Uint32
	released := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		w.Header().Set("Content-Type", SharedSchemaMediaType)
		// Leave the body open after the data so cancellation also tests transport release.
		_, _ = io.WriteString(w, body[:strings.LastIndex(body, `{"end"`)])
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(released)
	}))
	defer server.Close()
	client, _ := NewClientWithLimits(server.URL, "alarmd", server.Client(), DefaultLimits())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := &cancelSharedSink{cancel: cancel}
	done, err := client.Execute(ctx, validAttempt(t), sink)
	if !errors.Is(err, context.Canceled) || sink.received != 1 || done.Ref != "" || posts.Load() != 1 {
		t.Fatalf("cancel done=%+v err=%v delivered=%d", done, err, sink.received)
	}
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("request remained active after local cancellation")
	}
}

func TestSharedSchemaRecheckAndRangeRemainLegacy(t *testing.T) {
	var posts atomic.Uint32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		if r.Header.Get("Accept") != "" {
			t.Errorf("non-Execute entry negotiated a new format: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"series":[],"is_partial":false}`)
	}))
	defer server.Close()
	client, _ := NewClientWithLimits(server.URL, "alarmd", server.Client(), DefaultLimits())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := client.Recheck(ctx, validAttempt(t).Spec, &collectingSink{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Range(ctx, rangeRequest()); err != nil {
		t.Fatal(err)
	}
	if posts.Load() != 2 {
		t.Fatal("unexpected query count", posts.Load())
	}
}

func TestSharedSchemaDictionaryAndFrameSeriesLimits(t *testing.T) {
	client := fixtureClient(t, http.StatusOK, "", DefaultLimits())
	identity := queryIdentity{Spec: validAttempt(t).Spec, AttemptNo: 1}
	var body strings.Builder
	body.WriteString("{\"seq\":0,\"v\":1}\n")
	for id := 0; id < sharedSchemaCount; id++ {
		raw, _ := json.Marshal(map[string]any{"seq": id + 1, "schemas": []sharedSchema{{ID: uint32(id), Columns: []string{"_time", "_value"}, Types: []string{"float", "float"}, GroupKeys: []string{}}}})
		body.Write(raw)
		body.WriteByte('\n')
	}
	exact := body.String() + fmt.Sprintf("{\"seq\":%d,\"end\":{\"series\":0,\"points\":0,\"is_partial\":false}}\n", sharedSchemaCount+1)
	if done, err := client.decodeShared(context.Background(), strings.NewReader(exact), identity, &collectingSink{}); err != nil || done.Completeness != execution.CompletenessFull {
		t.Fatal("exact schema count rejected", err)
	}
	extra, _ := json.Marshal(map[string]any{"seq": sharedSchemaCount + 1, "schemas": []sharedSchema{{ID: sharedSchemaCount, Columns: []string{"_time", "_value"}, Types: []string{"float", "float"}, GroupKeys: []string{}}}})
	body.Write(extra)
	body.WriteByte('\n')
	if done, err := client.decodeShared(context.Background(), strings.NewReader(body.String()), identity, &collectingSink{}); err != nil || done.Completeness != execution.CompletenessUnavailable || done.RouteFacts.Attempts[0].Detail != "response=limit_dictionary" {
		t.Fatal("dictionary count limit unnamed", done, err)
	}
	// A declaration may have a large (unconsumed) column, still below its
	// frame limit. Its size must count even if no data ever refers to it.
	body.Reset()
	body.WriteString("{\"seq\":0,\"v\":1}\n")
	for id := 0; id < 40; id++ {
		raw, _ := json.Marshal(map[string]any{"seq": id + 1, "schemas": []sharedSchema{{ID: uint32(id), Columns: []string{"_time", strings.Repeat("x", 240<<10)}, Types: []string{"float", "float"}, GroupKeys: []string{}}}})
		body.Write(raw)
		body.WriteByte('\n')
	}
	if done, err := client.decodeShared(context.Background(), strings.NewReader(body.String()), identity, &collectingSink{}); err != nil || done.Completeness != execution.CompletenessUnavailable || done.RouteFacts.Attempts[0].Detail != "response=limit_dictionary" {
		t.Fatal("dictionary bytes limit unnamed", done, err)
	}
	entries := make([]any, sharedFrameSeries+1)
	for i := range entries {
		entries[i] = map[string]any{"s": 0, "g": []string{}, "r": [][]int64{}}
	}
	raw, _ := json.Marshal(map[string]any{"seq": 2, "series": entries})
	prefix := "{\"seq\":0,\"v\":1}\n{\"seq\":1,\"schemas\":[{\"id\":0,\"columns\":[\"_time\",\"_value\"],\"types\":[\"float\",\"float\"],\"group_keys\":[]}]}\n"
	if done, err := client.decodeShared(context.Background(), strings.NewReader(prefix+string(raw)+"\n"), identity, &collectingSink{}); err != nil || done.Completeness != execution.CompletenessUnavailable || done.RouteFacts.Attempts[0].Detail != "response=limit_frame_series" {
		t.Fatal("frame series limit unnamed", done, err)
	}
}
