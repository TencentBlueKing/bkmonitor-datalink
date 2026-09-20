// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build linux

package define

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestGetContainerMount 校验 linux 下 GetContainerMount 的前缀匹配语义：
// 仅返回 container_path 为采集路径前缀的挂载（host_path -> container_path）。
// 原 TestContainerActualPath 引用的 ContainerActualPath 已重构为 GetContainerMount，
// 此处同步更新，避免 define 测试包编译失败。
func TestGetContainerMount(t *testing.T) {
	container := &Container{
		RootPath: "/var/host",
		Mounts: []Mount{
			{HostPath: "/host/data", ContainerPath: "/data"},
			{HostPath: "/host/logs", ContainerPath: "/data/logs"},
			{HostPath: "/host/other", ContainerPath: "/other"},
		},
	}

	// 命中单个挂载
	m, err := GetContainerMount("/data/a.log", container)
	assert.NoError(t, err)
	assert.Equal(t, map[string]string{"/host/data": "/data"}, m)

	// 父子挂载都命中（/data 与 /data/logs 均为前缀）
	m, err = GetContainerMount("/data/logs/x.log", container)
	assert.NoError(t, err)
	assert.Equal(t, map[string]string{
		"/host/data": "/data",
		"/host/logs": "/data/logs",
	}, m)

	// 未命中任何挂载
	m, err = GetContainerMount("/nomatch/x.log", container)
	assert.NoError(t, err)
	assert.Empty(t, m)

	// 相对路径报错
	_, err = GetContainerMount("relative/x.log", container)
	assert.Error(t, err)
}
