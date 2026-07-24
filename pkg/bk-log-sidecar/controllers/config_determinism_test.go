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
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	bluekingv1alpha1 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/api/bk.tencent.com/v1alpha1"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/define"
)

func TestContainerLogConfigMountRenderingIsDeterministic(t *testing.T) {
	logConfig := &define.ContainerLogConfig{
		BkLogConfig: bluekingv1alpha1.BkLogConfig{
			ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "container-files"},
			Spec: bluekingv1alpha1.BkLogConfigSpec{
				DataId:        1001,
				LogConfigType: config.ContainerLogConfig,
				Path:          []string{"/var/log/app/*.log"},
			},
		},
		Container: &define.Container{
			ID:       "container-1",
			RootPath: "/runtime/root/container-1",
			Labels: map[string]string{
				config.ContainerLabelK8sContainerName: "app",
			},
			Mounts: []define.Mount{
				{HostPath: "/host/z-log", ContainerPath: "/var/log"},
				{HostPath: "/host/a-var", ContainerPath: "/var"},
			},
		},
		Pod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "pod-1"},
		},
	}

	first, err := logConfig.Config()
	require.NoError(t, err)
	for i := 0; i < 200; i++ {
		rendered, err := logConfig.Config()
		require.NoError(t, err)
		require.True(t, bytes.Equal(first, rendered), "rendering changed on iteration %d", i)
	}
}
