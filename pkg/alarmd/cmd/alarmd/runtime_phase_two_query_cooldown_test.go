// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

func TestQueryCooldownIndexInvalidatesWithoutClearingRetry(t *testing.T) {
	at := time.Unix(100, 0)
	recorder := metric.NewRecorder(metric.BuildInfo{})
	index := newPhaseTwoDueIndex(recorder)
	query := &phaseTwoQueryGroupLifecycle{}
	retry := &phaseTwoQueryGroupLifecycle{}
	index.Record("query", query, 0, scheduler.RunnerDueBound{QueryCooldown: true, NotDueUntilUnix: 200}, at)
	index.Record("retry", retry, 0, scheduler.RunnerDueBound{Deferred: true, NotDueUntilUnix: 200}, at)
	index.RecordSkip(recorder, false, "query")
	if got := counterValue(t, recorder, "bkmonitor_alarmd_dispatch_skipped_total", map[string]string{"reason": "query_cooldown"}); got != 1 {
		t.Fatal(got)
	}
	index.mu.Lock()
	index.expireScheduleBoundsLocked(at.Unix())
	index.mu.Unlock()
	if due, _, _, _ := index.Predict("query", query, at); !due {
		t.Fatal("publication did not revalidate query cooldown")
	}
	if due, _, _, _ := index.Predict("retry", retry, at); due {
		t.Fatal("publication cleared execution retry")
	}
}
