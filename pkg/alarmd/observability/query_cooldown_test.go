// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestQueryCooldownStructuredLog(t *testing.T) {
	var output bytes.Buffer
	facts := &QueryCooldownFacts{Event: "entered", Until: time.Unix(1000, 0), LastQueryAt: time.Unix(900, 0), Failures: 2}
	newQueryFailureTestObserver(&output).Observe(context.Background(), Observation{Component: ComponentScheduler, Stage: StageQueryCooldown, Result: ResultDegraded, Trace: TraceFields{QueryGroupKey: "qg"}, QueryCooldown: facts})
	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	f, ok := event["query_cooldown"].(map[string]any)
	if !ok || f["event"] != "entered" || f["failures"] != float64(2) || event["stage"] != StageQueryCooldown {
		t.Fatalf("lost facts: %v", event)
	}
	if !ValidRunOutcome("query_cooldown") {
		t.Fatal("cooldown becomes other_error")
	}
}
