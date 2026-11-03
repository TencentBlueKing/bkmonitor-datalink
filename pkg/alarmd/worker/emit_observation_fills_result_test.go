// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// emitObservation is a filler the label set scan relies on
// (observability's observationFillers): an Observation handed to it with no
// result is written with one - failed with an error, success without - so a
// site that leaves the result to it never reads _other. If it stops filling
// either branch, this fails rather than the scan trusting it.
func TestEmitObservationFillsAnEmptyResult(t *testing.T) {
	for _, testCase := range []struct {
		name string
		err  error
		want observability.Result
	}{
		{"with an error", errors.New("refused"), observability.ResultFailed},
		{"without one", nil, observability.ResultSuccess},
	} {
		var got []observability.Observation
		coordinator := &SlotExecutionCoordinator{ports: Ports{Observer: observability.ObserverFunc(
			func(_ context.Context, observation observability.Observation) { got = append(got, observation) })}}
		coordinator.emitObservation(context.Background(), observability.Observation{
			Component: observability.ComponentState, Stage: observability.StageMutationCompared, Err: testCase.err,
		})
		if len(got) != 1 || got[0].Result != testCase.want {
			t.Fatalf("%s: emitted %+v, want one observation with result %q", testCase.name, got, testCase.want)
		}
	}
}
