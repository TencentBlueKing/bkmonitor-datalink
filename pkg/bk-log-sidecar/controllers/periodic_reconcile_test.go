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
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	bluekingv1alpha1 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/api/bk.tencent.com/v1alpha1"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/define"
)

func TestStartLaunchesPeriodicReconcileAfterInitialConvergence(t *testing.T) {
	t.Setenv(config.CurrentNodeNameKey, "node-1")
	var listCalls atomic.Int32
	var reloadCalls atomic.Int32
	runtime := &stubRuntime{
		containersFn: func(context.Context) ([]define.SimpleContainer, error) {
			listCalls.Add(1)
			return nil, nil
		},
	}
	sidecar := newCharacterizationSidecar(t, runtime, &stubReader{})
	sidecar.periodicReconcileInterval = 2 * time.Millisecond
	sidecar.periodicReconcileDelayFn = func(interval time.Duration, _ float64) time.Duration {
		return interval
	}
	sidecar.reloadAgentFn = func() error {
		reloadCalls.Add(1)
		return nil
	}

	startDone := make(chan error, 1)
	go func() {
		startDone <- sidecar.Start(context.Background())
	}()
	require.Eventually(t, func() bool {
		// 启动 cache + 首次 Build 各 List 一次；第三次来自周期全量收敛。
		return listCalls.Load() >= 3
	}, 2*time.Second, 5*time.Millisecond)
	assert.Equal(t, int32(1), reloadCalls.Load())

	sidecar.Stop()
	require.NoError(t, <-startDone)
}

func TestPeriodicReconcileRecoversMissedEventWithoutRepeatedReload(t *testing.T) {
	t.Setenv(config.CurrentNodeNameKey, "node-1")
	var inspectCalls atomic.Int32
	var nodeReads atomic.Int32
	var reloadCalls atomic.Int32
	runtime := &stubRuntime{
		containersFn: func(context.Context) ([]define.SimpleContainer, error) {
			return []define.SimpleContainer{{ID: "container-1"}}, nil
		},
		inspectFn: func(context.Context, string) (define.Container, error) {
			inspectCalls.Add(1)
			return *testKubernetesContainer(), nil
		},
	}
	reader := periodicTestReader(&nodeReads, nil)
	sidecar := newCharacterizationSidecar(t, runtime, reader)
	sidecar.periodicReconcileInterval = 2 * time.Millisecond
	sidecar.periodicReconcileJitter = 0.2
	delayObserved := make(chan struct{}, 1)
	sidecar.periodicReconcileDelayFn = func(interval time.Duration, jitter float64) time.Duration {
		assert.Equal(t, 2*time.Millisecond, interval)
		assert.Equal(t, 0.2, jitter)
		select {
		case delayObserved <- struct{}{}:
		default:
		}
		return interval
	}
	sidecar.reloadAgentFn = func() error {
		reloadCalls.Add(1)
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		sidecar.periodicReconcile(ctx)
	}()
	waitForSignal(t, delayObserved, "periodic reconcile scheduling")

	configPath := filepath.Join(
		config.BkunifylogbeatConfig,
		configNameForTestContainer()+generatedConfigSuffix,
	)
	require.Eventually(t, func() bool {
		_, err := os.Stat(configPath)
		return err == nil && reloadCalls.Load() == 1
	}, 2*time.Second, 5*time.Millisecond)

	// 后续周期必须重新 Inspect Runtime，但相同 desired 不能重复 reload。
	require.Eventually(t, func() bool {
		return inspectCalls.Load() >= 3 && nodeReads.Load() >= 3
	}, 2*time.Second, 5*time.Millisecond)
	assert.Equal(t, int32(1), reloadCalls.Load())

	cancel()
	waitForSignal(t, done, "periodic reconcile shutdown")
}

