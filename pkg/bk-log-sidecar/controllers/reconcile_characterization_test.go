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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	bluekingv1alpha1 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/api/bk.tencent.com/v1alpha1"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/define"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type stubReader struct {
	getFn  func(context.Context, client.ObjectKey, client.Object) error
	listFn func(context.Context, client.ObjectList) error
}

type getErrorClient struct {
	client.Client
	err error
}

func (c *getErrorClient) Get(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error {
	return c.err
}

func (r *stubReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
	if r.getFn == nil {
		return nil
	}
	return r.getFn(ctx, key, obj)
}

func (r *stubReader) List(ctx context.Context, list client.ObjectList, _ ...client.ListOption) error {
	if r.listFn == nil {
		return nil
	}
	return r.listFn(ctx, list)
}

type stubRuntime struct {
	containersFn func(context.Context) ([]define.SimpleContainer, error)
	inspectFn    func(context.Context, string) (define.Container, error)
	subscribeFn  func(context.Context) (<-chan *define.ContainerEvent, <-chan error)
	runtimeType  define.RuntimeType
}

type stubLogConfig struct {
	name    string
	content []byte
	err     error
}

func (c *stubLogConfig) Config() ([]byte, error) {
	return c.content, c.err
}

func (c *stubLogConfig) ConfigName() string {
	return c.name
}

func (r *stubRuntime) Containers(ctx context.Context) ([]define.SimpleContainer, error) {
	if r.containersFn == nil {
		return nil, nil
	}
	return r.containersFn(ctx)
}

func (r *stubRuntime) Inspect(ctx context.Context, containerID string) (define.Container, error) {
	if r.inspectFn == nil {
		return define.Container{}, nil
	}
	return r.inspectFn(ctx, containerID)
}

func (r *stubRuntime) Subscribe(ctx context.Context) (<-chan *define.ContainerEvent, <-chan error) {
	if r.subscribeFn == nil {
		return nil, nil
	}
	return r.subscribeFn(ctx)
}

func (r *stubRuntime) Type() define.RuntimeType {
	if r.runtimeType == "" {
		return define.RuntimeTypeContainerd
	}
	return r.runtimeType
}

func newCharacterizationSidecar(t *testing.T, runtime define.Runtime, reader client.Reader) *BkLogSidecar {
	t.Helper()

	oldConfigPath := config.BkunifylogbeatConfig
	oldPIDFile := config.BkunifylogbeatPidFile
	config.BkunifylogbeatConfig = t.TempDir()
	config.BkunifylogbeatPidFile = filepath.Join(t.TempDir(), "missing.pid")
	t.Cleanup(func() {
		config.BkunifylogbeatConfig = oldConfigPath
		config.BkunifylogbeatPidFile = oldPIDFile
	})

	return &BkLogSidecar{
		runtime:       runtime,
		kubeClient:    reader,
		reloadAgentFn: func() error { return nil },
		log:           logr.Discard(),
		stopCh:        make(chan struct{}),
	}
}

func waitForSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

func TestStartRunsInitialGenerationBeforeRuntimeSubscriptionIsReady(t *testing.T) {
	initialGenerationStarted := make(chan struct{})
	releaseSubscription := make(chan struct{})
	subscriptionReady := make(chan struct{})
	var listCalls atomic.Int32

	runtime := &stubRuntime{
		containersFn: func(context.Context) ([]define.SimpleContainer, error) {
			if listCalls.Add(1) == 2 {
				close(initialGenerationStarted)
			}
			return nil, nil
		},
		subscribeFn: func(context.Context) (<-chan *define.ContainerEvent, <-chan error) {
			<-releaseSubscription
			close(subscriptionReady)
			return nil, nil
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

	waitForSignal(t, initialGenerationStarted, "initial configuration generation")
	select {
	case <-subscriptionReady:
		t.Fatal("runtime subscription unexpectedly became ready before initial generation")
	default:
	}

	close(releaseSubscription)
	waitForSignal(t, subscriptionReady, "runtime subscription")
	require.NoError(t, <-startDone)
	stop()
}

func TestGenerateKeepsExistingConfigWhenContainerDiscoveryFails(t *testing.T) {
	discoveryErr := errors.New("runtime list unavailable")
	runtime := &stubRuntime{
		containersFn: func(context.Context) ([]define.SimpleContainer, error) {
			return nil, discoveryErr
		},
	}
	sidecar := newCharacterizationSidecar(t, runtime, &stubReader{})
	existingConfig := filepath.Join(config.BkunifylogbeatConfig, "existing.conf")
	require.NoError(t, os.WriteFile(existingConfig, []byte("existing"), 0o600))

	err := sidecar.generateActualBkLogConfig()

	assert.ErrorIs(t, err, discoveryErr)
	content, readErr := os.ReadFile(existingConfig)
	require.NoError(t, readErr)
	assert.Equal(t, []byte("existing"), content)
}

func TestGenerateReturnsNodeReadFailure(t *testing.T) {
	nodeReadErr := errors.New("node cache unavailable")
	reader := &stubReader{
		getFn: func(context.Context, client.ObjectKey, client.Object) error {
			return nodeReadErr
		},
		listFn: func(_ context.Context, list client.ObjectList) error {
			bkLogConfigList := list.(*bluekingv1alpha1.BkLogConfigList)
			bkLogConfigList.Items = []bluekingv1alpha1.BkLogConfig{{
				ObjectMeta: metav1.ObjectMeta{Name: "node-config"},
				Spec: bluekingv1alpha1.BkLogConfigSpec{
					LogConfigType: config.NodeLogConfig,
				},
			}}
			return nil
		},
	}
	t.Setenv(config.CurrentNodeNameKey, "node-1")
	sidecar := newCharacterizationSidecar(t, &stubRuntime{}, reader)

	err := sidecar.generateActualBkLogConfig()

	assert.ErrorIs(t, err, nodeReadErr)
}

func TestMatchBkLogConfigsReturnsPodReadFailure(t *testing.T) {
	podReadErr := errors.New("pod cache unavailable")
	reader := &stubReader{
		getFn: func(context.Context, client.ObjectKey, client.Object) error {
			return podReadErr
		},
	}
	sidecar := newCharacterizationSidecar(t, &stubRuntime{}, reader)
	container := &define.Container{
		ID: "container-1",
		Labels: map[string]string{
			config.ContainerLabelK8sPodNamespace: "default",
			config.ContainerLabelK8sPodName:      "pod-1",
		},
	}

	matched, pod, err := sidecar.matchBklogConfigs(container)

	assert.Empty(t, matched)
	assert.Equal(t, corev1.Pod{}, *pod)
	assert.ErrorIs(t, err, podReadErr)
}

func TestMatchBkLogConfigsTreatsMissingPodAsNoMatch(t *testing.T) {
	reader := &stubReader{
		getFn: func(context.Context, client.ObjectKey, client.Object) error {
			return apierrors.NewNotFound(schema.GroupResource{Resource: "pods"}, "pod-1")
		},
	}
	sidecar := newCharacterizationSidecar(t, &stubRuntime{}, reader)
	container := &define.Container{
		ID: "container-1",
		Labels: map[string]string{
			config.ContainerLabelK8sPodNamespace: "default",
			config.ContainerLabelK8sPodName:      "pod-1",
		},
	}

	matched, pod, err := sidecar.matchBklogConfigs(container)

	assert.NoError(t, err)
	assert.Empty(t, matched)
	assert.Equal(t, corev1.Pod{}, *pod)
}

func TestReconcileReturnsErrorWhenReloadFailsForDeletedConfig(t *testing.T) {
	scheme := k8sruntime.NewScheme()
	require.NoError(t, bluekingv1alpha1.AddToScheme(scheme))
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	reloadErr := errors.New("reload unavailable")
	var reloadCalls atomic.Int32
	sidecar := newCharacterizationSidecar(t, &stubRuntime{}, &stubReader{})
	staleConfig := &stubLogConfig{name: "container_std_default_missing", content: []byte("stale config")}
	sidecar.actualBkLogConfigCache.Store(staleConfig.ConfigName(), staleConfig)
	require.NoError(t, os.WriteFile(
		filepath.Join(config.BkunifylogbeatConfig, staleConfig.ConfigName()+generatedConfigSuffix),
		staleConfig.content,
		0o600,
	))
	sidecar.reloadAgentFn = func() error {
		reloadCalls.Add(1)
		return reloadErr
	}
	reconciler := &BkLogConfigReconciler{
		Client:       kubeClient,
		Scheme:       scheme,
		BkLogSidecar: sidecar,
	}

	result, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "missing"},
	})

	assert.ErrorIs(t, err, reloadErr)
	assert.Equal(t, ctrl.Result{}, result)
	assert.Equal(t, int32(1), reloadCalls.Load())
}

