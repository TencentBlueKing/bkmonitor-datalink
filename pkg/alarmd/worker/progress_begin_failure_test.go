// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/progress"
)

// A BeginSlot error never becomes BLOCKED_EXACT_SET_UNAVAILABLE. Its text and
// the Slot coordinates reach the log, and the reason names the cause class.
func TestSlotExecutionCoordinatorReportsBeginSlotFailureWithErrorText(t *testing.T) {
	for _, test := range []struct {
		name       string
		err        error
		wantReason string
	}{
		{
			name:       "deterministic persisted fact",
			err:        &progress.DeterministicInvalidError{Err: errors.New("unfinished Slot projection differs from persisted facts")},
			wantReason: contract.ReasonProgressBeginFailed,
		},
		{
			name:       "request validation",
			err:        errors.New("progress: invalid BeginSlot identity"),
			wantReason: contract.ReasonProgressBeginFailed,
		},
		{
			name:       "transport failure",
			err:        &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")},
			wantReason: contract.ReasonProgressBeginRejected,
		},
		{
			name:       "context deadline",
			err:        context.DeadlineExceeded,
			wantReason: contract.ReasonProgressBeginRejected,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			limiter, err := observability.NewWindowLogLimiter(observability.WindowLogLimiterConfig{Window: time.Hour, MaxEvents: 16})
			if err != nil {
				t.Fatal(err)
			}
			policy, err := observability.NewBoundedLogPolicy(limiter)
			if err != nil {
				t.Fatal(err)
			}
			logging := observability.NewLoggingObserver(observability.New("alarmd", &output), policy)
			var observations []observability.Observation
			observer := observability.ObserverFunc(func(ctx context.Context, observation observability.Observation) {
				observations = append(observations, observability.NormalizeObservation(observation))
				logging.Observe(ctx, observation)
			})
			fixture := newFixtureWithObserver(t, true, "", observer)
			fixture.ports.beginErr = test.err

			request := slotRequest(execution.OperationNormal)
			result, err := fixture.coordinator.Execute(context.Background(), request)
			if err != nil || result.Completed || result.Result != observability.ResultRetrying ||
				result.ReasonCode != execution.ReasonCode(test.wantReason) {
				t.Fatalf("Execute() = (%+v, %v), want retrying with reason %s", result, err, test.wantReason)
			}
			if len(*fixture.trace) != 0 {
				t.Fatalf("trace after BeginSlot failure = %v, want no side effect stage", *fixture.trace)
			}
			var progressObservation *observability.Observation
			for index := range observations {
				observation := observations[index]
				if observation.ReasonCode == observability.ReasonCode(contract.ReasonBlockedExactSetUnavailable) {
					t.Fatalf("BeginSlot failure observed as %+v", observation)
				}
				if observation.Component == observability.ComponentProgress && observation.Stage == observability.StageProgressCommitted {
					progressObservation = &observation
				}
			}
			if progressObservation == nil || progressObservation.Result != observability.Result(observability.ResultFailed) ||
				progressObservation.ReasonCode != observability.ReasonCode(test.wantReason) || progressObservation.Err != test.err {
				t.Fatalf("Progress observation = %+v, want failed %s carrying the BeginSlot error", progressObservation, test.wantReason)
			}
			logged := output.String()
			for _, want := range []string{
				`"reason_code":"` + test.wantReason + `"`,
				`"error":"` + strings.ReplaceAll(test.err.Error(), `"`, `\"`) + `"`,
				`"query_group_key":"` + string(request.Contract.Slot.QueryGroup) + `"`,
				`"evaluation_time":` + strconv.FormatInt(int64(request.Contract.Slot.EvaluationTime), 10),
				`"schedule_segment_start":` + strconv.FormatInt(int64(request.Contract.ScheduleSegmentStart), 10),
			} {
				if !strings.Contains(logged, want) {
					t.Fatalf("log line missing %s:\n%s", want, logged)
				}
			}
		})
	}
}
