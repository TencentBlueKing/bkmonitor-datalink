// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package kaccompat

import (
	"context"
	"slices"
)

type projectionAlertLock struct {
	gate  chan struct{}
	users int
}

// lockProjectionAlerts 只串行同租户同告警的读写，避免大批次占满固定通道后阻塞无关告警。
// 按稳定身份顺序取锁，防止重叠批次交叉等待；最后一个使用者退出后移除登记。
// 返回的释放函数由调用方在本次 I/O 完成后调用一次。
func (r *Runtime) lockProjectionAlerts(ctx context.Context, identities []alarmKey) (func(), error) {
	unique := make(map[string]struct{}, len(identities))
	for _, id := range identities {
		unique[id.tenant+"\x00"+id.alert] = struct{}{}
	}
	keys := make([]string, 0, len(unique))
	for key := range unique {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	locks := make([]*projectionAlertLock, len(keys))
	r.projectionMu.Lock()
	if r.projectionLocks == nil {
		r.projectionLocks = make(map[string]*projectionAlertLock)
	}
	for i, key := range keys {
		lock := r.projectionLocks[key]
		if lock == nil {
			lock = &projectionAlertLock{gate: make(chan struct{}, 1)}
			r.projectionLocks[key] = lock
		}
		lock.users++
		locks[i] = lock
	}
	r.projectionMu.Unlock()
	held := 0
	release := func() {
		for _, lock := range locks[:held] {
			<-lock.gate
		}
		r.projectionMu.Lock()
		for i, lock := range locks {
			lock.users--
			if lock.users == 0 {
				delete(r.projectionLocks, keys[i])
			}
		}
		r.projectionMu.Unlock()
	}
	for _, lock := range locks {
		select {
		case lock.gate <- struct{}{}:
			held++
		case <-ctx.Done():
			release()
			return nil, ctx.Err()
		}
	}
	return release, nil
}