func TestPeriodicBuildUsesSingleBkLogConfigSnapshot(t *testing.T) {
	t.Setenv(config.CurrentNodeNameKey, "node-1")
	var listCalls atomic.Int32
	var nodeReads atomic.Int32
	reader := periodicTestReader(&nodeReads, func(list client.ObjectList) error {
		// 如果 Build 内读取了第二次 CR，返回不同 DataID，让测试同时约束
		// “只 List 一次”和“所有容器使用同一份快照”。
		dataID := int64(1000 + listCalls.Add(1))
		bkLogConfigs := list.(*bluekingv1alpha1.BkLogConfigList)
		bkLogConfigs.Items = []bluekingv1alpha1.BkLogConfig{testContainerBkLogConfig(dataID)}
		return nil
	})
	runtime := &stubRuntime{
		containersFn: func(context.Context) ([]define.SimpleContainer, error) {
			return []define.SimpleContainer{
				{ID: "container-1"},
				{ID: "container-2"},
			}, nil
		},
		inspectFn: func(_ context.Context, containerID string) (define.Container, error) {
			container := *testKubernetesContainer()
			container.ID = containerID
			return container, nil
		},
	}
	sidecar := newCharacterizationSidecar(t, runtime, reader)

	logConfigs, err := sidecar.buildActualBkLogConfigs(
		context.Background(),
		configGenerationOptions{refreshDiscoveredState: true},
	)

	require.NoError(t, err)
	require.Len(t, logConfigs, 2)
	assert.Equal(t, int32(1), listCalls.Load())
	for _, logConfig := range logConfigs {
		stdoutConfig, ok := logConfig.(*define.StdOutLogConfig)
		require.True(t, ok)
		assert.Equal(t, int64(1001), stdoutConfig.Spec.DataId)
	}
}

func TestPeriodicReconcileFailurePreservesLastKnownGoodAndContinues(t *testing.T) {
	t.Setenv(config.CurrentNodeNameKey, "node-1")
	buildErr := errors.New("BkLogConfig cache temporarily unavailable")
	var failNextList atomic.Bool
	var nodeReads atomic.Int32
	var reloadCalls atomic.Int32
	firstPeriodicFailure := make(chan struct{})
	var failureOnce sync.Once
	reader := periodicTestReader(&nodeReads, func(list client.ObjectList) error {
		if failNextList.CompareAndSwap(true, false) {
			failureOnce.Do(func() { close(firstPeriodicFailure) })
			return buildErr
		}
		return fillPeriodicBkLogConfigs(list)
	})
	runtime := &stubRuntime{
		containersFn: func(context.Context) ([]define.SimpleContainer, error) {
			return []define.SimpleContainer{{ID: "container-1"}}, nil
		},
		inspectFn: func(context.Context, string) (define.Container, error) {
			return *testKubernetesContainer(), nil
		},
	}
	sidecar := newCharacterizationSidecar(t, runtime, reader)
	sidecar.reloadAgentFn = func() error {
		reloadCalls.Add(1)
		return nil
	}

	require.NoError(t, sidecar.generateActualBkLogConfig())
	initialGeneration := sidecar.configSnapshotGeneration()
	configPath := filepath.Join(
		config.BkunifylogbeatConfig,
		configNameForTestContainer()+generatedConfigSuffix,
	)
	lastKnownGood, err := os.ReadFile(configPath)
	require.NoError(t, err)
	require.Equal(t, int32(1), reloadCalls.Load())

	failNextList.Store(true)
	sidecar.periodicReconcileInterval = 2 * time.Millisecond
	sidecar.periodicReconcileDelayFn = func(interval time.Duration, _ float64) time.Duration {
		return interval
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		sidecar.periodicReconcile(ctx)
	}()

	waitForSignal(t, firstPeriodicFailure, "periodic reconciliation failure")
	require.Eventually(t, func() bool {
		// 失败轮次不提交世代；下一轮成功后即使无差异也会完成一次 Apply。
		return sidecar.configSnapshotGeneration() > initialGeneration
	}, 2*time.Second, 5*time.Millisecond)
	cancel()
	waitForSignal(t, done, "periodic reconcile shutdown after recovery")

	current, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.Equal(t, lastKnownGood, current)
	assert.Equal(t, int32(1), reloadCalls.Load())
}

