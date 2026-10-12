// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package readhold

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A record answers the hold of every Slot from the one before its current
// hold's first Slot on, and nothing earlier; a record begun from no hold
// answers none for every Slot before its first.
func TestARecordAnswersTheHoldOfTheSlotsItReaches(t *testing.T) {
	record, err := Decode([]byte(`{"hold_ms":60000,"since_slot":600,"previous_hold_ms":30000,"previous_since_slot":300,"later":1}`))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	for slot, want := range map[execution.EvaluationTime]struct {
		hold  int64
		known bool
	}{900: {60000, true}, 600: {60000, true}, 540: {30000, true}, 300: {30000, true}, 240: {0, false}} {
		if hold, known := record.HoldAt(slot); hold != want.hold || known != want.known {
			t.Fatalf("HoldAt(%d) = %d %v, want %d %v", slot, hold, known, want.hold, want.known)
		}
	}
	begun, err := Decode([]byte(`{"hold_ms":60000,"since_slot":600,"previous_since_slot":1}`))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if hold, known := begun.HoldAt(60); hold != 0 || !known {
		t.Fatalf("HoldAt(60) = %d %v, want no hold before a record begun from none", hold, known)
	}
	for _, raw := range []string{`{"hold_ms":-1,"since_slot":600}`, `{"hold_ms":60000}`, `{"hold_ms":1,"since_slot":60,"previous_since_slot":120}`, `[]`,
		`{"since_slot":1,"plans":[{"key":{"TenantID":"tenant","BusinessID":"business"}}]}`} {
		if _, err := Decode([]byte(raw)); err == nil {
			t.Fatalf("Decode(%s) accepted", raw)
		}
	}
}
