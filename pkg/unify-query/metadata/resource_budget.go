// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metadata

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
)

const (
	ResourceSeries            = "series"
	ResourcePoints            = "points"
	ResourceBytes             = "bytes"
	ResourceResponseBytes     = "response_bytes"
	ResourceEvalCapacityBytes = "eval_capacity_bytes"

	// PromQLPointBytes is the base size of prometheus/promql.Point on the
	// supported 64-bit runtime (timestamp, float value and histogram pointer).
	// Native histogram payloads need separate, stricter limits when enabled.
	PromQLPointBytes int64 = 24
)

type ResourceBudgetLimits struct {
	MaxSeries            int64
	MaxPoints            int64
	MaxBytes             int64
	MaxResponseBytes     int64
	MaxEvalCapacityBytes int64
}

func (l ResourceBudgetLimits) Enabled() bool {
	return l.MaxSeries > 0 ||
		l.MaxPoints > 0 ||
		l.MaxBytes > 0 ||
		l.MaxResponseBytes > 0 ||
		l.MaxEvalCapacityBytes > 0
}

type ResourceBudgetUsage struct {
	Series            int64
	Points            int64
	Bytes             int64
	ResponseBytes     int64
	EvalCapacityBytes int64
	EvalSeries        int64
	MaxEvalSteps      int64
}

type ResourceBudgetSnapshot struct {
	Limits    ResourceBudgetLimits
	Usage     ResourceBudgetUsage
	Rejected  *ResourceBudgetError
	Cancelled bool
}

type ResourceBudgetError struct {
	Resource  string
	Limit     int64
	Attempted int64
}

func (e *ResourceBudgetError) Error() string {
	return fmt.Sprintf("query resource budget exceeds %s: attempted=%d limit=%d", e.Resource, e.Attempted, e.Limit)
}

func IsResourceBudgetError(err error) bool {
	var limitErr *ResourceBudgetError
	return errors.As(err, &limitErr)
}

type resourceBudgetContextKey struct{}

type ResourceBudget struct {
	mu        sync.Mutex
	limits    ResourceBudgetLimits
	usage     ResourceBudgetUsage
	rejected  *ResourceBudgetError
	cancelled bool
	closed    bool
	cancel    context.CancelFunc
}

func NewResourceBudget(limits ResourceBudgetLimits, cancel context.CancelFunc) *ResourceBudget {
	return &ResourceBudget{
		limits: limits,
		cancel: cancel,
	}
}

func WithResourceBudget(ctx context.Context, budget *ResourceBudget) context.Context {
	if budget == nil {
		return ctx
	}
	return context.WithValue(ctx, resourceBudgetContextKey{}, budget)
}

func GetResourceBudget(ctx context.Context) *ResourceBudget {
	if ctx == nil {
		return nil
	}
	budget, _ := ctx.Value(resourceBudgetContextKey{}).(*ResourceBudget)
	return budget
}

func CancelResourceBudget(ctx context.Context) {
	if budget := GetResourceBudget(ctx); budget != nil {
		budget.Cancel()
	}
}

func (b *ResourceBudget) Cancel() {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.cancelled = true
	cancel := b.cancel
	b.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (b *ResourceBudget) Limits() ResourceBudgetLimits {
	if b == nil {
		return ResourceBudgetLimits{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.limits
}

func (b *ResourceBudget) Reserve(series, points, bytes int64) error {
	if b == nil {
		return nil
	}
	if series < 0 || points < 0 || bytes < 0 {
		return fmt.Errorf("query resource budget reservation cannot be negative")
	}

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return context.Canceled
	}
	nextSeries := saturatingAdd(b.usage.Series, series)
	nextPoints := saturatingAdd(b.usage.Points, points)
	nextBytes := saturatingAdd(b.usage.Bytes, bytes)
	if err := b.limitErrorLocked(ResourceSeries, b.limits.MaxSeries, nextSeries); err != nil {
		return b.rejectAndUnlock(err)
	}
	if err := b.limitErrorLocked(ResourcePoints, b.limits.MaxPoints, nextPoints); err != nil {
		return b.rejectAndUnlock(err)
	}
	if err := b.limitErrorLocked(ResourceBytes, b.limits.MaxBytes, nextBytes); err != nil {
		return b.rejectAndUnlock(err)
	}
	b.usage.Series = nextSeries
	b.usage.Points = nextPoints
	b.usage.Bytes = nextBytes
	b.mu.Unlock()
	return nil
}

func (b *ResourceBudget) ReserveResponseBytes(bytes int64) error {
	if b == nil {
		return nil
	}
	if bytes < 0 {
		return fmt.Errorf("query response byte reservation cannot be negative")
	}

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return context.Canceled
	}
	next := saturatingAdd(b.usage.ResponseBytes, bytes)
	if err := b.limitErrorLocked(ResourceResponseBytes, b.limits.MaxResponseBytes, next); err != nil {
		return b.rejectAndUnlock(err)
	}
	b.usage.ResponseBytes = next
	b.mu.Unlock()
	return nil
}

func (b *ResourceBudget) ReserveEvalCapacity(series, steps, pointBytes int64) error {
	if b == nil || series <= 0 || steps <= 0 || pointBytes <= 0 {
		return nil
	}
	capacity := saturatingMultiply(saturatingMultiply(series, steps), pointBytes)

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return context.Canceled
	}
	next := saturatingAdd(b.usage.EvalCapacityBytes, capacity)
	if err := b.limitErrorLocked(ResourceEvalCapacityBytes, b.limits.MaxEvalCapacityBytes, next); err != nil {
		return b.rejectAndUnlock(err)
	}
	b.usage.EvalCapacityBytes = next
	b.usage.EvalSeries = saturatingAdd(b.usage.EvalSeries, series)
	if steps > b.usage.MaxEvalSteps {
		b.usage.MaxEvalSteps = steps
	}
	b.mu.Unlock()
	return nil
}

