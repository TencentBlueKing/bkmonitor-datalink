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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/define"
)

func TestRuntimeSubscriptionReconnectsWithoutRecursiveStart(t *testing.T) {
	reconnected := make(chan struct{})
	var subscribeCalls atomic.Int32
	var listCalls atomic.Int32
	runtime := &stubRuntime{
		containersFn: func(context.Context) ([]define.SimpleContainer, error) {
			listCalls.Add(1)
			return nil, nil
		},
		subscribeFn: func(context.Context) (<-chan *define.ContainerEvent, <-chan error, error) {
			if subscribeCalls.Add(1) == 1 {
				return nil, nil, errors.New("runtime stream failed to start")
			}
			close(reconnected)
			return make(chan *define.ContainerEvent), nil, nil
		},
	}
	sidecar := newCharacterizationSidecar(t, runtime, &stubReader{})
	sidecar.subscribeRetryInterval = time.Millisecond
	var stopOnce sync.Once
	stop := func() { stopOnce.Do(sidecar.Stop) }
	t.Cleanup(stop)

	startDone := make(chan error, 1)
	go func() {
		startDone <- sidecar.Start(context.Background())
	}()
	waitForSignal(t, reconnected, "runtime subscription reconnect")
	require.Eventually(t, func() bool {
		return listCalls.Load() >= 2
	}, 2*time.Second, 5*time.Millisecond)
	require.Equal(t, int32(2), subscribeCalls.Load())
	stop()
	require.NoError(t, <-startDone)
}

func TestRuntimeSubscriptionReconnectTriggersFullConvergence(t *testing.T) {
	firstErrors := make(chan error, 1)
	reconnected := make(chan struct{})
	reconnectBuildErr := errors.New("runtime list unavailable after reconnect")
	var subscribeCalls atomic.Int32
	var listCalls atomic.Int32
	runtime := &stubRuntime{
		containersFn: func(context.Context) ([]define.SimpleContainer, error) {
			if listCalls.Add(1) == 3 {
				return nil, reconnectBuildErr
			}
			return nil, nil
		},
		subscribeFn: func(context.Context) (<-chan *define.ContainerEvent, <-chan error, error) {
			if subscribeCalls.Add(1) == 1 {
				return make(chan *define.ContainerEvent), firstErrors, nil
			}
			close(reconnected)
			return make(chan *define.ContainerEvent), nil, nil
		},
	}
	sidecar := newCharacterizationSidecar(t, runtime, &stubReader{})
	sidecar.subscribeRetryInterval = time.Millisecond
	sidecar.convergenceRetryBaseDelay = time.Millisecond
	sidecar.convergenceRetryMaxDelay = time.Millisecond
	startDone := make(chan error, 1)
	go func() {
		startDone <- sidecar.Start(context.Background())
	}()

	require.Eventually(t, func() bool {
		return listCalls.Load() >= 2
	}, 2*time.Second, 5*time.Millisecond)
	initialListCalls := listCalls.Load()
	firstErrors <- errors.New("runtime stream disconnected")

	waitForSignal(t, reconnected, "runtime subscription reconnect")
	require.Eventually(t, func() bool {
		// 重连后的第一次全量 Build 失败，必须在同一条有效订阅上自动重试。
		return listCalls.Load() >= initialListCalls+2
	}, 2*time.Second, 5*time.Millisecond)

	sidecar.Stop()
	require.NoError(t, <-startDone)
}

func TestInitialConvergenceSignalsReadyOnlyAfterSuccessfulRetry(t *testing.T) {
	firstBuildFailed := make(chan struct{})
	retryStarted := make(chan struct{})
	allowRetry := make(chan struct{})
	var listCalls atomic.Int32
	runtime := &stubRuntime{
		containersFn: func(context.Context) ([]define.SimpleContainer, error) {
			switch listCalls.Add(1) {
			case 2:
				close(firstBuildFailed)
				return nil, errors.New("initial full build failed")
			case 3:
				close(retryStarted)
				<-allowRetry
			}
			return nil, nil
		},
	}
	sidecar := newCharacterizationSidecar(t, runtime, &stubReader{})
	sidecar.convergenceRetryBaseDelay = time.Millisecond
	sidecar.convergenceRetryMaxDelay = time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	supervisorDone := make(chan struct{})
	go func() {
		defer close(supervisorDone)
		sidecar.subscribeEvent(ctx, ready)
	}()

	waitForSignal(t, firstBuildFailed, "first initial full build failure")
	waitForSignal(t, retryStarted, "initial full build retry")
	select {
	case <-ready:
		t.Fatal("startup was marked ready before the initial full convergence succeeded")
	default:
	}

	close(allowRetry)
	waitForSignal(t, ready, "successful initial full convergence")
	cancel()
	waitForSignal(t, supervisorDone, "runtime subscription supervisor shutdown")
}