func TestReconcileReturnsKubernetesReadFailure(t *testing.T) {
	scheme := k8sruntime.NewScheme()
	reconcileErr := errors.New("kubernetes cache unavailable")
	reconciler := &BkLogConfigReconciler{
		Client: &getErrorClient{
			Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
			err:    reconcileErr,
		},
		Scheme:       scheme,
		BkLogSidecar: newCharacterizationSidecar(t, &stubRuntime{}, &stubReader{}),
	}

	result, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "config-1"},
	})

	assert.ErrorIs(t, err, reconcileErr)
	assert.Equal(t, ctrl.Result{}, result)
}

func TestReconcileReturnsConfigurationGenerationFailure(t *testing.T) {
	scheme := k8sruntime.NewScheme()
	require.NoError(t, bluekingv1alpha1.AddToScheme(scheme))
	bkLogConfig := &bluekingv1alpha1.BkLogConfig{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "config-1"},
	}
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(bkLogConfig).Build()
	listErr := errors.New("BkLogConfig cache unavailable")
	sidecar := newCharacterizationSidecar(t, &stubRuntime{}, &stubReader{
		listFn: func(context.Context, client.ObjectList) error {
			return listErr
		},
	})
	existingConfig := &stubLogConfig{name: "container_std_default_config-1", content: []byte("last known good")}
	sidecar.actualBkLogConfigCache.Store(existingConfig.ConfigName(), existingConfig)
	existingPath := filepath.Join(config.BkunifylogbeatConfig, existingConfig.ConfigName()+generatedConfigSuffix)
	require.NoError(t, os.WriteFile(existingPath, existingConfig.content, 0o600))
	reconciler := &BkLogConfigReconciler{
		Client:       kubeClient,
		Scheme:       scheme,
		BkLogSidecar: sidecar,
	}

	result, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "config-1"},
	})

	assert.ErrorIs(t, err, listErr)
	assert.Equal(t, ctrl.Result{}, result)
	content, readErr := os.ReadFile(existingPath)
	require.NoError(t, readErr)
	assert.Equal(t, existingConfig.content, content)
	cached, ok := sidecar.actualBkLogConfigCache.Load(existingConfig.ConfigName())
	require.True(t, ok)
	assert.Same(t, existingConfig, cached)
}

