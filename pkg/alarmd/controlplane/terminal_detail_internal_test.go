// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// A compiler refusal that said what went wrong reaches the disposition a
// strategy lookup reads, bounded like every other detail.
func TestACompilerRefusalKeepsWhatItSaid(t *testing.T) {
	said := `strategy: decode trigger plan: json: unknown field "cw_calendars"`
	disposition := terminalDisposition("54", "LEVEL", strategy.Terminal{LevelID: 1, ReasonCode: "LEVEL_INVALID",
		FieldPath: "level.trigger_plan", Detail: said})
	if disposition.Detail != said || disposition.Reason != "LEVEL_INVALID" || disposition.FieldPath != "level.trigger_plan" {
		t.Fatalf("disposition = %+v", disposition)
	}
	long := terminalDisposition("54", "LEVEL", strategy.Terminal{LevelID: 1, ReasonCode: "LEVEL_INVALID",
		FieldPath: "level.trigger_plan", Detail: strings.Repeat("x", 3*dispositionDetailMaxBytes)})
	if len(long.Detail) != dispositionDetailMaxBytes {
		t.Fatalf("detail of %d bytes, want the bound %d", len(long.Detail), dispositionDetailMaxBytes)
	}
	if quiet := terminalDisposition("54", "LEVEL", strategy.Terminal{LevelID: 1, ReasonCode: "LEVEL_BUDGET_EXCEEDED",
		FieldPath: "level.trigger_plan"}); quiet.Detail != "" {
		t.Fatalf("a refusal without text gained %q", quiet.Detail)
	}
}