func TestPeriodicReconcileCancellationInterruptsLongWait(t *testing.T) {
	sidecar := newCharacterizationSidecar(t, &stubRuntime{}, &stubReader{})
	sidecar.periodicReconcileInterval = time.Hour
	timerStarted := make(chan struct{})
	var timerOnce sync.Once
	sidecar.periodicReconcileDelayFn = func(interval time.Duration, _ float64) time.Duration {
		timerOnce.Do(func() { close(timerStarted) })
		return interval
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		sidecar.periodicReconcile(ctx)
	}()
	waitForSignal(t, timerStarted, "long periodic reconcile timer")
	cancel()
	waitForSignal(t, done, "periodic reconcile cancellation")
}

func TestPeriodicReconcileCancellationInterruptsActiveDiscovery(t *testing.T) {
	t.Setenv(config.CurrentNodeNameKey, "node-1")
	inspectStarted := make(chan struct{})
	var inspectOnce sync.Once
	runtime := &stubRuntime{
		containersFn: func(context.Context) ([]define.SimpleContainer, error) {
			return []define.SimpleContainer{{ID: "container-1"}}, nil
		},
		inspectFn: func(ctx context.Context, _ string) (define.Container, error) {
			inspectOnce.Do(func() { close(inspectStarted) })
			<-ctx.Done()
			return define.Container{}, ctx.Err()
		},
	}
	var nodeReads atomic.Int32
	sidecar := newCharacterizationSidecar(t, runtime, periodicTestReader(&nodeReads, nil))
	sidecar.periodicReconcileDelayFn = func(time.Duration, float64) time.Duration {
		return 0
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		sidecar.periodicReconcile(ctx)
	}()
	waitForSignal(t, inspectStarted, "active periodic container discovery")
	cancel()
	waitForSignal(t, done, "active periodic reconciliation cancellation")
}

func TestJitteredReconcileDelayStaysWithinConfiguredRange(t *testing.T) {
	interval := 5 * time.Minute
	lower := 4 * time.Minute
	upper := 6 * time.Minute
	for i := 0; i < 200; i++ {
		delay := jitteredReconcileDelay(interval, 0.2)
		assert.GreaterOrEqual(t, delay, lower)
		assert.LessOrEqual(t, delay, upper)
	}
	assert.Equal(t, interval, jitteredReconcileDelay(interval, 0))
	assert.Equal(t, float64(0), normalizePeriodicReconcileJitter(-0.1))
	assert.Equal(t, float64(1), normalizePeriodicReconcileJitter(1.5))
}

func periodicTestReader(
	nodeReads *atomic.Int32,
	listFn func(client.ObjectList) error,
) *stubReader {
	return &stubReader{
		getFn: func(_ context.Context, key client.ObjectKey, obj client.Object) error {
			switch target := obj.(type) {
			case *corev1.Node:
				nodeReads.Add(1)
				target.ObjectMeta = metav1.ObjectMeta{Name: key.Name}
			case *corev1.Pod:
				target.ObjectMeta = metav1.ObjectMeta{Namespace: key.Namespace, Name: key.Name}
			}
			return nil
		},
		listFn: func(_ context.Context, list client.ObjectList) error {
			if listFn != nil {
				return listFn(list)
			}
			return fillPeriodicBkLogConfigs(list)
		},
	}
}

func fillPeriodicBkLogConfigs(list client.ObjectList) error {
	bkLogConfigs := list.(*bluekingv1alpha1.BkLogConfigList)
	bkLogConfigs.Items = []bluekingv1alpha1.BkLogConfig{testContainerBkLogConfig(1001)}
	return nil
}
