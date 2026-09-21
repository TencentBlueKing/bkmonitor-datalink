// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"

// queryAvailabilityEvidence folds the existing validated binding traversal.
// Duplicate consumers of a physical query do not change these set predicates.
// Aggregate PRIMARY facts and completion causes deliberately play no role.
type queryAvailabilityEvidence struct {
	primarySeen           bool
	primaryNotUnavailable bool
	available             bool
	primaryDelivered      bool
	nonBackendFailure     bool
}

func (e *queryAvailabilityEvidence) observe(binding execution.NamedInputBinding, physical execution.PhysicalQueryCompletion, streamed, sourceBackend bool) {
	if binding.Role != execution.InputRolePrimary {
		return
	}
	e.primarySeen = true
	e.nonBackendFailure = e.nonBackendFailure || (physical.Completeness == execution.CompletenessUnavailable && !sourceBackend)
	e.primaryNotUnavailable = e.primaryNotUnavailable || physical.Completeness != execution.CompletenessUnavailable
	e.primaryDelivered = e.primaryDelivered || streamed
	// FULL_EMPTY establishes availability too, even when a local consumer or
	// algorithm fails. PARTIAL needs actual usable data, not merely an empty
	// partial response. An UNAVAILABLE completion invalidates provisional data.
	e.available = e.available || physical.Completeness == execution.CompletenessFull ||
		(physical.Completeness == execution.CompletenessPartial && streamed && binding.Disposition == execution.AccessAvailable)
}

func (e queryAvailabilityEvidence) availability() execution.QueryAvailability {
	if e.available {
		return execution.QueryAvailabilityAvailable
	}
	if e.primarySeen && !e.primaryNotUnavailable && !e.primaryDelivered && !e.nonBackendFailure {
		return execution.QueryAvailabilityUnavailable
	}
	return execution.QueryAvailabilityUnknown
}
