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
	return "container-1_" + config.StdLogConfig + "_default_collect-all"
}
