// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A Slot that ended without a finished result has no usage, and its line
// carries none: a row of zeros beside a share refusal read as a Slot that
// held nothing when it was refused. A reported usage, even one that took
// nothing, still carries its limits and is on the line.
func TestASlotWithoutUsageCarriesNoUsageFacts(t *testing.T) {
	if facts := slotBudgetUsageFacts(execution.SlotBudgetUsage{}); facts != nil {
		t.Fatalf("an unreported usage became facts %+v", facts)
	}
	facts := slotBudgetUsageFacts(execution.SlotBudgetUsage{RetainedBytesLimit: 1 << 30, RetainedShareBytes: 512 << 20})
	if facts == nil || facts.RetainedBytes != 0 || facts.RetainedShareBytes != 512<<20 {
		t.Fatalf("a reported usage that took nothing became %+v", facts)
	}
}
