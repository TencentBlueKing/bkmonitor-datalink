// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
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
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A query whose request was cancelled when the round gave up at the query's
// deadline, or whose context expired at it, ran out of its time: it is the
// query's timeout, with its error text and cause kept.
func TestAQueryEndedAtItsOwnDeadlineIsItsTimeout(t *testing.T) {
	deadline := time.Unix(1_700_124_055, 0)
	for name, cause := range map[string]error{
		"cancelled": fmt.Errorf("alarmd access: execute physical query: %w", fmt.Errorf("alarmd access uq: execute request: %w", context.Canceled)),
		"expired":   fmt.Errorf("alarmd access: execute physical query: %w", context.DeadlineExceeded),
	} {
		err := endedByDeadline(cause, deadline.UnixMilli(), deadline.Add(time.Millisecond))
		var declared interface{ QueryFailure() (string, string) }
		if !errors.As(err, &declared) {
			t.Fatalf("%s: %v names no failure", name, err)
		}
		if category, code := declared.QueryFailure(); category != observability.QueryFailureCategoryProviderTransport || code != contract.ReasonQueryTimeout {
			t.Fatalf("%s: %s/%s, want the query's timeout", name, category, code)
		}
		if err.Error() != cause.Error() || !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("%s: %v lost its text or cause", name, err)
		}
		// It names its branch, so its line says the round filed it.
		var sourced interface{ QueryFailureTimeoutSource() string }
		if !errors.As(err, &sourced) || sourced.QueryFailureTimeoutSource() != observability.QueryTimeoutSourceRoundDeadline {
			t.Fatalf("%s: %v names no round_deadline timeout source", name, err)
		}
	}
}

// A cancellation before the query's deadline is not its timeout - the
// process stopping, the lease lost, another query failing first - and keeps
// its error; so does a failure that is no cancellation, and one that already
// names its cause.
func TestACancellationBeforeTheDeadlineIsNotATimeout(t *testing.T) {
	deadline := time.Unix(1_700_124_055, 0)
	cancelled := fmt.Errorf("alarmd access uq: execute request: %w", context.Canceled)
	other := errors.New("alarmd access uq: decode response")
	named := &queryPermitDeadlineError{err: context.DeadlineExceeded}
	for name, tc := range map[string]struct {
		err error
		at  time.Time
	}{
		"cancelled before the deadline": {cancelled, deadline.Add(-time.Second)},
		"not a cancellation":            {other, deadline.Add(time.Second)},
		"already named":                 {named, deadline.Add(time.Second)},
	} {
		got := endedByDeadline(tc.err, deadline.UnixMilli(), tc.at)
		var deadlineErr *queryDeadlineError
		if errors.As(got, &deadlineErr) || got != tc.err {
			t.Fatalf("%s: %v (%T), want the error unchanged", name, got, got)
		}
	}
	if got := endedByDeadline(cancelled, 0, deadline); got != cancelled {
		t.Fatalf("no deadline: %v", got)
	}
}

// cancelledProvider is a query backend whose request is cancelled: it lets
// the given time pass first, then fails as a request whose context ended.
type cancelledProvider struct{ pass func() }

func (provider cancelledProvider) Execute(context.Context, execution.QueryAttempt, execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
	provider.pass()
	return execution.ProviderCompletion{}, fmt.Errorf("alarmd access uq: execute request: %w", context.Canceled)
}

// Through a whole execution: a request cancelled once its query's deadline
// had passed comes out as the query's timeout, and one cancelled while the
// query still had time comes out as it was.
func TestAnExecutionWhoseRequestIsCancelledAtTheDeadlineReportsATimeout(t *testing.T) {
	contractRef, frozen := frozenExecution(t)
	execute := func(t *testing.T, pass time.Duration) error {
		t.Helper()
		// Ten seconds into the Slot, whose query deadline is at twenty-five.
		clock := time.Unix(int64(contractRef.Slot.EvaluationTime)+10, 0)
		source, err := NewSource(staticFrozenPlan{plan: frozen}, cancelledProvider{pass: func() { clock = clock.Add(pass) }},
			&recordingQueryPermits{}, Config{MinReadyDelay: time.Second})
		if err != nil {
			t.Fatal(err)
		}
		source.now = func() time.Time { return clock }
		source.wait = func(context.Context, time.Duration) error { return nil }
		_, err = source.Execute(context.Background(), execution.QueryExecutionRequest{
			Contract: contractRef, Operation: execution.OperationNormal, AttemptNo: 1,
		}, &recordingConsumer{})
		return err
	}
	var declared interface{ QueryFailure() (string, string) }
	err := execute(t, 26*time.Second)
	if !errors.As(err, &declared) || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled past the deadline: %v, want the query's timeout", err)
	}
	if category, code := declared.QueryFailure(); category != observability.QueryFailureCategoryProviderTransport || code != contract.ReasonQueryTimeout {
		t.Fatalf("cancelled past the deadline: %s/%s", category, code)
	}
	if err := execute(t, 0); err == nil || errors.As(err, &declared) {
		t.Fatalf("cancelled in time: %v, want an unclassified error", err)
	}
}
