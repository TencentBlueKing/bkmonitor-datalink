// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// Every wait is measured; only a slow one is written down.
//
// Both halves matter and they pull against each other. A Slot attempt that is
// stuck reports nothing today -- nothing failed, so nothing fails -- and the
// only readable fact is a gap between two timestamps that cannot say which of
// several waits it was. So every wait has to be measured. But the ordinary
// wait is a few milliseconds and there are thousands a second of them; a line
// for each would push out the lines that answer questions.
//
// A mechanism that always logs and one that never logs are equally useless
// here, so the discriminating case is the pair: the same wait, once under the
// threshold and once over it.
func TestOnlyASlowSlotWaitSpendsLogQuota(t *testing.T) {
	for _, test := range []struct {
		name    string
		elapsed time.Duration
		wantLog bool
		want    observability.Result
	}{
		{name: "ordinary", elapsed: 3 * time.Millisecond, want: observability.Result(observability.ResultSuccess)},
		{name: "just under the threshold", elapsed: observability.SlowSlotWait - time.Millisecond,
			want: observability.Result(observability.ResultSuccess)},
		{name: "at the threshold", elapsed: observability.SlowSlotWait, wantLog: true,
			want: observability.Result(observability.ResultDegraded)},
		{name: "the twenty-two seconds nothing could account for", elapsed: 22 * time.Second, wantLog: true,
			want: observability.Result(observability.ResultDegraded)},
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
			var measured []observability.Observation
			observer := observability.ObserverFunc(func(ctx context.Context, observation observability.Observation) {
				measured = append(measured, observation)
				logging.Observe(ctx, observation)
			})

			started := time.Unix(1_700_124_000, 0)
			observability.ObserveSlotWait(context.Background(), observer, observability.SlotWaitFinalization,
				observability.Operation("replay"), started, func() time.Time { return started.Add(test.elapsed) })

			// Measured in every case, which is what the histogram is for.
			if len(measured) != 1 || measured[0].SlotWait == nil ||
				measured[0].SlotWait.Wait != observability.SlotWaitFinalization ||
				measured[0].Duration != test.elapsed {
				t.Fatalf("measured %+v, want one %s wait of %s", measured, observability.SlotWaitFinalization, test.elapsed)
			}
			if measured[0].Result != test.want {
				t.Fatalf("result = %s, want %s", measured[0].Result, test.want)
			}

			logged := strings.Contains(output.String(), string(observability.StageSlotWait))
			if logged != test.wantLog {
				t.Fatalf("logged=%t for a %s wait, want %t. A line for every wait buries the ones worth "+
					"reading; no line for any of them leaves a stalled attempt with nothing to say",
					logged, test.elapsed, test.wantLog)
			}
			if test.wantLog && !strings.Contains(output.String(), observability.SlotWaitFinalization) {
				t.Fatalf("the line does not name the wait: %s", output.String())
			}
		})
	}
}

// A wait measured with no observer, a zero start, or a clock that went
// backwards reports nothing rather than a nonsense duration.
func TestASlotWaitWithNothingToMeasureReportsNothing(t *testing.T) {
	var measured []observability.Observation
	observer := observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		measured = append(measured, observation)
	})
	started := time.Unix(1_700_124_000, 0)

	observability.ObserveSlotWait(context.Background(), nil, observability.SlotWaitProgressBegin, "", started, time.Now)
	observability.ObserveSlotWait(context.Background(), observer, observability.SlotWaitProgressBegin, "", time.Time{}, time.Now)
	observability.ObserveSlotWait(context.Background(), observer, observability.SlotWaitProgressBegin,
		"", started, func() time.Time { return started.Add(-time.Second) })

	if len(measured) != 0 {
		t.Fatalf("measured %+v, want nothing", measured)
	}
}
