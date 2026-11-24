// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

func newFrontRefusedCollector() *outcomeCounterCollector {
	return newOutcomeCounterCollector("front_refused_total",
		"Front reads (the admin page's calls through the front surface) alarmd refused, by the code it answered "+
			"with: method_not_allowed, admin_unauthorized, invalid_input, unsupported_channel_version, invalid_mode, "+
			"op_not_permitted_for_front (an operation or a parameter the front surface does not serve: a page calling "+
			"what this build no longer serves it), unknown_operation, catalog_changed, front_target_not_allowed, "+
			"target_routing_unavailable, operation_unavailable, front_answer_unshaped, _other. Every cell exists from "+
			"the start so a zero is a reading and not an absence.")
}

// SetFrontRefusedSource binds the front surface's refusal counts. Safe before
// or after registration; a nil recorder is a no-op.
func (r *Recorder) SetFrontRefusedSource(codes []string, counts func() map[string]uint64) {
	if r == nil || r.phaseTwo.frontRefused == nil {
		return
	}
	r.phaseTwo.frontRefused.set(codes, counts)
}