func (b *ResourceBudget) SetEvaluationSteps(steps int64) {
	if b == nil || steps <= 0 {
		return
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	if steps > b.usage.MaxEvalSteps {
		b.usage.MaxEvalSteps = steps
	}
	b.mu.Unlock()
}

func (b *ResourceBudget) EvaluationSteps() int64 {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.usage.MaxEvalSteps
}

func (b *ResourceBudget) Reject(resource string, attempted int64) error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return context.Canceled
	}
	limit := b.limitLocked(resource)
	if limit <= 0 {
		b.mu.Unlock()
		return nil
	}
	if attempted <= limit {
		attempted = saturatingAdd(limit, 1)
	}
	return b.rejectAndUnlock(&ResourceBudgetError{
		Resource:  resource,
		Limit:     limit,
		Attempted: attempted,
	})
}

func (b *ResourceBudget) MaxResponseBytes() int64 {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.limits.MaxResponseBytes
}

func (b *ResourceBudget) Snapshot() ResourceBudgetSnapshot {
	if b == nil {
		return ResourceBudgetSnapshot{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.snapshotLocked()
}

func (b *ResourceBudget) RejectionError() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.rejected == nil {
		return nil
	}
	copyOfError := *b.rejected
	return &copyOfError
}

// Close returns the final accounting snapshot and releases request-local
// counters. It does not cancel the caller context.
func (b *ResourceBudget) Close() ResourceBudgetSnapshot {
	if b == nil {
		return ResourceBudgetSnapshot{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	snapshot := b.snapshotLocked()
	b.usage = ResourceBudgetUsage{}
	b.closed = true
	return snapshot
}

func (b *ResourceBudget) snapshotLocked() ResourceBudgetSnapshot {
	var rejected *ResourceBudgetError
	if b.rejected != nil {
		copyOfError := *b.rejected
		rejected = &copyOfError
	}
	return ResourceBudgetSnapshot{
		Limits:    b.limits,
		Usage:     b.usage,
		Rejected:  rejected,
		Cancelled: b.cancelled,
	}
}

func (b *ResourceBudget) limitErrorLocked(resource string, limit, attempted int64) *ResourceBudgetError {
	if limit <= 0 || attempted <= limit {
		return nil
	}
	return &ResourceBudgetError{
		Resource:  resource,
		Limit:     limit,
		Attempted: attempted,
	}
}

func (b *ResourceBudget) limitLocked(resource string) int64 {
	switch resource {
	case ResourceSeries:
		return b.limits.MaxSeries
	case ResourcePoints:
		return b.limits.MaxPoints
	case ResourceBytes:
		return b.limits.MaxBytes
	case ResourceResponseBytes:
		return b.limits.MaxResponseBytes
	case ResourceEvalCapacityBytes:
		return b.limits.MaxEvalCapacityBytes
	default:
		return 0
	}
}

// rejectAndUnlock records the first rejection and invokes cancellation after
// dropping the accounting lock so sibling branches can stop immediately.
func (b *ResourceBudget) rejectAndUnlock(err *ResourceBudgetError) error {
	if b.rejected == nil {
		copyOfError := *err
		b.rejected = &copyOfError
	}
	b.cancelled = true
	cancel := b.cancel
	b.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return err
}

func saturatingAdd(a, b int64) int64 {
	if b > 0 && a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

func saturatingMultiply(a, b int64) int64 {
	if a <= 0 || b <= 0 {
		return 0
	}
	if a > math.MaxInt64/b {
		return math.MaxInt64
	}
	return a * b
}
