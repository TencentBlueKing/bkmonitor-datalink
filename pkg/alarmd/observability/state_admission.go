// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"

// StateAdmissionResults is every result a Runtime State admission call
// reports, plus the fold: success when every mutation was admitted, terminal
// when some were refused for a deterministic reason, failed when the call did
// not complete.
var StateAdmissionResults = []Result{Result(ResultSuccess), ResultTerminal, Result(ResultFailed), ResultOther}

// StateAdmissionReasons is every reason the store's admission puts on a
// call, plus the fold: STATE_BUDGET_EXCEEDED for a lifetime past the ceiling
// or a record past the value limit, STATE_CORRUPT for a record the framed
// contract refuses, the internal class word on a call that failed, none on
// one that admitted everything.
//
// This is the bounded label set of state_admission_total. A Plan refused as
// STATE_BUDGET_EXCEEDED at every round was on no counter: the refusal ended
// the round as terminal, the strategy stopped detecting, and fleet-wide
// nothing moved. Every cell exists from startup, so a zero is a zero.
var StateAdmissionReasons = []ReasonCode{
	ReasonNone, ReasonInternalUnknown,
	ReasonCode(contract.ReasonStateBudgetExceeded), ReasonCode(contract.ReasonStateCorrupt),
	ReasonOther,
}

var stateAdmissionResultSet = makeResultSet(StateAdmissionResults)
var stateAdmissionReasonSet = makeReasonSet(StateAdmissionReasons)

// NormalizeStateAdmission folds an admission call's result and reason onto
// the two closed lists above; a word they do not know is counted as _other
// rather than dropped.
func NormalizeStateAdmission(result Result, reason ReasonCode) (Result, ReasonCode) {
	if _, ok := stateAdmissionResultSet[result]; !ok {
		result = ResultOther
	}
	if _, ok := stateAdmissionReasonSet[reason]; !ok {
		reason = ReasonOther
	}
	return result, reason
}
