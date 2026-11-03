// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package scheduler

import "context"

type failureRetriedKey struct{}

// WithFailureRetried marks an execution whose Runner retries a failed attempt
// (recovery is on): its failure is one attempt, not the Slot's end.
func WithFailureRetried(ctx context.Context) context.Context {
	return context.WithValue(ctx, failureRetriedKey{}, true)
}

// FailureRetried says whether the Runner that started this execution will
// back off and retry the Slot after it returns err: it retries failed
// attempts, and err is one it counts as a failed attempt (not a cancellation
// or an ownership refusal). The execution's completion line reads it so a
// failure that will be retried is not written as the Slot's end.
func FailureRetried(ctx context.Context, err error) bool {
	retried, _ := ctx.Value(failureRetriedKey{}).(bool)
	return retried && executionErrorBacksOff(ctx, err)
}
