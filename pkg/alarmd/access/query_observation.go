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
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// How a physical query failed, closed: the overview's pull stage counts
// failures by these, and the query count and duration are taken at the same
// place (Config.ObserveQuery).
const (
	QueryFailureTimeout   = "timeout"
	QueryFailureRefused   = "refused"
	QueryFailureNotFound  = "not_found"
	QueryFailureTransport = "transport"
	QueryFailureDecode    = "decode"
	QueryFailureOther     = "other"
)

// QueryFailures is the closed list.
var QueryFailures = []string{QueryFailureTimeout, QueryFailureRefused, QueryFailureNotFound, QueryFailureTransport, QueryFailureDecode, QueryFailureOther}

// queryFailureOf is how a physical query failed, or "" when it answered,
// wholly or in part. A query that failed after its answer began comes back
// as an error that carries the detail of a failed attempt
// (QueryFailureDetail); one that failed before comes back as a completion
// whose last failed attempt carries it. Either way the detail decides
// (failureOfDetail). An error that carries none is a timeout when its
// deadline passed and other otherwise.
func queryFailureOf(completion execution.ProviderCompletion, err error) string {
	if err != nil {
		var detailed interface{ QueryFailureDetail() string }
		if errors.As(err, &detailed) {
			if failure := failureOfDetail("", detailed.QueryFailureDetail()); failure != QueryFailureOther {
				return failure
			}
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return QueryFailureTimeout
		}
		return QueryFailureOther
	}
	if completion.Completeness != execution.CompletenessUnavailable {
		return ""
	}
	attempts := completion.RouteFacts.Attempts
	for index := len(attempts) - 1; index >= 0; index-- {
		attempt := attempts[index]
		if attempt.Result != execution.RouteAttemptFailed && attempt.ReasonCode == "" {
			continue
		}
		return failureOfDetail(attempt.ReasonCode, attempt.Detail)
	}
	return QueryFailureOther
}

// failureOfDetail classifies a failed attempt by its reason and detail: a
// timeout wherever it was met (the transport, the body, the gateway, the
// query's own budget, or alarmd's delivery of what had arrived -- the
// query's time ran out either way); a refusal when the backend answered no
// (a 4xx, a throttle, a status code); not found when that status code names
// something that does not exist; transport when the connection or the body
// was cut or the gateway could not answer; decode when the answer broke the
// wire contract.
func failureOfDetail(reason execution.ReasonCode, detail string) string {
	if reason == execution.ReasonCode(contract.ReasonQueryTimeout) {
		return QueryFailureTimeout
	}
	kind, value, _ := strings.Cut(detail, "=")
	switch kind {
	case execution.RouteDetailKindTransport, execution.RouteDetailKindBody:
		if value == execution.TransportFailureTimeout {
			return QueryFailureTimeout
		}
		return QueryFailureTransport
	case execution.RouteDetailKindDelivery:
		return QueryFailureTimeout
	case execution.RouteDetailKindHTTPStatus:
		status, _ := strconv.Atoi(value)
		switch {
		case status == http.StatusGatewayTimeout:
			return QueryFailureTimeout
		case status >= 400 && status < 500:
			return QueryFailureRefused
		case status >= 500:
			return QueryFailureTransport
		}
		return QueryFailureOther
	case execution.RouteDetailKindResponse:
		if code, isStatus := strings.CutPrefix(value, execution.ResponseFailureStatusPrefix); isStatus {
			if strings.Contains(code, "not_exist") || strings.Contains(code, "not_found") {
				return QueryFailureNotFound
			}
			return QueryFailureRefused
		}
		return QueryFailureDecode
	}
	return QueryFailureOther
}

// queryElapsed is how long a physical query took: the provider's own
// measure of an answer, a failed answer's own timing when it carries one,
// and otherwise the time the call took, measured around it.
func queryElapsed(completion execution.ProviderCompletion, err error, called time.Duration) time.Duration {
	if err == nil {
		return time.Duration(completion.Stats.QueryMillis) * time.Millisecond
	}
	var timed interface {
		QueryFailureTiming() *execution.AttemptTiming
	}
	if errors.As(err, &timed) {
		if timing := timed.QueryFailureTiming(); timing != nil && timing.ElapsedMillis > 0 {
			return time.Duration(timing.ElapsedMillis) * time.Millisecond
		}
	}
	return called
}
