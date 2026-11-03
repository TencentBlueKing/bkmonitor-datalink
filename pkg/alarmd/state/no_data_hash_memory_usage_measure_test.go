// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// measureCapacityEnv names the switch that runs the no-data capacity
// measurements. They log what the server holds and judge nothing, so they
// stay out of the gate.
const measureCapacityEnv = "ALARMD_MEASURE_NO_DATA_CAPACITY"

// TestMeasureNoDataHashMemoryAgainstTheCapacityFormula reads the memory a
// per-group record takes on the server, MEMORY USAGE on the key, beside
// decision-008 section 7.2 (a)'s formula: groups times the bytes of one group,
// plus the header.
//
// The section holds the formula to 10% of MEMORY USAGE or of the HGETALL
// bytes. TestANoDataHashHoldsTheBytesTheCapacityFormulaPredictsOnARealServer
// holds it to the HGETALL bytes, where it agrees. Against MEMORY USAGE it does
// not, and this is the reading of by how much. Redis 5 keeps a hash compact
// (ziplist) and Redis 7 (listpack) only while every value is at most 64 bytes,
// and the header alone is several hundred, so every no-data record is a hash
// table on either server whatever its group count, and each group carries the
// table's per-entry cost on top of its bytes. The 100-group arm shows that a
// small record is no exception. The readings are in decision-008 section 7.2
// as the memory the representation costs.
//
// It is a measurement and runs only with ALARMD_MEASURE_NO_DATA_CAPACITY=1,
// once per Redis major (ALARMD_REDIS_SERVER); it logs and does not judge.
func TestMeasureNoDataHashMemoryAgainstTheCapacityFormula(t *testing.T) {
	if os.Getenv(measureCapacityEnv) != "1" {
		t.Skipf("a measurement; set %s=1 to run it (once per Redis major)", measureCapacityEnv)
	}
	absence, err := json.Marshal(noDataGroupAbsenceValue{LastSeen: 900, FirstAbsent: 940})
	if err != nil {
		t.Fatal(err)
	}
	for _, groups := range []int{100, 3200} {
		for _, present := range []bool{true, false} {
			name := fmt.Sprintf("%d groups, every group absent", groups)
			value := len(absence)
			if present {
				name, value = fmt.Sprintf("%d groups, every group present", groups), 1
			}
			t.Run(name, func(t *testing.T) {
				server := startNoDataServer(t)
				ctx := context.Background()
				memory := presentHostGroups(groups)
				predicted := 0
				for index, group := range memory {
					if !present {
						memory[index] = execution.NoDataGroupMemory{GroupKey: group.GroupKey, LastSeen: 900, FirstAbsent: 940}
					}
					predicted += len(noDataGroupPrefix) + len(group.GroupKey) + value
				}
				if got := storeNoData(t, server.store(t), firstNoDataWrite(t, 0, memory...)); got.Status != execution.NoDataApplied {
					t.Fatalf("write = %+v", got)
				}
				key := noDataHashKey(t)
				predicted += len(noDataHeaderField) + len(server.client.HGet(ctx, key, noDataHeaderField).Val())
				usage, err := server.client.MemoryUsage(ctx, key, 0).Result()
				if err != nil {
					t.Fatal(err)
				}
				encoding := server.client.ObjectEncoding(ctx, key).Val()
				version := strings.TrimSpace(firstLines(server.client.Info(ctx, "server").Val(), "redis_version"))
				t.Logf("%s %s: encoding %s, MEMORY USAGE %d bytes, formula %d, %+.1f%%",
					version, name, encoding, usage, predicted, 100*(float64(usage)-float64(predicted))/float64(predicted))
			})
		}
	}
}
