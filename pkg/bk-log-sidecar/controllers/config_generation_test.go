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
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	bluekingv1alpha1 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/api/bk.tencent.com/v1alpha1"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/define"
)

func TestCreateEventRebuildsWhenFullReconcileCommitsNewerConfig(t *testing.T) {
	firstListStarted := make(chan struct{})
	releaseFirstList := make(chan struct{})
	var listCalls atomic.Int32

	oldConfig := testContainerBkLogConfig(1001)
	newConfig := testContainerBkLogConfig(2002)
	reader := &stubReader{
		getFn: func(_ context.Context, _ client.ObjectKey, obj client.Object) error {
			pod := obj.(*corev1.Pod)
			*pod = corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "pod-1",
				},
			}
			return nil
		},
		listFn: func(_ context.Context, list client.ObjectList) error {
			bkLogConfigs := list.(*bluekingv1alpha1.BkLogConfigList)
			if listCalls.Add(1) == 1 {
				bkLogConfigs.Items = []bluekingv1alpha1.BkLogConfig{oldConfig}
				close(firstListStarted)
				<-releaseFirstList
				return nil
			}
			bkLogConfigs.Items = []bluekingv1alpha1.BkLogConfig{newConfig}
			return nil
		},
	}
	sidecar := newCharacterizationSidecar(t, &stubRuntime{}, reader)
	container := testKubernetesContainer()

	eventDone := make(chan error, 1)
	go func() {
		_, err := sidecar.upsertContainerConfigs(container, true)
		eventDone <- err
	}()
	waitForSignal(t, firstListStarted, "stale CREATE event build")

	newDesired, err := renderDesiredConfigs([]define.LogConfigType{
		&define.StdOutLogConfig{
			BkLogConfig: newConfig,
			Container:   container,
			Pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "pod-1"},
			},
			RuntimeType: define.RuntimeTypeContainerd,
		},
	})
	require.NoError(t, err)
	require.NoError(t, sidecar.applyDesiredConfigs(newDesired))
	close(releaseFirstList)
	require.NoError(t, <-eventDone)

	expected := newDesired[configNameForTestContainer()].content
	actual, err := os.ReadFile(filepath.Join(
		config.BkunifylogbeatConfig,
		configNameForTestContainer()+generatedConfigSuffix,
	))
	require.NoError(t, err)
	assert.Equal(t, expected, actual)
	assert.NotContains(t, string(actual), "dataid: 1001")
}

func TestFullBuildDoesNotHoldConfigMutationLockDuringRuntimeIO(t *testing.T) {
	firstListStarted := make(chan struct{})
	releaseFirstList := make(chan struct{})
	retryErr := errors.New("stop after generation retry")
	var listCalls atomic.Int32
	runtime := &stubRuntime{
		containersFn: func(context.Context) ([]define.SimpleContainer, error) {
			if listCalls.Add(1) == 1 {
				close(firstListStarted)
				<-releaseFirstList
				return nil, nil
			}
			return nil, retryErr
		},
	}
	sidecar := newCharacterizationSidecar(t, runtime, &stubReader{})
	generateDone := make(chan error, 1)
	go func() {
		generateDone <- sidecar.generateActualBkLogConfig()
	}()
	waitForSignal(t, firstListStarted, "full runtime discovery")

	desired, err := renderDesiredConfigs([]define.LogConfigType{
		&stubLogConfig{name: "event-config", content: []byte("event config")},
	})
	require.NoError(t, err)
	applyDone := make(chan error, 1)
	go func() {
		applyDone <- sidecar.applyDesiredConfigs(desired)
	}()
	select {
	case err := <-applyDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("incremental Apply was blocked by Runtime discovery")
	}

	close(releaseFirstList)
	assert.ErrorIs(t, <-generateDone, retryErr)
}

