// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"context"
	"errors"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func manualCloseReadFailures(t *testing.T, recorder *Recorder) map[string]float64 {
	t.Helper()
	families, err := recorder.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	read := map[string]float64{}
	for _, family := range families {
		if family.GetName() != "alarmd_worker_no_data_manual_close_read_failures_total" {
			continue
		}
		for _, series := range family.GetMetric() {
			for _, pair := range series.GetLabel() {
				if pair.GetName() == "site" {
					read[pair.GetValue()] = series.GetCounter().GetValue()
				}
			}
		}
	}
	return read
}

// The output check's own line moves the counters, through the recorder and
// its component/stage catalog; an event_acked or no_data_decided line with
// no check on it moves nothing.
func TestTheManualCloseCheckLineMovesItsCounters(t *testing.T) {
	recorder := NewRecorder(BuildInfo{Version: "0.2.9999", Commit: "0123456789abcdef", SchemaVersion: "v3"})
	ctx := context.Background()
	recorder.Observe(ctx, observability.Observation{
		Component: observability.ComponentOutput, Stage: observability.StageNoDataManualCloseChecked,
		Result:            observability.ResultSuccess,
		NoDataManualClose: &observability.NoDataManualCloseFacts{Dropped: 2},
	})
	recorder.Observe(ctx, observability.Observation{
		Component: observability.ComponentOutput, Stage: observability.StageNoDataManualCloseChecked,
		Result:     observability.ResultFailed,
		ReasonCode: observability.ReasonCode(contract.ReasonRedisUnavailable), Err: errors.New("redis down"),
		NoDataManualClose: &observability.NoDataManualCloseFacts{CheckFailed: true},
	})
	recorder.Observe(ctx, observability.Observation{
		Component: observability.ComponentState, Stage: observability.StageNoDataMemoryRead,
		Result:           observability.ResultSuccess,
		NoDataMemoryRead: &observability.NoDataMemoryReadFacts{Representation: "PER_GROUP", ManualClosesUnread: true},
	})
	recorder.Observe(ctx, observability.Observation{
		Component: observability.ComponentOutput, Stage: observability.StageEventACKed,
		Result: observability.ResultSuccess,
	})

	if got := recorderCounterValue(t, recorder, "alarmd_worker_no_data_manual_close_dropped_total"); got != 2 {
		t.Fatalf("dropped = %v, want the two alerts the check line held back", got)
	}
	failures := manualCloseReadFailures(t, recorder)
	if failures["output_check"] != 1 || failures["memory_load"] != 1 || len(failures) != 2 {
		t.Fatalf("read failures = %+v, want one at each site", failures)
	}
}
