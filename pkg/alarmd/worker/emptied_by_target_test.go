// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker_test

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The round's completion line says an empty primary was the target's doing
// only when every series the query returned was refused as outside it: a
// query that returned nothing, or one a series of which was refused for
// another reason, says nothing of the kind.
func TestAnEmptyPrimaryIsEmptiedByTheTargetOnlyWhenEveryWithheldSeriesWasOutsideIt(t *testing.T) {
	for _, test := range []struct {
		name     string
		withheld [2]uint64
		want     bool
	}{
		{"every series outside the target", [2]uint64{2, 2}, true},
		{"one series withheld for another reason", [2]uint64{2, 1}, false},
		{"nothing returned", [2]uint64{0, 0}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newFixture(t, true, "")
			withheld := test.withheld
			fixture.ports.withheld = &withheld
			result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
			if err != nil || !result.Completed {
				t.Fatalf("Execute() result=%+v error=%v", result, err)
			}
			var primary *observability.PrimaryInputFacts
			for _, observation := range *fixture.observations {
				if observation.Stage == observability.StageProgressCommitted {
					primary = observation.PrimaryInput
				}
			}
			if primary == nil || primary.Completeness != "FULL" || primary.DataState != "EMPTY" || primary.EmptiedByTarget != test.want {
				t.Fatalf("committed primary %+v, want FULL, EMPTY and emptied by the target = %v", primary, test.want)
			}
		})
	}
}
