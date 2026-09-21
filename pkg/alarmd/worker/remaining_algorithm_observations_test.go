// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func TestHistoricalDependencyPointsUseBoundedLabel(t *testing.T) {
	for _, name := range []string{"history_60", "history_604800", "history_172800"} {
		point, ok := observedDependencyPoint(name)
		if !ok || point != observability.AlgorithmDependencyPointHistorical {
			t.Fatalf("%s mapped to %q, %t", name, point, ok)
		}
	}
	for _, name := range []string{"history_", "history_0", "history_-1", "history_arbitrary_input", "unknown"} {
		if _, ok := observedDependencyPoint(name); ok {
			t.Fatalf("unrecognized dependency %q accepted", name)
		}
	}
}
