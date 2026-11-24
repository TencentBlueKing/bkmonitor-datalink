// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package store

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"linkd/internal/domain"
)

func TestEventProcessingStateDiagnostics(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name    string
		p       EventProcessing
		message string
	}{
		{name: "unprocessed", p: NewUnprocessedEventProcessing()},
		{name: "terminal", p: EventProcessing{State: domain.EventProcessStateOrphaned, Outcome: "orphaned", ProcessedAt: &now}},
		{name: "failure diagnostic", p: EventProcessing{State: domain.EventProcessStateRejected, Outcome: "processing_failed", ReasonCode: "handler_panic", ProcessedAt: &now}},
		{name: "missing result", p: EventProcessing{State: domain.EventProcessStateOrphaned}, message: "requires outcome and processed_at"},
		{name: "invalid state", p: EventProcessing{State: "bad"}, message: "state is invalid"},
		{name: "unprocessed result", p: EventProcessing{State: domain.EventProcessStateUnprocessed, Outcome: "orphaned"}, message: "must not contain process result"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.p.Normalize()
			if tc.message == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if !errors.Is(err, ErrInvalidEventProcessing) || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

// 已部署 Event 的旧 checkpoint 字段不阻止解码，也不进入新处理流程。
func TestEventProcessingIgnoresRetiredCheckpoints(t *testing.T) {
	var processing EventProcessing
	if err := json.Unmarshal([]byte(`{"state":"unprocessed","plan":{"obsolete":true},"policy_context":{"obsolete":true}}`), &processing); err != nil {
		t.Fatal(err)
	}
	if _, err := processing.Normalize(); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(processing)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"plan"`) || strings.Contains(string(raw), `"policy_context"`) {
		t.Fatalf("retired checkpoints retained: %s", raw)
	}
}
