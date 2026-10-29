// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package uq

import (
	"bytes"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

const (
	// headerTraceParent carries the W3C trace context of one attempt. The
	// provider takes it as its own trace, so its spans, its logs and the
	// trace_id it answers with are this attempt's - the one way to find a
	// refused request on its side. A fresh trace per attempt: a retry is a
	// request of its own.
	headerTraceParent = "traceparent"
	// headerStrategy names the strategies a query serves, for the provider's
	// logs alone. The query source header is not used for it: the provider
	// splits that header into source and name and decides feature flags on
	// both, so anything added there could change where a query is routed.
	headerStrategy = "X-Alarmd-Strategy"
	// maxStrategyHeaderBytes bounds the strategy header. A query a hundred
	// Plans share names the first ones and how many more there were.
	maxStrategyHeaderBytes = 256
)

// strategyHeader is the strategy ids joined by commas, cut before the id
// that would pass maxStrategyHeaderBytes and followed by "+N" for the N left
// out; empty for none. Only canonical decimal ids are named - an id that
// is anything else is counted into N - so the header is digits, commas and
// "+N" whatever an id holds.
func strategyHeader(ids []string) string {
	named := make([]string, 0, len(ids))
	for _, id := range ids {
		if contract.CanonicalDecimal(id) {
			named = append(named, id)
		}
	}
	skipped := len(ids) - len(named)
	tail := func(left int) string {
		if left == 0 {
			return ""
		}
		return "+" + strconv.Itoa(left)
	}
	var out strings.Builder
	for index, id := range named {
		more := ""
		if left := len(named) - index - 1 + skipped; left > 0 {
			more = "," + tail(left)
		}
		next := id
		if out.Len() > 0 {
			next = "," + id
		}
		if out.Len()+len(next)+len(more) > maxStrategyHeaderBytes {
			if out.Len() == 0 {
				return tail(len(ids))
			}
			return out.String() + "," + tail(len(named)-index+skipped)
		}
		out.WriteString(next)
	}
	if skipped > 0 {
		if out.Len() == 0 {
			return tail(skipped)
		}
		out.WriteString("," + tail(skipped))
	}
	return out.String()
}

// rejectedStatus says a status is the provider refusing the query as
// written: a client error other than 408 (it ran out of time) and 429 (it
// asks for less load), both of which are the backend's own state.
func rejectedStatus(status int) bool {
	return status >= 400 && status <= 499 && status != http.StatusRequestTimeout && status != http.StatusTooManyRequests
}

// timedOutStatus says a status is the provider saying it ran out of time:
// 504 (its backend did not answer before its deadline) and 408. The number
// decides, never the body.
func timedOutStatus(status int) bool {
	return status == http.StatusGatewayTimeout || status == http.StatusRequestTimeout
}

// statusReason is the reason a non-success status files a query under.
func statusReason(status int) execution.ReasonCode {
	switch {
	case rejectedStatus(status):
		return execution.ReasonCode(contract.ReasonQueryRejected)
	case timedOutStatus(status):
		return execution.ReasonCode(contract.ReasonQueryTimeout)
	}
	return execution.ReasonCode(contract.ReasonQueryUnavailable)
}

// traceIDPattern is a W3C trace id; anything else in the field is not
// carried.
var traceIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// providerRejection is a non-success answer as it is kept (see
// execution.ProviderRejection): its class, the provider's trace id - the
// one its body names, else the one this attempt sent - and the sanitized
// start of its body.
func providerRejection(status int, body []byte, sentTraceID string) *execution.ProviderRejection {
	traceID := bodyTraceID(body)
	if traceID == "" && traceIDPattern.MatchString(sentTraceID) {
		traceID = sentTraceID
	}
	return &execution.ProviderRejection{Class: rejectionClass(status, body), TraceID: traceID, Excerpt: providerErrorExcerpt(body)}
}

func bodyTraceID(body []byte) string {
	var answer struct {
		TraceID string `json:"trace_id"`
	}
	if json.Unmarshal(body, &answer) != nil || !traceIDPattern.MatchString(answer.TraceID) {
		return ""
	}
	return answer.TraceID
}

// rejectionClass names what a refusal was about from words the provider's
// storages answer with, HTTP_<status> when it names none of them. A bounded
// word, so it can be a label and a log field; the body itself is not.
// It stays a log-only diagnostic: nothing decides a reason, check or owner
// on it, and attribution reads the status alone.
func rejectionClass(status int, body []byte) string {
	text := bytes.ToLower(body)
	switch {
	case bytes.Contains(text, []byte("reduce the time range")) ||
		bytes.Contains(text, []byte("samples")) && bytes.Contains(text, []byte("limit")):
		return "VM_SAMPLE_LIMIT"
	case bytes.Contains(text, []byte("parse error")) || bytes.Contains(text, []byte("promql")) && bytes.Contains(text, []byte("parse")):
		return "PROMQL_PARSE"
	case bytes.Contains(text, []byte("invalid query")):
		return "INVALID_QUERY"
	}
	return "HTTP_" + strconv.Itoa(status)
}
