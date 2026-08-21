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
	"encoding/json"
	"log/slog"
	"testing"
	"time"
)

func TestLoggerWritesFixedEventFields(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	logger := New(ComponentTrigger, &output)
	logger.Info(StageDecisionACK, ResultBrokerACK, 2, 1500*time.Millisecond, slog.String("batch_id", "batch-1"))

	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("decode log: %v", err)
	}
	want := map[string]any{
		"component":   ComponentTrigger,
		"stage":       StageDecisionACK,
		"result":      ResultBrokerACK,
		"records":     float64(2),
		"duration_ms": float64(1500),
		"batch_id":    "batch-1",
	}
	for field, value := range want {
		if event[field] != value {
			t.Fatalf("event[%q] = %#v, want %#v; event=%#v", field, event[field], value, event)
		}
	}
}

func TestDiscardLoggerAcceptsEvents(t *testing.T) {
	t.Parallel()

	Discard(ComponentComparator).Error(StageFatal, ResultFailed, 0, 0)
}
