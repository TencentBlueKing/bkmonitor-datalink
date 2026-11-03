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
	"context"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A request that failed on its own can say which deadline was on it and
// which had run out. A Slot's request runs under the earlier of its caller's
// deadline and its attempt's own (Execute), and a deadline error from the
// transport names neither; a timeout the transport set itself names nothing
// at all. Without these a query cut a fraction of a second after it was
// sent, with its round's budget a minute away, could not be told from a
// backend that never answered.

// requestDeadline is the request context's deadline as sent, and how much of
// it was left just then: -1 when it had none.
func requestDeadline(ctx context.Context, at time.Time) (deadline time.Time, left int64) {
	deadline, bounded := ctx.Deadline()
	if !bounded {
		return time.Time{}, -1
	}
	return deadline, deadline.Sub(at).Milliseconds()
}

// requestFacts is a failed request against its deadline (execution.RequestFacts).
func requestFacts(callerCtx, ctx context.Context, err error, deadline time.Time, left int64, traceID string) *execution.RequestFacts {
	facts := &execution.RequestFacts{DeadlineLeftAtDoMs: left, DeadlineSource: execution.RequestDeadlineAttempt,
		Expired: execution.RequestExpiredNone, TraceID: traceID}
	if !deadline.IsZero() {
		facts.DeadlineUnixMilli = deadline.UnixMilli()
	}
	if caller, bounded := callerCtx.Deadline(); bounded && (deadline.IsZero() || !caller.After(deadline)) {
		facts.DeadlineSource = execution.RequestDeadlineCaller
	}
	// The request's context is the caller's or derived from it, so the
	// caller's running out ends both: it is named first.
	switch {
	case callerCtx.Err() != nil:
		facts.Expired = execution.RequestDeadlineCaller
	case ctx.Err() != nil:
		facts.Expired = execution.RequestDeadlineAttempt
	}
	if err != nil {
		facts.ErrorChain = errorChain(err)
		facts.ErrorTypes = errorTypes(err)
	}
	return facts
}

// errorChain is err as text, sanitized as a provider's body is (URLs,
// addresses and credential pairs replaced) and cut to MaxRequestErrorBytes.
func errorChain(err error) string {
	text := providerErrorExcerpt([]byte(fmt.Sprintf("%v", err)))
	if len(text) <= execution.MaxRequestErrorBytes {
		return text
	}
	cut := execution.MaxRequestErrorBytes - len("...")
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "..."
}

// errorTypes is the Go types along err's chain, outermost first, following
// Unwrap() error and each branch of Unwrap() []error, at most
// MaxRequestErrorTypes.
func errorTypes(err error) []string {
	var types []string
	queue := []error{err}
	for len(queue) > 0 && len(types) < execution.MaxRequestErrorTypes {
		current := queue[0]
		queue = queue[1:]
		if current == nil {
			continue
		}
		types = append(types, fmt.Sprintf("%T", current))
		switch unwrapped := current.(type) {
		case interface{ Unwrap() []error }:
			queue = append(queue, unwrapped.Unwrap()...)
		case interface{ Unwrap() error }:
			queue = append(queue, unwrapped.Unwrap())
		}
	}
	return types
}
