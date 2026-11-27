// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package window

import "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metricmetadata/core"

// StateCache holds committed snapshots by business group and scope hash.
// Its zero value is ready to use; persistence and expiry remain database responsibilities.
type StateCache struct {
	scopes  map[core.ScopeKey]core.ScopeState
	bytes   int64
	members int
}

// Lookup returns the committed snapshot for key.
func (w *StateCache) Lookup(key core.ScopeKey) (core.ScopeState, bool) {
	state, ok := w.scopes[key]
	return state, ok
}

// Store replaces one committed snapshot and updates its estimated size for metrics.
func (w *StateCache) Store(state core.ScopeState) {
	if w.scopes == nil {
		w.scopes = make(map[core.ScopeKey]core.ScopeState)
	}
	if old, ok := w.scopes[state.Key]; ok {
		w.bytes -= old.Size()
		w.members -= len(old.Metrics)
		delete(w.scopes, state.Key)
	}
	size := state.Size()
	w.scopes[state.Key] = state
	w.bytes += size
	w.members += len(state.Metrics)
}

// ReplaceAll publishes one complete database snapshot after it has been loaded.
// The caller serializes access; building locally keeps the old snapshot intact until then.
func (w *StateCache) ReplaceAll(states []core.ScopeState) {
	scopes := make(map[core.ScopeKey]core.ScopeState, len(states))
	var bytes int64
	var members int
	for _, state := range states {
		scopes[state.Key] = state
		bytes += state.Size()
		members += len(state.Metrics)
	}
	w.scopes = scopes
	w.bytes = bytes
	w.members = members
}

// Clear removes all snapshots and reports whether the cache held any.
func (w *StateCache) Clear() bool {
	hadStates := len(w.scopes) > 0
	w.scopes = make(map[core.ScopeKey]core.ScopeState)
	w.bytes = 0
	w.members = 0
	return hadStates
}

// RemoveMetric removes one metric from scopes in groupID.
func (w *StateCache) RemoveMetric(groupID int, field string) {
	for key, state := range w.scopes {
		if key.GroupID != groupID {
			continue
		}
		if _, ok := state.Metrics[field]; !ok {
			continue
		}
		oldSize := state.Size()
		delete(state.Metrics, field)
		w.members--
		w.bytes += state.Size() - oldSize
		w.scopes[key] = state
	}
}

// Len returns the number of cached scopes.
func (w *StateCache) Len() int { return len(w.scopes) }

// Members returns the number of cached metrics.
func (w *StateCache) Members() int { return w.members }

// Bytes returns the estimated size of cached state.
func (w *StateCache) Bytes() int64 { return w.bytes }

// Range visits every cached snapshot.
func (w *StateCache) Range(visit func(core.ScopeState)) {
	for _, state := range w.scopes {
		visit(state)
	}
}
