// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

// RoleNotApplicable is the state a one-hot family says on a process none of
// whose roles it describes - span export on a process without the worker
// role, the control source, the Console and the platform settings on one
// with neither control nor worker - so the process still says one state
// there instead of reading all zero. fleet.TracingOffRole is the same word.
const RoleNotApplicable = "off_role"

// SetNotApplicableToRole marks the families this process's roles never set:
// each then says RoleNotApplicable. A family whose source is bound later
// (its role runs here after all) says the bound state. A family this does
// not know is ignored, as is a nil recorder.
func (r *Recorder) SetNotApplicableToRole(families ...string) {
	if r == nil {
		return
	}
	for _, family := range families {
		switch family {
		case "alarmd_trace_export":
			r.SetTraceExportState(RoleNotApplicable)
		case "alarmd_control_source_mode":
			if c := r.phaseTwo.controlSource; c != nil {
				c.mu.Lock()
				c.offRole = true
				c.mu.Unlock()
			}
		case "alarmd_platform_settings_mode":
			if c := r.phaseTwo.platformSettings; c != nil {
				c.mu.Lock()
				c.offRole = true
				c.mu.Unlock()
			}
		}
	}
}

// OneHotFamilies are the gauge families every process sets to exactly one
// series at 1 and the rest at 0, whatever roles it runs: the state it is in,
// said rather than inferred. A family that some role leaves unset reads all
// zero there, which is no answer - the silence these families exist to
// remove. A gauge whose HELP calls itself one-hot (or "1 on the current")
// must be listed here (TestEveryOneHotGaugeIsListed), and each listed family
// is read on a process of every role shape (cmd/alarmd's role guard).
var OneHotFamilies = []string{
	"alarmd_build_info",
	"alarmd_control_source_mode",
	"alarmd_platform_settings_mode",
	"alarmd_trace_export",
}
