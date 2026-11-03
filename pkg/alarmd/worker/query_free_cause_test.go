// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A query-free finalization that carries no cause still names one, so the
// Runner writes its slot_skipped line for every completion the kind counter
// counts.
func TestAQueryFreeFinalizationWithoutACauseStillNamesOne(t *testing.T) {
	if got := queryFreeCause(""); got != execution.CompletionCause(observability.ProgressCompletionCauseNone) {
		t.Fatalf("empty cause = %q, want %q", got, observability.ProgressCompletionCauseNone)
	}
	if got := queryFreeCause(execution.CauseExpiredReplay); got != execution.CauseExpiredReplay {
		t.Fatalf("named cause = %q, want it kept", got)
	}
}
