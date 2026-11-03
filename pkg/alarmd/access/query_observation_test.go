// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package access

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A physical query that did not answer is classified by what its last
// failed attempt says: timed out, refused by the backend, about something
// that does not exist, cut in transport, an answer that could not be read,
// or other. One that answered, wholly or in part, is no failure.
func TestAFailedQueryIsClassifiedByItsLastAttempt(t *testing.T) {
	unavailable := execution.ReasonCode(contract.ReasonQueryUnavailable)
	for _, test := range []struct {
		completion execution.ProviderCompletion
		err        error
		want       string
	}{
		{failedAttempt(execution.ReasonCode(contract.ReasonQueryTimeout), ""), nil, QueryFailureTimeout},
		{failedAttempt(unavailable, execution.TransportRouteDetail(execution.TransportFailureTimeout)), nil, QueryFailureTimeout},
		{failedAttempt(unavailable, execution.HTTPStatusRouteDetail(504)), nil, QueryFailureTimeout},
		{failedAttempt(unavailable, execution.HTTPStatusRouteDetail(429)), nil, QueryFailureRefused},
		{failedAttempt(unavailable, execution.HTTPStatusRouteDetail(400)), nil, QueryFailureRefused},
		{failedAttempt(unavailable, execution.HTTPStatusRouteDetail(503)), nil, QueryFailureTransport},
		{failedAttempt(unavailable, execution.TransportRouteDetail(execution.TransportFailureConnectionRefused)), nil, QueryFailureTransport},
		{failedAttempt(unavailable, execution.BodyRouteDetail(execution.TransportFailureEOF)), nil, QueryFailureTransport},
		{failedAttempt(unavailable, execution.ResponseStatusRouteDetail("SPACE_TABLE_ID_FIELD_IS_NOT_EXISTS")), nil, QueryFailureNotFound},
		{failedAttempt(unavailable, execution.ResponseStatusRouteDetail("TABLE_NOT_FOUND")), nil, QueryFailureNotFound},
		{failedAttempt(unavailable, execution.ResponseStatusRouteDetail("QUERY_SYNTAX_ERROR")), nil, QueryFailureRefused},
		{failedAttempt(unavailable, execution.ResponseRouteDetail(execution.ResponseFailureFrameInvalid)), nil, QueryFailureDecode},
		{failedAttempt(unavailable, ""), nil, QueryFailureOther},
		{execution.ProviderCompletion{}, context.DeadlineExceeded, QueryFailureTimeout},
		{execution.ProviderCompletion{}, errors.New("provider broke"), QueryFailureOther},
		{execution.ProviderCompletion{Completeness: execution.CompletenessFull}, nil, ""},
		{execution.ProviderCompletion{Completeness: execution.CompletenessPartial}, nil, ""},
	} {
		got := queryFailureOf(test.completion, test.err)
		if got != test.want {
			t.Fatalf("%+v / %v: failure %q, want %q", test.completion.RouteFacts.Attempts, test.err, got, test.want)
		}
		if got != "" && !slices.Contains(QueryFailures, got) {
			t.Fatalf("%q is not in the closed list", got)
		}
	}
}

type observedQuery struct {
	elapsed time.Duration
	failure string
}

// Every physical query is heard once, with the time the provider took and
// how it failed, if it did: the one place the query count, its duration and
// its failures are all taken, so they agree.
func TestEveryPhysicalQueryIsHeardOnceWithItsTimeAndFailure(t *testing.T) {
	var heard []observedQuery
	released := 0
	answered := execution.ProviderCompletion{Completeness: execution.CompletenessFull, Stats: execution.ProviderStats{QueryMillis: 1500}}
	refused := failedAttempt(execution.ReasonCode(contract.ReasonQueryUnavailable), execution.HTTPStatusRouteDetail(400))
	refused.Stats.QueryMillis = 20
	for _, completion := range []execution.ProviderCompletion{answered, refused} {
		source := &Source{provider: distressProvider{completion: completion}, config: Config{
			ObserveQuery: func(elapsed time.Duration, failure string) { heard = append(heard, observedQuery{elapsed, failure}) }}}
		if _, err := source.executeWithPermit(context.Background(), execution.QueryAttempt{}, nil, waitedPermit{released: &released}); err != nil {
			t.Fatal(err)
		}
	}
	want := []observedQuery{{1500 * time.Millisecond, ""}, {20 * time.Millisecond, QueryFailureRefused}}
	if !slices.Equal(heard, want) {
		t.Fatalf("heard %+v, want %+v", heard, want)
	}
}

// bodyFailure is shaped as the UQ client's failure after an answer began:
// an error, not a completion, carrying its detail and its timing.
type bodyFailure struct {
	detail string
	timing *execution.AttemptTiming
}

func (f bodyFailure) Error() string                                { return "body failed: " + f.detail }
func (f bodyFailure) QueryFailureDetail() string                   { return f.detail }
func (f bodyFailure) QueryFailureTiming() *execution.AttemptTiming { return f.timing }

type failingProvider struct {
	err   error
	sleep time.Duration
}

func (provider failingProvider) Execute(context.Context, execution.QueryAttempt, execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
	time.Sleep(provider.sleep) // fixed-wait: the provider's answer time, which the case measures
	return execution.ProviderCompletion{}, provider.err
}

// A query that failed after its answer began comes back as an error, not a
// completion: it is classified by the detail the error carries and timed by
// the error's own timing, or by the call itself when it has none -- never
// at zero. A caller that gave up is not a failure of the query and is not
// heard.
func TestAQueryThatFailedAsAnErrorIsClassifiedAndTimedByIt(t *testing.T) {
	var heard []observedQuery
	released := 0
	observe := Config{ObserveQuery: func(elapsed time.Duration, failure string) { heard = append(heard, observedQuery{elapsed, failure}) }}
	for _, provider := range []failingProvider{
		{err: bodyFailure{detail: execution.BodyRouteDetail(execution.TransportFailureTimeout), timing: &execution.AttemptTiming{ElapsedMillis: 30_004}}},
		{err: fmt.Errorf("wrapped: %w", bodyFailure{detail: execution.BodyRouteDetail(execution.TransportFailureConnectionReset)}), sleep: 20 * time.Millisecond},
		{err: bodyFailure{detail: execution.DeliveryTimeoutRouteDetail, timing: &execution.AttemptTiming{ElapsedMillis: 12_000}}},
		{err: fmt.Errorf("step down: %w", context.Canceled)},
	} {
		source := &Source{provider: provider, config: observe}
		_, _ = source.executeWithPermit(context.Background(), execution.QueryAttempt{}, nil, waitedPermit{released: &released})
	}
	if len(heard) != 3 {
		t.Fatalf("heard %+v, want three: the cancelled one is not a failure of the query", heard)
	}
	if heard[0] != (observedQuery{30_004 * time.Millisecond, QueryFailureTimeout}) {
		t.Fatalf("body timeout heard as %+v, want 30.004 s timeout from its own timing", heard[0])
	}
	if heard[1].failure != QueryFailureTransport || heard[1].elapsed < 20*time.Millisecond {
		t.Fatalf("body reset heard as %+v, want transport timed by the call", heard[1])
	}
	if heard[2] != (observedQuery{12 * time.Second, QueryFailureTimeout}) {
		t.Fatalf("delivery timeout heard as %+v, want a 12 s timeout", heard[2])
	}
}
