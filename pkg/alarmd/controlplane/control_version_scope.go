// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package controlplane

import (
	"context"
	"sync"
)

// The activation header precedes every activation and timeline read, and one
// logical operation reads it several times over: measured live it is a quarter
// of all Redis commands but three fifths of all time spent waiting on Redis.
//
// Caching it across operations is not available. The header is not only a cache
// validity token, it is the signal that a publication cut over, and Segment
// closure and Gap recovery are decided by observing that signal rather than by
// choosing which bytes to use. Serving a header late therefore misses a
// cutover rather than merely delaying data, which the plan gap recovery tests
// demonstrate directly.
//
// A scope bounded by one operation has neither problem. The header is still
// read live once per operation, so the next operation observes a cutover
// exactly as it does today; only the repeats inside a single operation go away.
// That is also the stronger reading semantics: every read in one operation now
// observes the same control version, instead of some reads landing before a
// cutover and some after it.
type controlVersionScopeKey struct{}

type controlVersionScope struct {
	mu     sync.Mutex
	value  controlVersion
	loaded bool
}

// WithControlVersionScope marks the start of one logical operation. Nesting is
// a no-op so an inner call cannot narrow an outer scope.
func WithControlVersionScope(ctx context.Context) context.Context {
	if ctx == nil {
		return nil
	}
	if _, ok := ctx.Value(controlVersionScopeKey{}).(*controlVersionScope); ok {
		return ctx
	}
	return context.WithValue(ctx, controlVersionScopeKey{}, &controlVersionScope{})
}

func controlVersionScopeFrom(ctx context.Context) *controlVersionScope {
	if ctx == nil {
		return nil
	}
	scope, _ := ctx.Value(controlVersionScopeKey{}).(*controlVersionScope)
	return scope
}

// readControlVersion serves the activation header, reading it live unless this
// operation already read it. A failed read is not remembered, so the next call
// in the same operation retries rather than inheriting the failure.
func (repository *RedisCatalogRepository) readControlVersion(ctx context.Context) (controlVersion, error) {
	scope := controlVersionScopeFrom(ctx)
	if scope == nil {
		return repository.fetchControlVersion(ctx)
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if scope.loaded {
		repository.controlReads.version.hits.Add(1)
		return scope.value, nil
	}
	version, err := repository.fetchControlVersion(ctx)
	if err != nil {
		return controlVersion{}, err
	}
	scope.value, scope.loaded = version, true
	repository.controlReads.version.misses.Add(1)
	return version, nil
}
