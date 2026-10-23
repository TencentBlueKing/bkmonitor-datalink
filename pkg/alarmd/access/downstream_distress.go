// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package access

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The ways a downstream answer says the downstream is overloaded or cannot be
// reached: closed, for a counter label.
const (
	// DistressTimeout is a query that ran out of time waiting for an answer.
	DistressTimeout = "timeout"
	// DistressThrottled is an HTTP 429.
	DistressThrottled = "throttled"
	// DistressUnavailable is an HTTP 502, 503 or 504.
	DistressUnavailable = "unavailable"
	// DistressUnreachable is a connection refused, reset or cut short.
	DistressUnreachable = "unreachable"
)

// DistressClasses is the closed set of distress classes.
var DistressClasses = []string{DistressTimeout, DistressThrottled, DistressUnavailable, DistressUnreachable}

// The route details a distressed answer carries, spelled once here so the
// comparison below allocates nothing.
var (
	distressDetailTimeout     = execution.TransportRouteDetail(execution.TransportFailureTimeout)
	distressDetailRefused     = execution.TransportRouteDetail(execution.TransportFailureConnectionRefused)
	distressDetailReset       = execution.TransportRouteDetail(execution.TransportFailureConnectionReset)
	distressDetailEOF         = execution.TransportRouteDetail(execution.TransportFailureEOF)
	distressDetailThrottled   = execution.HTTPStatusRouteDetail(429)
	distressDetailBadGateway  = execution.HTTPStatusRouteDetail(502)
	distressDetailUnavailable = execution.HTTPStatusRouteDetail(503)
	distressDetailGateway     = execution.HTTPStatusRouteDetail(504)
)

// downstreamDistressOf is the distress the query's last failed attempt says
// the downstream is in, or empty for an answer, a refusal of the query itself
// (a 4xx other than 429, a 500) or a failure on this side (DNS, TLS).
//
// It is what tells a downstream that this process overloaded from one that
// is failing anyway, read against whether the query had waited for its
// permit (ObserveDownstreamDistress). Nothing acts on it: when distress lines
// up with this process's own contention, the query budget derivation is to
// be revisited, and this is the reading that says so.
func downstreamDistressOf(completion execution.ProviderCompletion) string {
	attempts := completion.RouteFacts.Attempts
	for index := len(attempts) - 1; index >= 0; index-- {
		attempt := &attempts[index]
		if attempt.Result != execution.RouteAttemptFailed && attempt.ReasonCode == "" {
			continue
		}
		if attempt.ReasonCode == execution.ReasonCode(contract.ReasonQueryTimeout) {
			return DistressTimeout
		}
		switch attempt.Detail {
		case distressDetailTimeout:
			return DistressTimeout
		case distressDetailThrottled:
			return DistressThrottled
		case distressDetailBadGateway, distressDetailUnavailable, distressDetailGateway:
			return DistressUnavailable
		case distressDetailRefused, distressDetailReset, distressDetailEOF:
			return DistressUnreachable
		}
		return ""
	}
	return ""
}

// permitWaited is whether the query a permit admits had queued for it; a
// permit that cannot say reads as not.
func permitWaited(permit QueryPermit) bool {
	waited, ok := permit.(interface{ Waited() bool })
	return ok && waited.Waited()
}
