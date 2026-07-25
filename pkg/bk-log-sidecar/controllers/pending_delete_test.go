// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 日志平台 (BlueKing - Log) available.
// Copyright (C) 2017-2021 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

package controllers

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	bluekingv1alpha1 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/api/bk.tencent.com/v1alpha1"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/define"
)

func cacheActualConfig(t *testing.T, sidecar *BkLogSidecar, logConfig define.LogConfigType) (string, []byte) {
	t.Helper()
	content, err := logConfig.Config()
	require.NoError(t, err)
	path := filepath.Join(config.BkunifylogbeatConfig, logConfig.ConfigName()+generatedConfigSuffix)
	require.NoError(t, os.WriteFile(path, content, 0o600))
	sidecar.actualBkLogConfigCache.Store(logConfig.ConfigName(), logConfig)
	return path, content
}

func scheduleDestroyCleanup(t *testing.T, sidecar *BkLogSidecar, containerID string) func() {
	t.Helper()
	sidecar.containerCache.Store(containerID, &define.Container{ID: containerID})
	delayedCleanup := make(chan func(), 1)
	sidecar.delayCleanFn = func(_ time.Duration, fn func()) {
		delayedCleanup <- fn
	}
	sidecar.destroyActionHandler(&define.ContainerEvent{
		Type:        define.ContainerEventDelete,
		ContainerID: containerID,
	})
	select {
	case cleanup := <-delayedCleanup:
		return cleanup
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for delayed container cleanup")
		return nil
	}
}

func TestReconcileKeepsPendingContainerConfigForUnrelatedBkLogConfig(t *testing.T) {
	scheme := k8sruntime.NewScheme()
	require.NoError(t, bluekingv1alpha1.AddToScheme(scheme))
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&bluekingv1alpha1.BkLogConfig{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "config-2"},
	}).Build()
	sidecar := newCharacterizationSidecar(t, &stubRuntime{}, &stubReader{})
	pendingConfig := &stubLogConfig{
		name:    "container-1_std_default_config-1",
		content: []byte("pending config"),
	}
	pendingPath, pendingContent := cacheActualConfig(t, sidecar, pendingConfig)
	var reloadCalls atomic.Int32
	sidecar.reloadAgentFn = func() error {
		reloadCalls.Add(1)
		return nil
	}

	cleanup := scheduleDestroyCleanup(t, sidecar, "container-1")
	reconciler := &BkLogConfigReconciler{
		Client:       kubeClient,
		Scheme:       scheme,
		BkLogSidecar: sidecar,
	}

	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "config-2"},
	})

	require.NoError(t, err)
	content, readErr := os.ReadFile(pendingPath)
	require.NoError(t, readErr)
	assert.Equal(t, pendingContent, content)
	assert.Equal(t, int32(0), reloadCalls.Load())

	cleanup()
	_, statErr := os.Stat(pendingPath)
	assert.True(t, os.IsNotExist(statErr))
	assert.Equal(t, int32(1), reloadCalls.Load())
}

func TestFullGenerationStartsGraceBeforeDeleteEventIsProcessed(t *testing.T) {
	sidecar := newCharacterizationSidecar(t, &stubRuntime{}, &stubReader{})
	pendingConfig := &stubLogConfig{
		name:    "container-1_std_default_config-1",
		content: []byte("pending config"),
	}
	pendingPath, pendingContent := cacheActualConfig(t, sidecar, pendingConfig)
	sidecar.containerCache.Store("container-1", &define.Container{ID: "container-1"})
	delayedCleanup := make(chan func(), 1)
	sidecar.delayCleanFn = func(_ time.Duration, fn func()) {
		delayedCleanup <- fn
	}
	var reloadCalls atomic.Int32
	sidecar.reloadAgentFn = func() error {
		reloadCalls.Add(1)
		return nil
	}

	require.NoError(t, sidecar.generateActualBkLogConfig())
	cleanup := <-delayedCleanup

	content, readErr := os.ReadFile(pendingPath)
	require.NoError(t, readErr)
	assert.Equal(t, pendingContent, content)
	assert.Equal(t, int32(0), reloadCalls.Load())

	cleanup()
	_, statErr := os.Stat(pendingPath)
	assert.True(t, os.IsNotExist(statErr))
	_, cached := sidecar.containerCache.Load("container-1")
	assert.False(t, cached)
	assert.Equal(t, int32(1), reloadCalls.Load())
}

