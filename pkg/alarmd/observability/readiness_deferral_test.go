// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
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
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// A QUERY_NOT_READY line names what it waits for, on both lines that report
// one, with its names bounded and its basis closed; the facts do not ride on
// any other line.
func TestANotReadyLineNamesWhatItWaitsFor(t *testing.T) {
	facts := &ReadinessDeferralFacts{Requirement: strings.Repeat("r", 200), PhysicalQuery: "physical-303",
		ReadyAtUnixMilli: 1_800_000_310_000, Basis: ReadinessBasisReadHold, ReadHoldMillis: 300_000}
	for _, stage := range []struct {
		component Component
		stage     Stage
	}{{ComponentAccess, StageQueryCompleted}, {ComponentScheduler, StageSlotCompleted}} {
		var output bytes.Buffer
		limiter, err := NewScopedLogLimiter(ScopedLogLimiterConfig{Window: time.Hour, MaxEvents: 100, MaxScopes: 1024})
		if err != nil {
			t.Fatal(err)
		}
		policy, err := NewScopedBoundedLogPolicy(limiter)
		if err != nil {
			t.Fatal(err)
		}
		observer := NewLoggingObserver(New("alarmd", &output), policy)
		observer.Observe(context.Background(), NormalizeObservation(Observation{Component: stage.component, Stage: stage.stage,
			Result: ResultRetrying, ReasonCode: ReasonCode(contract.ReasonQueryNotReady), Duration: time.Millisecond,
			ReadinessDeferral: facts}))
		var line map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &line); err != nil {
			t.Fatalf("%s: %v: %s", stage.stage, err, output.String())
		}
		want := map[string]any{"not_ready_query": "physical-303", "not_ready_at_ms": float64(1_800_000_310_000),
			"not_ready_basis": ReadinessBasisReadHold, "not_ready_read_hold_ms": float64(300_000),
			"not_ready_requirement": strings.Repeat("r", maxReadinessDeferralName)}
		for key, value := range want {
			if line[key] != value {
				t.Fatalf("%s: %s = %v, want %v; line %s", stage.stage, key, line[key], value, output.String())
			}
		}
	}
	odd := NormalizeObservation(Observation{Component: ComponentAccess, Stage: StageQueryCompleted, Result: ResultRetrying,
		ReasonCode: ReasonCode(contract.ReasonQueryNotReady), ReadinessDeferral: &ReadinessDeferralFacts{Basis: "data_arrival"}})
	if odd.ReadinessDeferral == nil || odd.ReadinessDeferral.Basis != ReadinessBasisOther {
		t.Fatalf("an unknown basis = %+v, want other", odd.ReadinessDeferral)
	}
	elsewhere := NormalizeObservation(Observation{Component: ComponentAccess, Stage: StageQueryCompleted, Result: ResultSuccess,
		ReadinessDeferral: facts})
	if elsewhere.ReadinessDeferral != nil {
		t.Fatal("readiness facts rode on a line that is not QUERY_NOT_READY")
	}
}
