// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	uq "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/access/uq"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

const traceLineBody = `{"series":[{"columns":["_time","_value"],"types":["float","float"],"group_keys":["bk_target_ip"],` +
	`"group_values":["192.0.2.10"],"values":[[1700123940000,50.1]]}],"is_partial":false}`

func answer(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, traceLineBody)
}

// slowSink takes each batch after delay.
type slowSink struct{ delay time.Duration }

func (sink slowSink) ConsumeProviderSeries(context.Context, execution.ProviderSeriesBatch) error {
	time.Sleep(sink.delay) // fixed-wait: the stub sink's delay per batch, which the case sets
	return nil
}

func traceLineAttempt(t *testing.T, deadline time.Duration) execution.QueryAttempt {
	t.Helper()
	facts, err := execution.BuildQueryPlanFacts(execution.QueryPlanFacts{
		Provider: execution.ProviderUQ, ProviderRouteRef: "uq-main", TenantID: "tenant", BusinessID: "2", SpaceScope: "bkcc__2",
		QueryList: []execution.QueryClause{{DataSource: "bkmonitor", TableID: "system.cpu", FieldName: "usage",
			ReferenceName: "a", Driver: "influxdb", TimeField: "time",
			TimeAggregation: execution.QueryFunction{Method: "avg", Position: 0, Window: "60s"}, OffsetForward: "false"}},
		MetricMerge: "a", StepMillis: 60_000, AlignmentMillis: 60_000, DownSampleRange: execution.DownSampleNone, Timezone: "UTC",
		Normalization: execution.DatasetNormalizationSpec{DatasetContract: contract.DatasetContractV2{
			SchemaDigest: strings.Repeat("a", 64), NormalizationDigest: strings.Repeat("b", 64),
			IdentityFields: []string{"bk_target_ip"}, SourceTimeField: "_time", ReceivedTimeField: "_received_time"},
			SourceTimeUnit: execution.TimeUnitMillisecond, CanonicalSourceTimeUnit: execution.TimeUnitSecond,
			SeriesIdentityMode: execution.SeriesIdentityUQGroupKeysValuesV1, GroupKeyRule: execution.GroupKeyStripTableSuffixV1,
			ValueSelectionMode: execution.ValueSelectionResultOrFirstReferenceV1, CanonicalValueField: "value",
			ReceivedTimeMode: execution.ReceivedTimeProviderReceivedAt, Version: "uq-threshold-normalization-v1"}})
	if err != nil {
		t.Fatal(err)
	}
	window := execution.QueryWindow{Start: 1_700_123_000, End: 1_700_124_000}
	spec, err := execution.BuildPhysicalQuerySpec(execution.PhysicalQuerySpec{PlanFacts: facts,
		LogicalWindow: window, ProviderRange: window, AcceptedRange: window, RequiredColumns: []string{"value"}})
	if err != nil {
		t.Fatal(err)
	}
	return execution.QueryAttempt{Spec: spec, Slot: execution.SlotIdentity{QueryGroup: "group", EvaluationTime: 1_700_124_000},
		Operation: execution.OperationNormal, AttemptNo: 1, DeadlineUnixMilli: time.Now().Add(deadline).UnixMilli()}
}