func TestFullGenerationUpgradesStopGraceToCleanContainerCache(t *testing.T) {
	sidecar := newCharacterizationSidecar(t, &stubRuntime{}, &stubReader{})
	pendingConfig := &stubLogConfig{
		name:    "container-1_std_default_config-1",
		content: []byte("pending config"),
	}
	cacheActualConfig(t, sidecar, pendingConfig)
	sidecar.containerCache.Store("container-1", &define.Container{ID: "container-1"})
	deadline := time.Now().Add(time.Minute)
	sidecar.pendingContainerDeletes = map[string]*pendingContainerDeletion{
		"container-1": {
			generation:           1,
			deadline:             deadline,
			deleteContainerCache: false,
		},
	}
	var cleanupScheduled atomic.Bool
	sidecar.delayCleanFn = func(_ time.Duration, _ func()) {
		cleanupScheduled.Store(true)
	}
	desired := desiredConfigSet{}

	sidecar.configMutationMu.Lock()
	err := sidecar.preservePendingContainerConfigsLocked(desired, nil)
	pending := sidecar.pendingContainerDeletes["container-1"]
	sidecar.configMutationMu.Unlock()

	require.NoError(t, err)
	require.NotNil(t, pending)
	assert.True(t, pending.deleteContainerCache)
	assert.Equal(t, deadline, pending.deadline)
	assert.False(t, cleanupScheduled.Load())
	_, preserved := desired[pendingConfig.ConfigName()]
	assert.True(t, preserved)

	require.NoError(t, sidecar.finishPendingContainerDeletion("container-1", pending.generation))
	_, cached := sidecar.containerCache.Load("container-1")
	assert.False(t, cached)
}

func TestFullGenerationPrunesCacheAfterStopCleanupAndMissedDelete(t *testing.T) {
	sidecar := newCharacterizationSidecar(t, &stubRuntime{}, &stubReader{})
	pendingConfig := &stubLogConfig{
		name:    "container-1_std_default_config-1",
		content: []byte("pending config"),
	}
	cacheActualConfig(t, sidecar, pendingConfig)
	sidecar.containerCache.Store("container-1", &define.Container{ID: "container-1"})

	// 模拟只收到 STOP、未收到 DELETE：宽限期到期后配置已经删除，
	// 但 STOP 语义会暂时保留 containerCache。
	sidecar.configMutationMu.Lock()
	pending, _ := sidecar.ensurePendingContainerDeletionLocked("container-1", false)
	sidecar.configMutationMu.Unlock()
	require.NoError(t, sidecar.finishPendingContainerDeletion("container-1", pending.generation))
	_, cachedAfterStopCleanup := sidecar.containerCache.Load("container-1")
	require.True(t, cachedAfterStopCleanup)

	// 后续全量 Runtime 快照已确认容器不存在，应独立于配置 cache 清理残留。
	require.NoError(t, sidecar.generateActualBkLogConfig())
	_, cachedAfterFullGeneration := sidecar.containerCache.Load("container-1")
	assert.False(t, cachedAfterFullGeneration)
}

func TestDeleteDuringFullBuildIsNotCancelledByStaleDesired(t *testing.T) {
	listStarted := make(chan struct{})
	releaseFirstList := make(chan struct{})
	var listCalls atomic.Int32
	runtime := &stubRuntime{
		containersFn: func(context.Context) ([]define.SimpleContainer, error) {
			if listCalls.Add(1) == 1 {
				close(listStarted)
				<-releaseFirstList
				// 第一次 Build 返回 DELETE 之前取得的旧 Runtime 快照。
				return []define.SimpleContainer{{ID: "container-1"}}, nil
			}
			// 世代冲突后的重试读取 DELETE 之后的最新快照。
			return nil, nil
		},
	}
	bkLogConfig := testContainerBkLogConfig(1001)
	reader := &stubReader{
		getFn: func(_ context.Context, _ client.ObjectKey, obj client.Object) error {
			pod := obj.(*corev1.Pod)
			*pod = corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "pod-1"},
			}
			return nil
		},
		listFn: func(_ context.Context, list client.ObjectList) error {
			bkLogConfigs := list.(*bluekingv1alpha1.BkLogConfigList)
			bkLogConfigs.Items = []bluekingv1alpha1.BkLogConfig{bkLogConfig}
			return nil
		},
	}
	sidecar := newCharacterizationSidecar(t, runtime, reader)
	container := testKubernetesContainer()
	sidecar.containerCache.Store(container.ID, container)
	activeConfig := &define.StdOutLogConfig{
		BkLogConfig: bkLogConfig,
		Container:   container,
		Pod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "pod-1"},
		},
		RuntimeType: define.RuntimeTypeContainerd,
	}
	activePath, _ := cacheActualConfig(t, sidecar, activeConfig)
	delayedCleanup := make(chan func(), 1)
	sidecar.delayCleanFn = func(_ time.Duration, cleanup func()) {
		delayedCleanup <- cleanup
	}

	generateDone := make(chan error, 1)
	go func() {
		generateDone <- sidecar.generateActualBkLogConfig()
	}()
	waitForSignal(t, listStarted, "stale full runtime discovery")

	require.NoError(t, sidecar.destroyActionHandler(&define.ContainerEvent{
		Type:        define.ContainerEventDelete,
		ContainerID: container.ID,
	}))
	cleanup := <-delayedCleanup
	close(releaseFirstList)
	require.NoError(t, <-generateDone)

	cleanup()
	_, statErr := os.Stat(activePath)
	assert.True(t, os.IsNotExist(statErr))
	_, pending := sidecar.pendingContainerDeletes[container.ID]
	assert.False(t, pending)
	assert.GreaterOrEqual(t, listCalls.Load(), int32(2))
}