func TestReconcileSucceedsAfterTransientReloadFailureRecovers(t *testing.T) {
	scheme := k8sruntime.NewScheme()
	require.NoError(t, bluekingv1alpha1.AddToScheme(scheme))
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	reloadErr := errors.New("reload temporarily unavailable")
	var reloadCalls atomic.Int32
	sidecar := newCharacterizationSidecar(t, &stubRuntime{}, &stubReader{})
	staleConfig := &stubLogConfig{name: "container_std_default_missing", content: []byte("stale config")}
	sidecar.actualBkLogConfigCache.Store(staleConfig.ConfigName(), staleConfig)
	require.NoError(t, os.WriteFile(
		filepath.Join(config.BkunifylogbeatConfig, staleConfig.ConfigName()+generatedConfigSuffix),
		staleConfig.content,
		0o600,
	))
	sidecar.reloadAgentFn = func() error {
		if reloadCalls.Add(1) == 1 {
			return reloadErr
		}
		return nil
	}
	reconciler := &BkLogConfigReconciler{
		Client:       kubeClient,
		Scheme:       scheme,
		BkLogSidecar: sidecar,
	}
	request := ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "missing"},
	}

	_, firstErr := reconciler.Reconcile(context.Background(), request)
	secondResult, secondErr := reconciler.Reconcile(context.Background(), request)

	assert.ErrorIs(t, firstErr, reloadErr)
	assert.NoError(t, secondErr)
	assert.Equal(t, ctrl.Result{}, secondResult)
	assert.Equal(t, int32(2), reloadCalls.Load())
}

func TestCacheRefreshDoesNotReloadConfiguration(t *testing.T) {
	runtime := &stubRuntime{
		containersFn: func(context.Context) ([]define.SimpleContainer, error) {
			return []define.SimpleContainer{{ID: "container-1"}}, nil
		},
		inspectFn: func(_ context.Context, containerID string) (define.Container, error) {
			return define.Container{ID: containerID}, nil
		},
	}
	sidecar := newCharacterizationSidecar(t, runtime, &stubReader{})
	var reloadCalls atomic.Int32
	sidecar.reloadAgentFn = func() error {
		reloadCalls.Add(1)
		return nil
	}

	require.NoError(t, sidecar.cacheContainer())

	_, ok := sidecar.containerCache.Load("container-1")
	assert.True(t, ok)
	assert.Equal(t, int32(0), reloadCalls.Load())
}

func TestContainerByIDReturnsInspectFailure(t *testing.T) {
	inspectErr := errors.New("runtime inspect unavailable")
	sidecar := newCharacterizationSidecar(t, &stubRuntime{
		inspectFn: func(context.Context, string) (define.Container, error) {
			return define.Container{}, inspectErr
		},
	}, &stubReader{})

	container, err := sidecar.containerByID("container-1")

	assert.Nil(t, container)
	assert.ErrorIs(t, err, inspectErr)
}

func TestContainerByIDTreatsRuntimeNotFoundAsNormalDisappearance(t *testing.T) {
	sidecar := newCharacterizationSidecar(t, &stubRuntime{
		inspectFn: func(context.Context, string) (define.Container, error) {
			return define.Container{}, fmt.Errorf("runtime inspect: %w", define.ErrContainerNotFound)
		},
	}, &stubReader{})

	container, err := sidecar.containerByID("container-1")

	assert.NoError(t, err)
	assert.Nil(t, container)
}