func TestCreateDuringFullBuildIsNotPrunedByStaleRuntimeSnapshot(t *testing.T) {
	firstListStarted := make(chan struct{})
	releaseFirstList := make(chan struct{})
	var listCalls atomic.Int32
	container := testKubernetesContainer()
	runtime := &stubRuntime{
		containersFn: func(context.Context) ([]define.SimpleContainer, error) {
			if listCalls.Add(1) == 1 {
				close(firstListStarted)
				<-releaseFirstList
				// CREATE 之前取得的旧快照还看不到新容器。
				return nil, nil
			}
			return []define.SimpleContainer{{ID: container.ID}}, nil
		},
		inspectFn: func(context.Context, string) (define.Container, error) {
			return *container, nil
		},
	}
	sidecar := newCharacterizationSidecar(t, runtime, &stubReader{})
	generateDone := make(chan error, 1)
	go func() {
		generateDone <- sidecar.generateActualBkLogConfig()
	}()
	waitForSignal(t, firstListStarted, "stale full runtime discovery before CREATE")

	// 即使新容器暂时没有匹配任何采集配置，CREATE 也必须推进状态世代，
	// 让旧 Runtime 快照重试，而不是把刚写入的 containerCache 当残留删除。
	require.NoError(t, sidecar.startActionHandler(context.Background(), &define.ContainerEvent{
		Type:        define.ContainerEventCreate,
		ContainerID: container.ID,
	}))
	close(releaseFirstList)
	require.NoError(t, <-generateDone)

	cached, ok := sidecar.containerCache.Load(container.ID)
	require.True(t, ok)
	assert.Equal(t, container.ID, castContainer(cached).ID)
	assert.GreaterOrEqual(t, listCalls.Load(), int32(2))
}

func TestFullReconcileBoundsContinuousSnapshotChanges(t *testing.T) {
	var sidecar *BkLogSidecar
	var listCalls atomic.Int32
	runtime := &stubRuntime{
		containersFn: func(context.Context) ([]define.SimpleContainer, error) {
			listCalls.Add(1)
			// 模拟 Build 期间持续有事件提交新状态。生产代码只能在当前调用内
			// 合并一次，随后应把重试交给外层限速机制，不能原地忙等。
			sidecar.configMutationMu.Lock()
			sidecar.configGeneration++
			sidecar.configMutationMu.Unlock()
			return nil, nil
		},
	}
	sidecar = newCharacterizationSidecar(t, runtime, &stubReader{})

	err := sidecar.generateActualBkLogConfigWithOptions(context.Background(), configGenerationOptions{})

	assert.ErrorIs(t, err, errConfigSnapshotChanged)
	assert.EqualValues(t, maxImmediateConfigSnapshotRetries+1, listCalls.Load())
}

func TestCreateUpsertBoundsContinuousSnapshotChangesWithEmptyMatch(t *testing.T) {
	var sidecar *BkLogSidecar
	var listCalls atomic.Int32
	reader := &stubReader{
		listFn: func(_ context.Context, list client.ObjectList) error {
			listCalls.Add(1)
			sidecar.configMutationMu.Lock()
			sidecar.configGeneration++
			sidecar.configMutationMu.Unlock()
			list.(*bluekingv1alpha1.BkLogConfigList).Items = nil
			return nil
		},
	}
	sidecar = newCharacterizationSidecar(t, &stubRuntime{}, reader)

	matched, err := sidecar.upsertContainerConfigs(testKubernetesContainer(), true)

	assert.False(t, matched)
	assert.ErrorIs(t, err, errConfigSnapshotChanged)
	assert.EqualValues(t, maxImmediateConfigSnapshotRetries+1, listCalls.Load())
}

func TestCreateUpsertBoundsContinuousSnapshotChangesBeforeApply(t *testing.T) {
	var sidecar *BkLogSidecar
	var listCalls atomic.Int32
	bkLogConfig := testContainerBkLogConfig(1001)
	reader := containerConfigTestReader(&bkLogConfig)
	reader.listFn = func(_ context.Context, list client.ObjectList) error {
		listCalls.Add(1)
		sidecar.configMutationMu.Lock()
		sidecar.configGeneration++
		sidecar.configMutationMu.Unlock()
		list.(*bluekingv1alpha1.BkLogConfigList).Items = []bluekingv1alpha1.BkLogConfig{bkLogConfig}
		return nil
	}
	sidecar = newCharacterizationSidecar(t, &stubRuntime{}, reader)

	matched, err := sidecar.upsertContainerConfigs(testKubernetesContainer(), true)

	assert.False(t, matched)
	assert.ErrorIs(t, err, errConfigSnapshotChanged)
	assert.EqualValues(t, maxImmediateConfigSnapshotRetries+1, listCalls.Load())
}

