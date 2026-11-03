// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package nodata

import (
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// unparseableKey is a remembered key this package could not have written:
// every key it writes ends in the no-data tag.
const unparseableKey = "bk_target_cloud_id=0,bk_target_ip=192.0.2.9"

// A remembered key that does not parse back into a group fails the Plan's
// round by name, in both places a round needs the group back: where a history
// roster expects it, and where a round closes it after the target stopped
// naming it. Failed, the round writes nothing, so the memory keeps the group
// as it was. Each has its other side: the same memory with a key that parses
// is judged as usual.
func TestARememberedKeyThatDoesNotParseFailsTheRoundRatherThanDroppingTheGroup(t *testing.T) {
	parseable := hostTargetGroup(HostIdentity{IP: "192.0.2.9", CloudID: "0"}).Key()

	t.Run("expected by a history roster", func(t *testing.T) {
		for name, test := range map[string]struct {
			key    string
			failed bool
		}{"does not parse": {key: unparseableKey, failed: true}, "parses": {key: parseable}} {
			t.Run(name, func(t *testing.T) {
				input := planSlotInput(storedSnapshot(t, 940, execution.NoDataGroupMemory{GroupKey: test.key, LastSeen: 940}),
					presentSeries())
				input.Scope, input.KnownHosts, input.HostsResolved = nil, nil, false
				result, err := EvaluatePlanSlot(input)
				assertUnparseableRound(t, result, err, test.failed)
			})
		}
	})

	t.Run("closed after the target stopped naming it", func(t *testing.T) {
		for name, test := range map[string]struct {
			key    string
			failed bool
		}{"does not parse": {key: unparseableKey, failed: true}, "parses": {key: parseable}} {
			t.Run(name, func(t *testing.T) {
				input := planSlotInput(storedSnapshot(t, 940, execution.NoDataGroupMemory{
					GroupKey: test.key, LastSeen: 700, FirstAbsent: 880,
				}), presentSeries())
				result, err := EvaluatePlanSlot(input)
				assertUnparseableRound(t, result, err, test.failed)
				if test.failed {
					return
				}
				closed := false
				for _, series := range result.Series {
					closed = closed || (series.Group.Key() == test.key && series.Value == PresentValue)
				}
				if !closed {
					t.Fatalf("series = %+v, want the closing NORMAL for the group the target stopped naming", result.Series)
				}
			})
		}
	})
}

func assertUnparseableRound(t *testing.T, result PlanSlotResult, err error, failed bool) {
	t.Helper()
	if !failed {
		if err != nil {
			t.Fatalf("EvaluatePlanSlot() error = %v, want the round judged", err)
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), "does not parse back into a group") {
		t.Fatalf("EvaluatePlanSlot() error = %v, want the round refused for the key that does not parse", err)
	}
	if result.Mutation != nil || len(result.Series) != 0 {
		t.Fatalf("a refused round produced mutation %+v and series %+v, want nothing", result.Mutation, result.Series)
	}
}
