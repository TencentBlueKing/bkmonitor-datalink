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

// StatePreflightResults is every result a Runtime State preflight reports,
// plus the fold: success when every series' state came back, degraded when
// some did not for a retryable reason, terminal when some cannot be read for
// a deterministic one, failed when the store refused the request outright.
var StatePreflightResults = []Result{
	Result(ResultSuccess), ResultDegraded, ResultTerminal, Result(ResultFailed), ResultOther,
}

// StatePreflightReasons is every reason the state store's read path puts on
// a preflight, plus the fold. The read path names four: the read ran out of
// time, the store was unreachable, a blob is corrupt, a blob is past the
// retained-bytes budget; a request refused outright carries the internal
// class word; a read that came back whole carries none. Anything else is
// counted as _other rather than dropped -- a word this list does not know
// is still a preflight that happened.
//
// This is the bounded label set of state_preflight_total. The preflight's
// result and reason were on the log line and the fleet's object row and on
// no counter, so whether a state read timed out was answerable per object
// and never fleet-wide -- the word it got when it timed out (STATE_READ_TIMEOUT
// rather than REDIS_UNAVAILABLE) could not be verified from metrics at all.
var StatePreflightReasons = []ReasonCode{
	ReasonNone, ReasonInternalUnknown,
	ReasonCode(contract.ReasonStateReadTimeout), ReasonCode(contract.ReasonRedisUnavailable),
	ReasonCode(contract.ReasonStateCorrupt), ReasonCode(contract.ReasonStateBudgetExceeded),
	ReasonOther,
}

var statePreflightResultSet = makeResultSet(StatePreflightResults)
var statePreflightReasonSet = makeReasonSet(StatePreflightReasons)

// NormalizeStatePreflight folds a preflight's result and reason onto the two
// closed lists above. The observation is normalized first, so an empty
// reason on a degraded result already reads reason_not_reported, which this
// then folds to _other: the site failed to report, and that is counted, not
// given a cell of its own.
func NormalizeStatePreflight(result Result, reason ReasonCode) (Result, ReasonCode) {
	if _, ok := statePreflightResultSet[result]; !ok {
		result = ResultOther
	}
	if _, ok := statePreflightReasonSet[reason]; !ok {
		reason = ReasonOther
	}
	return result, reason
}