func TestPeriodicReconcilePreservesContainerTailFilesSemantics(t *testing.T) {
	extFalse := false
	extTrue := true
	tests := []struct {
		name              string
		logConfigType     string
		isNewContainer    bool
		extTailFiles      *bool
		expectedTailFiles bool
	}{
		{
			name:              "stdout dynamic create decision",
			logConfigType:     config.StdLogConfig,
			isNewContainer:    true,
			expectedTailFiles: false,
		},
		{
			name:              "container dynamic create decision",
			logConfigType:     config.ContainerLogConfig,
			isNewContainer:    true,
			expectedTailFiles: false,
		},
		{
			name:              "stdout ext false overrides existing decision",
			logConfigType:     config.StdLogConfig,
			isNewContainer:    false,
			extTailFiles:      &extFalse,
			expectedTailFiles: false,
		},
		{
			name:              "container ext true overrides create decision",
			logConfigType:     config.ContainerLogConfig,
			isNewContainer:    true,
			extTailFiles:      &extTrue,
			expectedTailFiles: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(config.CurrentNodeNameKey, "node-1")
			container := testKubernetesContainer()
			bkLogConfig := testContainerBkLogConfig(1001)
			bkLogConfig.UID = "bklogconfig-uid-1"
			bkLogConfig.Spec.LogConfigType = tt.logConfigType
			if tt.logConfigType == config.ContainerLogConfig {
				bkLogConfig.Spec.Path = []string{"/var/log/app/*.log"}
			}
			if tt.extTailFiles != nil {
				bkLogConfig.Spec.ExtOptions = map[string]k8sruntime.RawExtension{
					"tail_files": {Raw: []byte(fmt.Sprintf("%t", *tt.extTailFiles))},
				}
			}
			runtime := &stubRuntime{
				containersFn: func(context.Context) ([]define.SimpleContainer, error) {
					return []define.SimpleContainer{{ID: container.ID}}, nil
				},
				inspectFn: func(context.Context, string) (define.Container, error) {
					return *container, nil
				},
			}
			sidecar := newCharacterizationSidecar(t, runtime, containerConfigTestReader(&bkLogConfig))
			var reloadCalls atomic.Int32
			sidecar.reloadAgentFn = func() error {
				reloadCalls.Add(1)
				return nil
			}

			matched, err := sidecar.upsertContainerConfigs(container, tt.isNewContainer)
			require.NoError(t, err)
			require.True(t, matched)
			configPath := filepath.Join(
				config.BkunifylogbeatConfig,
				configNameForTestContainerType(tt.logConfigType)+generatedConfigSuffix,
			)
			createContent, err := os.ReadFile(configPath)
			require.NoError(t, err)
			require.Contains(t, string(createContent),
				fmt.Sprintf("tail_files: %t", tt.expectedTailFiles))

			// 直接调用生产的 periodic 入口，同时覆盖 Node refresh 和
			// refreshContainerInfo=true 的 Runtime Inspect 路径。
			require.NoError(t, sidecar.generateActualBkLogConfigForPeriodicReconcile(context.Background()))
			periodicContent, err := os.ReadFile(configPath)
			require.NoError(t, err)
			assert.Equal(t, createContent, periodicContent)
			assert.EqualValues(t, 1, reloadCalls.Load(),
				"周期全量不应仅因 tail_files 运行时决策改变而 reload")

			// BkLogConfig 更新会保持 UID。声明配置应正常更新，但
			// sidecar 已作出的动态 tail_files 决策不应随之漂移。
			bkLogConfig.Spec.DataId = 2002
			require.NoError(t, sidecar.generateActualBkLogConfigForPeriodicReconcile(context.Background()))
			updatedContent, err := os.ReadFile(configPath)
			require.NoError(t, err)
			assert.Contains(t, string(updatedContent), "dataid: 2002")
			assert.Contains(t, string(updatedContent),
				fmt.Sprintf("tail_files: %t", tt.expectedTailFiles))
			assert.EqualValues(t, 2, reloadCalls.Load(),
				"同 UID 的声明配置变更仍应正常落盘并 reload")
		})
	}
}

