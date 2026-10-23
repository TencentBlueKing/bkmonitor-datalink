// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package openalerts

import (
	"context"
	"time"
)

// LocationConfirmed implements ConsoleFacts: the place this process reads
// the sets from was the Console's own answer - taken from its target at
// startup, or found equal to it by the last reconciliation - and no later
// reconciliation found the link writing elsewhere.
func (reader *HTTPReconciler) LocationConfirmed() bool {
	if reader == nil {
		return false
	}
	reader.mu.Lock()
	defer reader.mu.Unlock()
	return reader.confirmed
}

// KeyedByAlertID implements ConsoleFacts from the last read of the event
// source that answered. The answer is kept while later reads fail: the link
// refuses any change to a released source's fingerprint settings, so what
// can change is only whether it is released or deleted, and the next read
// that answers says so. An answer about another source than the current
// target's is no answer.
func (reader *HTTPReconciler) KeyedByAlertID() (bool, time.Time, bool) {
	if reader == nil {
		return false, time.Time{}, false
	}
	reader.mu.Lock()
	current := reader.binding.EventSourceID
	reader.mu.Unlock()
	reader.calls.mu.Lock()
	keying, at := reader.calls.eventSource, reader.calls.eventSourceReadAt
	reader.calls.mu.Unlock()
	if at.IsZero() || current != "" && keying.EventSourceID != current {
		return false, time.Time{}, false
	}
	return keying.KeyedByAlertID, at, true
}

// KeyingAsked implements ConsoleFacts: the event source has been asked
// since the location in force was set.
func (reader *HTTPReconciler) KeyingAsked() bool {
	if reader == nil {
		return false
	}
	reader.mu.Lock()
	defer reader.mu.Unlock()
	return !reader.keyingTried.IsZero()
}

// Refresh implements ConsoleFacts. With the location confirmed and its
// target resolved, it reads the keying when due (refreshKeying), which
// costs no target read. Otherwise - at a start, after a move, or while the
// link is found writing elsewhere - it resolves the target, at most once per
// bindingReuse, and reads the keying with it. A strategy's calibration
// resolves too; this is the path that does not wait for one.
func (reader *HTTPReconciler) Refresh(ctx context.Context) {
	if reader == nil {
		return
	}
	reader.mu.Lock()
	confirmed, binding, resolvedAt, tried := reader.confirmed, reader.binding, reader.resolvedAt, reader.factsTried
	reader.mu.Unlock()
	if confirmed && !resolvedAt.IsZero() {
		reader.refreshKeying(ctx, binding)
		return
	}
	if !tried.IsZero() && reader.now().Sub(tried) < bindingReuse {
		return
	}
	reader.mu.Lock()
	reader.factsTried = reader.now()
	reader.mu.Unlock()
	resolved, err := reader.resolve(ctx)
	if err != nil {
		return
	}
	reader.refreshKeying(ctx, resolved)
}

// refreshKeying reads the event source again when it is due: never read for
// this target's source, or KeyingEvery past the last attempt. One read per
// process per KeyingEvery, not one per strategy reconciled. Its outcome is
// kept on the call record (EventSource); a failure leaves the last answer.
func (reader *HTTPReconciler) refreshKeying(ctx context.Context, binding TargetBinding) {
	reader.mu.Lock()
	tried := reader.keyingTried
	reader.mu.Unlock()
	reader.calls.mu.Lock()
	keying, at := reader.calls.eventSource, reader.calls.eventSourceReadAt
	reader.calls.mu.Unlock()
	current := !at.IsZero() && keying.EventSourceID == binding.EventSourceID
	if current && (reader.options.KeyingEvery <= 0 || reader.now().Sub(tried) < reader.options.KeyingEvery) {
		return
	}
	if !current && !tried.IsZero() && reader.now().Sub(tried) < bindingReuse {
		// Not answered yet: asked again at most a minute apart, not on every
		// strategy's reconciliation.
		return
	}
	reader.mu.Lock()
	reader.keyingTried = reader.now()
	reader.mu.Unlock()
	_, _ = reader.EventSource(ctx)
}