// Every degraded or failed query line names the trace id its request was
// sent with, whatever the failure: a request that got no answer by its
// deadline, an answer alarmd could not deliver in time, a provider that
// answered 503, and one that refused the query with a 400. It is known at
// the send, so a failure with no answer names it too, and it is the id an
// operator looks the request up by on the provider's side. A query that
// succeeded names none.
func TestEveryFailedQueryLineNamesTheTraceItWasSentWith(t *testing.T) {
	for _, tc := range []struct {
		name     string
		reason   string
		deadline time.Duration
		delay    time.Duration
		serve    func(http.ResponseWriter, *http.Request)
	}{
		// The deadlines below are the behaviour under test. Each leaves a loaded
		// host seconds to get the request to the server, or the answer back,
		// before it passes: at 200 ms the request itself could come after it,
		// and then nothing was sent to read the trace id from.
		{name: "no answer by the deadline", reason: contract.ReasonQueryTimeout, deadline: 2 * time.Second, // fixed-wait: the deadline under test
			serve: func(w http.ResponseWriter, r *http.Request) {
				select {
				case <-r.Context().Done():
				case <-time.After(3 * time.Second): // fixed-wait: releases the silent server for the close; the client gave up at its 2 s deadline before
				}
			}},
		{name: "delivered too late", reason: contract.ReasonQueryDeliveryTimeout, deadline: 2 * time.Second, delay: 4 * time.Second, // fixed-wait: the deadline under test and a delivery twice as slow
			serve: answer},
		{name: "unavailable", reason: contract.ReasonQueryUnavailable, deadline: time.Minute,
			serve: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = io.WriteString(w, `{"error":"storage busy"}`)
			}},
		{name: "rejected", reason: contract.ReasonQueryRejected, deadline: time.Minute,
			serve: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"error":"field usage is not in table system.cpu"}`)
			}},
		{name: "succeeded", deadline: time.Minute,
			serve: answer},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var sent string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				sent = r.Header.Get("traceparent")
				mu.Unlock()
				tc.serve(w, r)
			}))
			defer server.Close()
			client, err := uq.NewClient(server.URL, "alarmd", server.Client())
			if err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			limiter, err := observability.NewScopedLogLimiter(observability.ScopedLogLimiterConfig{Window: time.Hour, MaxEvents: 100, MaxScopes: 1024})
			if err != nil {
				t.Fatal(err)
			}
			policy, err := observability.NewScopedBoundedLogPolicy(limiter)
			if err != nil {
				t.Fatal(err)
			}
			var line observability.Observation
			logged := observability.NewLoggingObserver(observability.New("alarmd", &output), policy)
			c := &SlotExecutionCoordinator{ports: Ports{Observer: observability.ObserverFunc(func(ctx context.Context, o observability.Observation) {
				line = o
				logged.Observe(ctx, o)
			})}}

			done, err := client.Execute(context.Background(), traceLineAttempt(t, tc.deadline), slowSink{delay: tc.delay})
			// The worker's two query lines: a failure the query returned, and
			// a completion the provider gave.
			switch {
			case err != nil:
				c.observeQueryFailure(context.Background(), execution.OperationNormal, time.Now(), "execute", err)
			case done.Completeness == execution.CompletenessUnavailable:
				c.observeQueryCompleted(context.Background(), execution.OperationNormal, time.Now(),
					execution.QueryExecutionCompletion{PhysicalQueries: []execution.PhysicalQueryCompletion{
						{Completeness: done.Completeness, RouteFacts: done.RouteFacts}}},
					execution.EvaluationResult{Result: observability.ResultDegraded, ReasonCode: done.RouteFacts.Attempts[0].ReasonCode})
			default:
				c.observeQueryCompleted(context.Background(), execution.OperationNormal, time.Now(),
					execution.QueryExecutionCompletion{PhysicalQueries: []execution.PhysicalQueryCompletion{
						{Completeness: done.Completeness, RouteFacts: done.RouteFacts}}}, execution.EvaluationResult{})
			}

			mu.Lock()
			parts := strings.Split(sent, "-")
			mu.Unlock()
			if len(parts) != 4 || len(parts[1]) != 32 {
				t.Fatalf("sent traceparent %v", parts)
			}
			if tc.reason == "" {
				if line.Result != observability.ResultSuccess || line.QueryFailure != nil || strings.Contains(output.String(), "failure_provider_trace_id") {
					t.Fatalf("a successful query's line %s failure %+v wrote %s", line.Result, line.QueryFailure, output.String())
				}
				return
			}
			if string(line.ReasonCode) != tc.reason {
				t.Fatalf("query line reason %s, want %s (err %v, failure %+v)", line.ReasonCode, tc.reason, err, line.QueryFailure)
			}
			if got := renderedQueryLine(t, output.Bytes())["failure_provider_trace_id"]; got != parts[1] {
				t.Fatalf("failure_provider_trace_id = %v, want the sent trace id %s; line %s", got, parts[1], output.String())
			}
		})
	}
}

// renderedQueryLine is the access query_completed line written to output.
func renderedQueryLine(t *testing.T, output []byte) map[string]any {
	t.Helper()
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		var line map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatalf("line %q: %v", scanner.Text(), err)
		}
		if line["component"] == observability.ComponentAccess && line["stage"] == observability.StageQueryCompleted {
			return line
		}
	}
	t.Fatalf("no query_completed line written:\n%s", output)
	return nil
}