func TestFullGenerationPrunesOnlyMissingConfigForStillDesiredContainer(t *testing.T) {
	sidecar := newCharacterizationSidecar(t, &stubRuntime{}, &stubReader{})
	activeConfig := &stubLogConfig{
		name:    "container-1_std_default_active",
		content: []byte("active config"),
	}
	staleConfig := &stubLogConfig{
		name:    "container-1_std_default_stale",
		content: []byte("stale config"),
	}
	activePath, activeContent := cacheActualConfig(t, sidecar, activeConfig)
	stalePath, _ := cacheActualConfig(t, sidecar, staleConfig)
	var cleanupScheduled atomic.Bool
	sidecar.delayCleanFn = func(_ time.Duration, _ func()) {
		cleanupScheduled.Store(true)
	}
	var reloadCalls atomic.Int32
	sidecar.reloadAgentFn = func() error {
		reloadCalls.Add(1)
		return nil
	}
	desired := desiredConfigSet{
		activeConfig.ConfigName(): {
			logConfig: activeConfig,
			content:   activeContent,
		},
	}

	sidecar.configMutationMu.Lock()
	err := sidecar.preservePendingContainerConfigsLocked(desired, nil)
	if err == nil {
		err = sidecar.applyDesiredConfigsLocked(desired, true, nil, convergenceTriggerDirect)
	}
	sidecar.configMutationMu.Unlock()

	require.NoError(t, err)
	content, readErr := os.ReadFile(activePath)
	require.NoError(t, readErr)
	assert.Equal(t, activeContent, content)
	_, statErr := os.Stat(stalePath)
	assert.True(t, os.IsNotExist(statErr))
	assert.False(t, cleanupScheduled.Load())
	_, pending := sidecar.pendingContainerDeletes["container-1"]
	assert.False(t, pending)
	assert.Equal(t, int32(1), reloadCalls.Load())
}

func TestFullGenerationDoesNotRestartExpiredGraceForPartialMismatch(t *testing.T) {
	sidecar := newCharacterizationSidecar(t, &stubRuntime{}, &stubReader{})
	activeConfig := &stubLogConfig{
		name:    "container-1_std_default_active",
		content: []byte("active config"),
	}
	staleConfig := &stubLogConfig{
		name:    "container-1_std_default_stale",
		content: []byte("stale config"),
	}
	_, activeContent := cacheActualConfig(t, sidecar, activeConfig)
	cacheActualConfig(t, sidecar, staleConfig)
	sidecar.pendingContainerDeletes = map[string]*pendingContainerDeletion{
		"container-1": {
			generation: 1,
			deadline:   time.Now().Add(-time.Second),
		},
	}
	var cleanupScheduled atomic.Bool
	sidecar.delayCleanFn = func(_ time.Duration, _ func()) {
		cleanupScheduled.Store(true)
	}
	desired := desiredConfigSet{
		activeConfig.ConfigName(): {
			logConfig: activeConfig,
			content:   activeContent,
		},
	}

	sidecar.configMutationMu.Lock()
	err := sidecar.preservePendingContainerConfigsLocked(desired, nil)
	sidecar.configMutationMu.Unlock()

	require.NoError(t, err)
	_, preserved := desired[staleConfig.ConfigName()]
	assert.False(t, preserved)
	assert.False(t, cleanupScheduled.Load())
	_, pending := sidecar.pendingContainerDeletes["container-1"]
	assert.False(t, pending)
}

