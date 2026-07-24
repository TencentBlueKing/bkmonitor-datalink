// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 日志平台 (BlueKing - Log) available.
// Copyright (C) 2017-2021 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controllers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/util/workqueue"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/define"
)

func TestContainerEventQueueRetriesTransientCreateFailure(t *testing.T) {
	inspectErr := errors.New("runtime inspect temporarily unavailable")
	var inspectCalls atomic.Int32
	sidecar := newCharacterizationSidecar(t, &stubRuntime{
		inspectFn: func(_ context.Context, containerID string) (define.Container, error) {
			if inspectCalls.Add(1) == 1 {
				return define.Container{}, inspectErr
			}
			return define.Container{ID: containerID}, nil
		},
	}, &stubReader{})
	startTestContainerEventWorker(t, sidecar, time.Millisecond)

	sidecar.enqueueContainerEvent(&define.ContainerEvent{
		Type:        define.ContainerEventCreate,
		ContainerID: "container-1",
	})

	require.Eventually(t, func() bool {
		return inspectCalls.Load() == 2 && !hasLatestContainerEvent(sidecar, "container-1")
	}, 2*time.Second, 5*time.Millisecond)
}

func TestContainerEventQueueDoesNotRetryConfirmedNotFound(t *testing.T) {
	var inspectCalls atomic.Int32
	sidecar := newCharacterizationSidecar(t, &stubRuntime{
		inspectFn: func(context.Context, string) (define.Container, error) {
			inspectCalls.Add(1)
			return define.Container{}, fmt.Errorf("inspect: %w", define.ErrContainerNotFound)
		},
	}, &stubReader{})
	startTestContainerEventWorker(t, sidecar, time.Millisecond)
	sidecar.containerCache.Store("container-1", &define.Container{ID: "container-1"})
	sidecar.configMutationMu.Lock()
	sidecar.ensurePendingContainerDeletionLocked("container-1", false)
	sidecar.configMutationMu.Unlock()

	sidecar.enqueueContainerEvent(&define.ContainerEvent{
		Type:        define.ContainerEventCreate,
		ContainerID: "container-1",
	})

	require.Eventually(t, func() bool {
		return inspectCalls.Load() == 1 && !hasLatestContainerEvent(sidecar, "container-1")
	}, 2*time.Second, 5*time.Millisecond)
	assert.Never(t, func() bool {
		return inspectCalls.Load() > 1
	}, 50*time.Millisecond, 5*time.Millisecond)
	assert.True(t, hasPendingContainerDeletion(sidecar, "container-1"),
		"过期 CREATE 不应取消已经安排好的容器配置清理")
}

func TestContainerEventQueueDropsStaleCreateRetryAfterDelete(t *testing.T) {
	inspectErr := errors.New("runtime inspect unavailable")
	var inspectCalls atomic.Int32
	sidecar := newCharacterizationSidecar(t, &stubRuntime{
		inspectFn: func(context.Context, string) (define.Container, error) {
			inspectCalls.Add(1)
			return define.Container{}, inspectErr
		},
	}, &stubReader{})
	startTestContainerEventWorker(t, sidecar, 100*time.Millisecond)

	sidecar.enqueueContainerEvent(&define.ContainerEvent{
		Type:        define.ContainerEventCreate,
		ContainerID: "container-1",
	})
	require.Eventually(t, func() bool {
		return inspectCalls.Load() == 1
	}, 2*time.Second, 5*time.Millisecond)

	sidecar.enqueueContainerEvent(&define.ContainerEvent{
		Type:        define.ContainerEventDelete,
		ContainerID: "container-1",
	})
	require.Eventually(t, func() bool {
		return !hasLatestContainerEvent(sidecar, "container-1")
	}, 2*time.Second, 5*time.Millisecond)
	assert.Never(t, func() bool {
		return inspectCalls.Load() > 1
	}, 200*time.Millisecond, 10*time.Millisecond)
}

func TestPendingCleanupRetriesReloadThroughContainerWorkqueue(t *testing.T) {
	reloadErr := errors.New("reload temporarily unavailable")
	var reloadCalls atomic.Int32
	sidecar := newCharacterizationSidecar(t, &stubRuntime{}, &stubReader{})
	startTestContainerEventWorker(t, sidecar, time.Millisecond)
	logConfig := &stubLogConfig{
		name:    "container-1_std_default_config",
		content: []byte("config"),
	}
	configPath, _ := cacheActualConfig(t, sidecar, logConfig)
	sidecar.reloadAgentFn = func() error {
		if reloadCalls.Add(1) == 1 {
			return reloadErr
		}
		return nil
	}

	cleanup := scheduleDestroyCleanup(t, sidecar, "container-1")
	cleanup()

	require.Eventually(t, func() bool {
		return reloadCalls.Load() == 2 && !isReloadPending(sidecar)
	}, 2*time.Second, 5*time.Millisecond)
	_, statErr := os.Stat(configPath)
	assert.True(t, os.IsNotExist(statErr))
}

func TestContainerEventTimeoutDoesNotBlockNewerEvent(t *testing.T) {
	inspectStarted := make(chan struct{})
	var inspectCalls atomic.Int32
	sidecar := newCharacterizationSidecar(t, &stubRuntime{
		inspectFn: func(ctx context.Context, _ string) (define.Container, error) {
			inspectCalls.Add(1)
			close(inspectStarted)
			<-ctx.Done()
			return define.Container{}, ctx.Err()
		},
	}, &stubReader{})
	sidecar.runtimeOperationTimeout = 20 * time.Millisecond
	startTestContainerEventWorker(t, sidecar, 100*time.Millisecond)

	sidecar.enqueueContainerEvent(&define.ContainerEvent{
		Type:        define.ContainerEventCreate,
		ContainerID: "container-1",
	})
	waitForSignal(t, inspectStarted, "container inspect")
	sidecar.enqueueContainerEvent(&define.ContainerEvent{
		Type:        define.ContainerEventDelete,
		ContainerID: "container-1",
	})

	require.Eventually(t, func() bool {
		return inspectCalls.Load() == 1 && !hasLatestContainerEvent(sidecar, "container-1")
	}, 2*time.Second, 5*time.Millisecond)
}

func startTestContainerEventWorker(t *testing.T, sidecar *BkLogSidecar, retryDelay time.Duration) {
	t.Helper()
	sidecar.eventQueue = workqueue.NewRateLimitingQueue(
		workqueue.NewItemExponentialFailureRateLimiter(retryDelay, retryDelay),
	)
	ctx, cancel := context.WithCancel(context.Background())
	sidecar.startContainerEventWorker(ctx)
	t.Cleanup(func() {
		cancel()
		sidecar.shutdownContainerEventQueue()
		sidecar.lifecycleWG.Wait()
	})
}

func hasLatestContainerEvent(sidecar *BkLogSidecar, containerID string) bool {
	sidecar.eventSequenceMu.Lock()
	defer sidecar.eventSequenceMu.Unlock()
	_, ok := sidecar.latestEventSequence[containerID]
	return ok
}

func isReloadPending(sidecar *BkLogSidecar) bool {
	sidecar.configMutationMu.Lock()
	defer sidecar.configMutationMu.Unlock()
	return sidecar.reloadPending
}

func hasPendingContainerDeletion(sidecar *BkLogSidecar, containerID string) bool {
	sidecar.configMutationMu.Lock()
	defer sidecar.configMutationMu.Unlock()
	_, ok := sidecar.pendingContainerDeletes[containerID]
	return ok
}
