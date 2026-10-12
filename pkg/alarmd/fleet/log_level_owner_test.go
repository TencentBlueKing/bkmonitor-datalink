// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"log/slog"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A reason the page files under this deployment's own checks, or under a
// backend not answering, is written at WARN; one the page files under a
// strategy, the data or the platform, or reads as a normal value, at INFO.
// The log and the page decide the same reason the same way: when the page
// moves a reason to another owner, the log's level moves with it, or this
// fails. Two reasons the page files under a backend not answering are the
// routine wait for data and data arriving after its window, and stay INFO.
func TestAReasonsLogLevelFollowsTheOwnerThePageFilesItUnder(t *testing.T) {
	t.Parallel()

	info := map[string]bool{"QUERY_NOT_READY": true, "LATE_OUT_OF_WINDOW": true}
	checked := 0
	for _, reason := range observability.AllLogReasons() {
		verdict, mapped := codeChecks[string(reason)]
		if !mapped {
			continue
		}
		checked++
		want := slog.LevelInfo
		switch {
		case verdict.normal, info[string(reason)]:
		case checkAnswers[verdict.check].Owner == OwnerAlarmd, verdict.check == CheckBackendNotAnswering:
			want = slog.LevelWarn
		}
		if got := observability.ReasonLogLevel(reason); got != want {
			t.Errorf("%s: page files it under %s (%s), log writes it at %s, want %s",
				reason, verdict.check, checkAnswers[verdict.check].Owner, got, want)
		}
	}
	if checked < 100 {
		t.Fatalf("only %d reasons are mapped by the page: the loop read the wrong table", checked)
	}
}
