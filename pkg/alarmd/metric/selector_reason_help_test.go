// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/targetplan"
)

// The selector counter's help names every reason its label can carry. The
// help is where an operator reading a reason for the first time looks it up,
// and the label is filled from targetplan's closed list, so a reason added
// there and not here is a series nobody can look up. Checked against the
// list rather than spelled out, so that the next reason fails here too.
func TestTheSelectorCounterHelpNamesEveryClosedReason(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	families, err := r.registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	help := ""
	for _, family := range families {
		if family.GetName() == "bkmonitor_alarmd_target_selector_resolutions_total" {
			help = family.GetHelp()
		}
	}
	if help == "" {
		t.Fatal("the selector counter is not published before any resolution, or has no help")
	}
	for _, reason := range targetplan.SelectorReasons {
		if reason == targetplan.ReasonNone {
			continue
		}
		if !strings.Contains(help, reason) {
			t.Errorf("the selector counter's help does not name %q", reason)
		}
	}
}