func TestCancelledPendingCleanupDoesNotDeleteRestartedContainerConfig(t *testing.T) {
	sidecar := newCharacterizationSidecar(t, &stubRuntime{}, &stubReader{})
	pendingConfig := &stubLogConfig{
		name:    "container-1_std_default_config-1",
		content: []byte("restarted container config"),
	}
	pendingPath, pendingContent := cacheActualConfig(t, sidecar, pendingConfig)
	var reloadCalls atomic.Int32
	sidecar.reloadAgentFn = func() error {
		reloadCalls.Add(1)
		return nil
	}

	oldCleanup := scheduleDestroyCleanup(t, sidecar, "container-1")
	sidecar.cancelPendingContainerDeletion("container-1")
	oldCleanup()

	content, readErr := os.ReadFile(pendingPath)
	require.NoError(t, readErr)
	assert.Equal(t, pendingContent, content)
	assert.Equal(t, int32(0), reloadCalls.Load())
}

func TestPendingCleanupIsCancelledWithContainerWorkqueueShutdown(t *testing.T) {
	sidecar := newCharacterizationSidecar(t, &stubRuntime{}, &stubReader{})
	pendingConfig := &stubLogConfig{
		name:    "container-1_std_default_config-1",
		content: []byte("pending config"),
	}
	pendingPath, pendingContent := cacheActualConfig(t, sidecar, pendingConfig)

	sidecar.configMutationMu.Lock()
	pending, _ := sidecar.ensurePendingContainerDeletionLocked("container-1", true)
	sidecar.configMutationMu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	sidecar.startContainerEventWorker(ctx)
	sidecar.schedulePendingContainerCleanup("container-1", pending.generation, 50*time.Millisecond)
	sidecar.shutdownContainerEventQueue()
	cancel()
	sidecar.lifecycleWG.Wait()

	// 超过原定 deadline 后文件仍存在，证明延迟任务随 workqueue 一起退出，
	// 不会在 controller-runtime 已停止后继续修改配置并触发 reload。
	time.Sleep(100 * time.Millisecond)
	content, err := os.ReadFile(pendingPath)
	require.NoError(t, err)
	assert.Equal(t, pendingContent, content)
}

func TestReconcileDoesNotKeepPendingConfigForChangedSource(t *testing.T) {
	scheme := k8sruntime.NewScheme()
	require.NoError(t, bluekingv1alpha1.AddToScheme(scheme))
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&bluekingv1alpha1.BkLogConfig{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "config-1"},
	}).Build()
	sidecar := newCharacterizationSidecar(t, &stubRuntime{}, &stubReader{})
	pendingConfig := &stubLogConfig{
		name:    "container-1_std_default_config-1",
		content: []byte("pending config"),
	}
	pendingPath, _ := cacheActualConfig(t, sidecar, pendingConfig)
	var reloadCalls atomic.Int32
	sidecar.reloadAgentFn = func() error {
		reloadCalls.Add(1)
		return nil
	}

	cleanup := scheduleDestroyCleanup(t, sidecar, "container-1")
	reconciler := &BkLogConfigReconciler{
		Client:       kubeClient,
		Scheme:       scheme,
		BkLogSidecar: sidecar,
	}

	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "config-1"},
	})

	require.NoError(t, err)
	_, statErr := os.Stat(pendingPath)
	assert.True(t, os.IsNotExist(statErr))
	assert.Equal(t, int32(1), reloadCalls.Load())

	cleanup()
	assert.Equal(t, int32(1), reloadCalls.Load())
}

func TestReconcileKeepsPendingConfigForUnchangedSource(t *testing.T) {
	scheme := k8sruntime.NewScheme()
	require.NoError(t, bluekingv1alpha1.AddToScheme(scheme))
	source := bluekingv1alpha1.BkLogConfig{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "config-1"},
		Spec: bluekingv1alpha1.BkLogConfigSpec{
			LogConfigType: config.ContainerLogConfig,
			Path:          []string{"/var/log/app.log"},
		},
	}
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(source.DeepCopy()).Build()
	sidecar := newCharacterizationSidecar(t, &stubRuntime{}, &stubReader{})
	pendingConfig := &define.ContainerLogConfig{
		BkLogConfig: source,
		Container: &define.Container{
			ID:     "container-1",
			Labels: map[string]string{},
		},
		Pod: &corev1.Pod{},
	}
	pendingPath, pendingContent := cacheActualConfig(t, sidecar, pendingConfig)
	cleanup := scheduleDestroyCleanup(t, sidecar, "container-1")
	reconciler := &BkLogConfigReconciler{
		Client:       kubeClient,
		Scheme:       scheme,
		BkLogSidecar: sidecar,
	}

	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "config-1"},
	})

	require.NoError(t, err)
	content, readErr := os.ReadFile(pendingPath)
	require.NoError(t, readErr)
	assert.Equal(t, pendingContent, content)

	cleanup()
	_, statErr := os.Stat(pendingPath)
	assert.True(t, os.IsNotExist(statErr))
}
