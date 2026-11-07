// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metricmeta

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// 分布在不同小时的版本分代到期，同一小时内的冷启动批次仍会集中到期。
func TestRotationCohortExpiry(t *testing.T) {
	const cohortSize = 1000
	for _, tc := range []struct {
		name           string
		secondCohortAt time.Duration
		wantAtTwoHours int
	}{
		{"same_hour", 30 * time.Minute, 2 * cohortSize},
		{"separate_hours", time.Hour, cohortSize},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sent := 0
			e := testExporter(t, func(_ context.Context, body []byte) error {
				var rows []Entry
				if err := json.Unmarshal(body, &rows); err != nil {
					return err
				}
				sent += len(rows)
				return nil
			})
			start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			now := start
			e.store.now = func() time.Time { return now }
			cohort := func(id int) {
				for i := 0; i < cohortSize; i++ {
					row := testEntry()
					row.Key.Metric = fmt.Sprintf("cohort_%d_metric_%d", id, i)
					observe(t, e, row)
				}
			}
			cohort(0)
			flush(t, e)
			now = start.Add(tc.secondCohortAt)
			cohort(1)
			flush(t, e)
			if sent != 2*cohortSize {
				t.Fatalf("initial export: %d", sent)
			}
			now = start.Add(time.Hour)
			cohort(0)
			cohort(1)
			flush(t, e)
			if sent != 2*cohortSize {
				t.Fatalf("one-hour rotation expired live generation: %d", sent)
			}
			now = start.Add(2 * time.Hour)
			cohort(0)
			cohort(1)
			if got := e.store.Stats().Pending; got != tc.wantAtTwoHours {
				t.Fatalf("expired cohort: got %d, want %d", got, tc.wantAtTwoHours)
			}
			before := sent
			flush(t, e)
			if sent-before != tc.wantAtTwoHours {
				t.Fatalf("re-exported %d entries", sent-before)
			}
			t.Logf("at 2h: %d/%d entries expire together", sent-before, 2*cohortSize)
		})
	}
}
