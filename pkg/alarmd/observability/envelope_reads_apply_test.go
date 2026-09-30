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
	"testing"
	"time"
)

// The apply's envelope count reaches the rendered line under its own key.
//
// Asserted on the line and not on the struct: the count list the line is
// written from is hand-listed, and a field on Counts that is not in it is a
// number nobody can read. The preflight's key is checked absent in the same
// line, so the two consumers of the envelope stay two readings.
func TestTheStateAppliedLineCarriesTheApplyEnvelopeCount(t *testing.T) {
	var output bytes.Buffer
	limiter, err := NewWindowLogLimiter(WindowLogLimiterConfig{Window: time.Hour, MaxEvents: 10})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewBoundedLogPolicy(limiter)
	if err != nil {
		t.Fatal(err)
	}
	NewLoggingObserver(New("alarmd", &output), policy).Observe(context.Background(), Observation{
		Component: ComponentState, Stage: StageStateApplied, Result: ResultSuccess,
		Counts: Counts{Keys: 4, EnvelopeReadsApply: 2},
	})
	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("decode log: %v; log=%q", err, output.String())
	}
	if event["envelope_reads_apply"] != float64(2) {
		t.Fatalf("envelope_reads_apply = %#v, want 2; event=%#v", event["envelope_reads_apply"], event)
	}
	if _, found := event["envelope_reads"]; found {
		t.Fatalf("the apply's count was rendered under the preflight's key: %#v", event)
	}
}
