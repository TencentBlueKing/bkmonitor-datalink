// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 日志平台 (BlueKing - Log) available.
// Copyright (C) 2017-2021 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

package define

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/api/bk.tencent.com/v1alpha1"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/config"
	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
)

// parsedContainerConfig 解析 ContainerLogConfig.Config() 产物中关注的字段
type parsedContainerConfig struct {
	Local []struct {
		Path   []string `yaml:"paths"`
		Mounts []Mount  `yaml:"mounts"`
		RootFs string   `yaml:"root_fs"`
	} `yaml:"local"`
}

func newContainerLogConfig(mounts []Mount, paths []string) *ContainerLogConfig {
	return &ContainerLogConfig{
		BkLogConfig: v1alpha1.BkLogConfig{
			Spec: v1alpha1.BkLogConfigSpec{
				DataId: 11,
				Path:   paths,
			},
		},
		Container: &Container{
			ID:       "container-xxx",
			RootPath: "/proc/1/root",
			Mounts:   mounts,
		},
		Pod: &corev1.Pod{},
	}
}

func parseContainerConfig(t *testing.T, content []byte) parsedContainerConfig {
	var parsed parsedContainerConfig
	assert.NoError(t, yaml.Unmarshal(content, &parsed))
	assert.Len(t, parsed.Local, 1)
	return parsed
}

// TestContainerLogConfigMounts 验证容器采集配置生成的挂载下发契约：
// 1. 原始 paths 不被改写；
// 2. 源路径与软链目标对应的两个挂载都会下发；
// 3. host_path / container_path 为空的挂载会被过滤；
// 4. 无有效挂载时保持原行为（不下发 mounts）。
func TestContainerLogConfigMounts(t *testing.T) {
	origHostPath := config.HostPath
	config.HostPath = "/host"
	defer func() { config.HostPath = origHostPath }()

	paths := []string{"/data/apphome/logs/*.log"}

	t.Run("原始 paths 不被改写 & 源+软链目标挂载全量下发", func(t *testing.T) {
		mounts := []Mount{
			// 源采集路径所在卷
			{HostPath: "/data/pvc-src", ContainerPath: "/data/apphome"},
			// 软链跨卷指向的目标卷
			{HostPath: "/data/pvc-dst", ContainerPath: "/data/real"},
		}
		cfg := newContainerLogConfig(mounts, paths)
		parsed := parseContainerConfig(t, cfg.Config())

		// 原始 paths 原样保留
		assert.Equal(t, paths, parsed.Local[0].Path)

		// 两个挂载都下发，host_path 经 ToHostPath 前缀化
		assert.Equal(t, []Mount{
			{HostPath: "/host/data/pvc-src", ContainerPath: "/data/apphome"},
			{HostPath: "/host/data/pvc-dst", ContainerPath: "/data/real"},
		}, parsed.Local[0].Mounts)
	})

	t.Run("空 host_path/container_path 挂载被过滤", func(t *testing.T) {
		mounts := []Mount{
			{HostPath: "/data/pvc-src", ContainerPath: "/data/apphome"},
			// tmpfs：Source(HostPath) 为空，ToHostPath("") 会变成 host 根，必须过滤
			{HostPath: "", ContainerPath: "/dev/shm"},
			// container_path 为空同样过滤
			{HostPath: "/data/pvc-x", ContainerPath: ""},
		}
		cfg := newContainerLogConfig(mounts, paths)
		parsed := parseContainerConfig(t, cfg.Config())

		mountsOut := parsed.Local[0].Mounts
		assert.Equal(t, []Mount{
			{HostPath: "/host/data/pvc-src", ContainerPath: "/data/apphome"},
		}, mountsOut)
		// 确保不会出现 host 根路径的危险挂载
		for _, m := range mountsOut {
			assert.NotEqual(t, "/host", m.HostPath)
			assert.NotEmpty(t, m.HostPath)
			assert.NotEmpty(t, m.ContainerPath)
		}
	})

	t.Run("无挂载保持原行为", func(t *testing.T) {
		cfg := newContainerLogConfig(nil, paths)
		parsed := parseContainerConfig(t, cfg.Config())
		assert.Empty(t, parsed.Local[0].Mounts)
		assert.Equal(t, paths, parsed.Local[0].Path)
	})

	t.Run("挂载全为空时不下发 mounts", func(t *testing.T) {
		mounts := []Mount{
			{HostPath: "", ContainerPath: "/dev/shm"},
			{HostPath: "/data/pvc-x", ContainerPath: ""},
		}
		cfg := newContainerLogConfig(mounts, paths)
		parsed := parseContainerConfig(t, cfg.Config())
		assert.Empty(t, parsed.Local[0].Mounts)
	})
}
