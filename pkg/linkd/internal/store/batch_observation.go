// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package store

import (
	"context"
	"time"
)

type batchPhaseKey struct{}

type BatchPhaseObserver func(context.Context, string, time.Duration)

// WithBatchPhaseObserver 只在显式批次链路记录固定阶段，不携带业务身份。
func WithBatchPhaseObserver(ctx context.Context, observer BatchPhaseObserver) context.Context {
	return context.WithValue(ctx, batchPhaseKey{}, observer)
}

func BatchObserver(ctx context.Context) BatchPhaseObserver {
	observer, _ := ctx.Value(batchPhaseKey{}).(BatchPhaseObserver)
	return observer
}

func ObserveBatchPhase(ctx context.Context, phase string, duration time.Duration) {
	if observer := BatchObserver(ctx); observer != nil {
		observer(ctx, phase, duration)
	}
}