func TestStartStopsPromptlyDuringInitialConvergenceBackoff(t *testing.T) {
	firstBuildFailed := make(chan struct{})
	var listCalls atomic.Int32
	runtime := &stubRuntime{
		containersFn: func(context.Context) ([]define.SimpleContainer, error) {
			if listCalls.Add(1) == 2 {
				close(firstBuildFailed)
			}
			return nil, errors.New("runtime list unavailable")
		},
	}
	sidecar := newCharacterizationSidecar(t, runtime, &stubReader{})
	sidecar.convergenceRetryBaseDelay = time.Hour
	sidecar.convergenceRetryMaxDelay = time.Hour

	startDone := make(chan error, 1)
	go func() {
		startDone <- sidecar.Start(context.Background())
	}()
	waitForSignal(t, firstBuildFailed, "initial convergence failure before backoff")

	sidecar.Stop()
	select {
	case err := <-startDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return after Stop interrupted convergence backoff")
	}
}

func TestNextConvergenceRetryDelayIsCapped(t *testing.T) {
	maximum := 5 * time.Second
	require.Equal(t, 2*time.Second, nextConvergenceRetryDelay(time.Second, maximum))
	require.Equal(t, 4*time.Second, nextConvergenceRetryDelay(2*time.Second, maximum))
	require.Equal(t, maximum, nextConvergenceRetryDelay(4*time.Second, maximum))
	require.Equal(t, maximum, nextConvergenceRetryDelay(maximum, maximum))
}

func TestStartReturnsCleanlyWhenCanceledBeforeSubscriptionReady(t *testing.T) {
	sidecar := newCharacterizationSidecar(t, nil, &stubReader{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	require.NoError(t, sidecar.Start(ctx))
}

func TestStartBlocksUntilStopAndWaitsForInFlightEvent(t *testing.T) {
	events := make(chan *define.ContainerEvent, 1)
	initialConvergenceDone := make(chan struct{})
	inspectStarted := make(chan struct{})
	inspectExited := make(chan struct{})
	var reloadOnce sync.Once
	var inspectOnce sync.Once
	runtime := &stubRuntime{
		inspectFn: func(ctx context.Context, _ string) (define.Container, error) {
			inspectOnce.Do(func() { close(inspectStarted) })
			<-ctx.Done()
			close(inspectExited)
			return define.Container{}, ctx.Err()
		},
		subscribeFn: func(context.Context) (<-chan *define.ContainerEvent, <-chan error, error) {
			return events, nil, nil
		},
	}
	sidecar := newCharacterizationSidecar(t, runtime, &stubReader{})
	sidecar.runtimeOperationTimeout = time.Hour
	sidecar.reloadAgentFn = func() error {
		reloadOnce.Do(func() { close(initialConvergenceDone) })
		return nil
	}

	startDone := make(chan error, 1)
	go func() {
		startDone <- sidecar.Start(context.Background())
	}()
	waitForSignal(t, initialConvergenceDone, "initial convergence")
	select {
	case err := <-startDone:
		t.Fatalf("Start returned before Stop: %v", err)
	default:
	}

	events <- &define.ContainerEvent{
		Type:        define.ContainerEventCreate,
		ContainerID: "container-1",
	}
	waitForSignal(t, inspectStarted, "in-flight container inspect")

	sidecar.Stop()
	waitForSignal(t, inspectExited, "in-flight container inspect cancellation")
	require.NoError(t, <-startDone)
}

func TestRuntimeEventIsQueuedWhileInitialContainerDiscoveryIsRunning(t *testing.T) {
	events := make(chan *define.ContainerEvent, 1)
	scanStarted := make(chan struct{})
	releaseScan := make(chan struct{})
	eventProcessed := make(chan struct{})
	var listCalls atomic.Int32
	var processedOnce sync.Once
	runtime := &stubRuntime{
		containersFn: func(context.Context) ([]define.SimpleContainer, error) {
			if listCalls.Add(1) == 1 {
				close(scanStarted)
				<-releaseScan
			}
			return nil, nil
		},
		inspectFn: func(_ context.Context, containerID string) (define.Container, error) {
			processedOnce.Do(func() { close(eventProcessed) })
			return define.Container{ID: containerID}, nil
		},
		subscribeFn: func(context.Context) (<-chan *define.ContainerEvent, <-chan error, error) {
			return events, nil, nil
		},
	}
	sidecar := newCharacterizationSidecar(t, runtime, &stubReader{})
	var stopOnce sync.Once
	stop := func() { stopOnce.Do(sidecar.Stop) }
	t.Cleanup(stop)

	startDone := make(chan error, 1)
	go func() {
		startDone <- sidecar.Start(context.Background())
	}()
	waitForSignal(t, scanStarted, "initial container discovery")

	events <- &define.ContainerEvent{
		Type:        define.ContainerEventCreate,
		ContainerID: "container-during-scan",
	}
	waitForSignal(t, eventProcessed, "container event processing during initial discovery")

	close(releaseScan)
	require.Eventually(t, func() bool {
		return !hasLatestContainerEvent(sidecar, "container-during-scan")
	}, 2*time.Second, 5*time.Millisecond)
	stop()
	require.NoError(t, <-startDone)
}