func TestFullReconcileUsesCreateTailFilesWhileInspectRetryIsPending(t *testing.T) {
	container := testKubernetesContainer()
	bkLogConfig := testContainerBkLogConfig(1001)
	bkLogConfig.UID = "bklogconfig-uid-1"
	runtime := &stubRuntime{
		containersFn: func(context.Context) ([]define.SimpleContainer, error) {
			return []define.SimpleContainer{{ID: container.ID}}, nil
		},
		inspectFn: func(context.Context, string) (define.Container, error) {
			return *container, nil
		},
	}
	t.Setenv(config.CurrentNodeNameKey, "node-1")
	sidecar := newCharacterizationSidecar(t, runtime, containerConfigTestReader(&bkLogConfig))
	sidecar.recordPendingContainerCreate(container.ID, 1, time.Now().Add(time.Second))

	require.NoError(t, sidecar.generateActualBkLogConfigForPeriodicReconcile(context.Background()))
	content, err := os.ReadFile(filepath.Join(
		config.BkunifylogbeatConfig,
		configNameForTestContainer()+generatedConfigSuffix,
	))
	require.NoError(t, err)
	assert.Contains(t, string(content), "tail_files: false")
}

func TestFullReconcileDoesNotReuseTailFilesAcrossBkLogConfigLifecycle(t *testing.T) {
	container := testKubernetesContainer()
	oldConfig := testContainerBkLogConfig(1001)
	oldConfig.UID = "bklogconfig-uid-old"
	newConfig := oldConfig
	newConfig.UID = "bklogconfig-uid-new"
	sidecar := newCharacterizationSidecar(t, &stubRuntime{}, &stubReader{})
	sidecar.actualBkLogConfigCache.Store(configNameForTestContainer(), &define.StdOutLogConfig{
		BkLogConfig: oldConfig,
		Container:   container,
	})
	generated := &define.StdOutLogConfig{
		BkLogConfig: newConfig,
		Container:   container,
	}
	generated.BkLogConfig.Spec.TailFiles = true

	sidecar.preserveRuntimeTailFiles(generated)

	assert.True(t, generated.BkLogConfig.Spec.TailFiles,
		"同名 CR 重建后不应继承旧 UID 的运行时 tail_files 状态")
}

func containerConfigTestReader(bkLogConfig *bluekingv1alpha1.BkLogConfig) *stubReader {
	return &stubReader{
		getFn: func(_ context.Context, key client.ObjectKey, obj client.Object) error {
			switch typed := obj.(type) {
			case *corev1.Node:
				*typed = corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: key.Name}}
			case *corev1.Pod:
				*typed = corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "default",
						Name:      "pod-1",
					},
				}
			default:
				return fmt.Errorf("unexpected test object type %T", obj)
			}
			return nil
		},
		listFn: func(_ context.Context, list client.ObjectList) error {
			bkLogConfigs := list.(*bluekingv1alpha1.BkLogConfigList)
			bkLogConfigs.Items = []bluekingv1alpha1.BkLogConfig{*bkLogConfig.DeepCopy()}
			return nil
		},
	}
}

func testContainerBkLogConfig(dataID int64) bluekingv1alpha1.BkLogConfig {
	return bluekingv1alpha1.BkLogConfig{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "collect-all",
		},
		Spec: bluekingv1alpha1.BkLogConfigSpec{
			DataId:        dataID,
			LogConfigType: config.StdLogConfig,
			AllContainer:  true,
		},
	}
}

func testKubernetesContainer() *define.Container {
	return &define.Container{
		ID:      "container-1",
		LogPath: "/var/log/pods/default_pod-1/app/0.log",
		Labels: map[string]string{
			config.ContainerLabelK8sContainerName: "app",
			config.ContainerLabelK8sPodName:       "pod-1",
			config.ContainerLabelK8sPodNamespace:  "default",
		},
	}
}

func configNameForTestContainer() string {
	return configNameForTestContainerType(config.StdLogConfig)
}

func configNameForTestContainerType(logConfigType string) string {
	return "container-1_" + logConfigType + "_default_collect-all"
}
