// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	uq "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/access/uq"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// How a sliced source's read runs out of time, as production wiring returns
// it (access.Source over uq.Client): the provider's own timeout, UQ's 504,
// comes back from the client as an UNAVAILABLE completion with the request
// it answered, and no error; alarmd's deadline ending a read while its
// answer's body is read comes back as the client's error. The deadline
// ending a round or a permit wait are access.Source's, and are read through
// it, in the production bundle's cases.
type slicedTimeout int

const (
	// timeoutProvider is UQ answering 504 (the default).
	timeoutProvider slicedTimeout = iota
	// timeoutBody is the deadline passing while the answer's body is read:
	// the client's own body failure, from a real request.
	timeoutBody
)

// slicedProvider is a real uq.Client over an httptest server, for the
// sliced source's reads that run out of time: one answering 504 at once, and
// one that begins its answer - the shared format's header - and holds it
// until the request ends.
type slicedProvider struct {
	once           sync.Once
	timeout, stall *uq.Client
}

func (provider *slicedProvider) clients(t *testing.T) (*uq.Client, *uq.Client) {
	provider.once.Do(func() {
		gateway := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusGatewayTimeout)
		}))
		stalled := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", uq.SharedSchemaMediaType)
			writer.WriteHeader(http.StatusOK)
			_, _ = writer.Write([]byte(workerSharedHeader))
			writer.(http.Flusher).Flush()
			<-request.Context().Done()
		}))
		t.Cleanup(gateway.Close)
		t.Cleanup(stalled.Close)
		var err error
		if provider.timeout, err = uq.NewClient(gateway.URL, "alarmd", gateway.Client()); err != nil {
			t.Fatal(err)
		}
		if provider.stall, err = uq.NewClient(stalled.URL, "alarmd", stalled.Client()); err != nil {
			t.Fatal(err)
		}
	})
	return provider.timeout, provider.stall
}

// timedOutRead is a read that ran out of time, as the source's kind says:
// an UNAVAILABLE completion for the provider's 504, an error otherwise.
func (source *slicedSource) timedOutRead(ctx context.Context, request execution.QueryExecutionRequest, consumer execution.QueryExecutionConsumer, call *slicedCall) (execution.QueryExecutionCompletion, error) {
	header := source.headerFor(request)
	if err := consumer.Begin(ctx, header); err != nil {
		return execution.QueryExecutionCompletion{}, err
	}
	gateway, stalled := source.provider.clients(source.t)
	spec := codecWorkerSpec(source.t)
	attempt := execution.QueryAttempt{Spec: spec, Slot: request.Contract.Slot, Operation: request.Operation, AttemptNo: 1,
		DeadlineUnixMilli: time.Now().Add(time.Minute).UnixMilli()}
	if source.timeoutKind == timeoutBody {
		attempt.DeadlineUnixMilli = time.Now().Add(50 * time.Millisecond).UnixMilli()
		_, err := stalled.Execute(ctx, attempt, discardingProviderSink{})
		if err == nil {
			source.t.Fatal("a read held past its deadline completed")
		}
		call.err = err
		return execution.QueryExecutionCompletion{}, err
	}
	done, err := gateway.Execute(ctx, attempt, discardingProviderSink{})
	if err != nil {
		source.t.Fatalf("a 504 came back as an error: %v", err)
	}
	if done.Completeness != execution.CompletenessUnavailable {
		source.t.Fatalf("a 504 completed %v, want UNAVAILABLE", done.Completeness)
	}
	queryRevision := header.RequiredPhysicalQueries[0].QueryRevision
	physical := execution.PhysicalQueryCompletion{Ref: "provider-result-1", PhysicalQuery: "physical-query-1",
		QueryRevision: queryRevision, Completeness: done.Completeness, DataState: done.DataState,
		RouteFacts: done.RouteFacts, Stats: done.Stats}
	completion := execution.QueryExecutionCompletion{AllRequiredCompleted: true,
		PhysicalQueries: []execution.PhysicalQueryCompletion{physical}}
	completion.CompletionBindings = accessShapedCompletionBindings(source.t, header, completion.PhysicalQueries)
	call.timedOut = true
	return completion, nil
}

// discardingProviderSink takes nothing: a read that runs out of time
// delivers none.
type discardingProviderSink struct{}

func (discardingProviderSink) ConsumeProviderSeries(context.Context, execution.ProviderSeriesBatch) error {
	return errors.New("alarmd test: a read out of time delivered series")
}

// readWholeTimedOut runs the Slot whose read of the whole Query Group the
// provider times out: as production completes it, degraded, its query
// unavailable, not failed.
func readWholeTimedOut(t *testing.T, fixture *slicedFixture, request execution.SlotExecutionRequest) {
	t.Helper()
	result, err := fixture.coordinator.Execute(context.Background(), request)
	if err != nil || !result.Completed || result.QueryAvailability != execution.QueryAvailabilityUnavailable {
		t.Fatalf("the read whole the provider timed out: result=%+v err=%v, want it completed, its query unavailable", result, err)
	}
}
