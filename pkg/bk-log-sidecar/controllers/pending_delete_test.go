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
	assert.Equal(t, int32(1), reloadCalls.Load())
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
