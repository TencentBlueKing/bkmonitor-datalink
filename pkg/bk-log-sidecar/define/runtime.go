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
	"context"
)

type ContainerEventType string

const (
	ContainerEventCreate ContainerEventType = "create"
	ContainerEventStop   ContainerEventType = "stop"
	ContainerEventDelete ContainerEventType = "delete"
)

type RuntimeType string

const (
	RuntimeTypeContainerd RuntimeType = "containerd"
	RuntimeTypeDocker     RuntimeType = "docker"
	RuntimeTypeEks        RuntimeType = "eks"
)

// SimpleContainer 容器简要信息
type SimpleContainer struct {
	// ID 容器ID
	ID string
}

// Container 容器详细信息
type Container struct {
	// ID 容器ID
	ID string

	// Labels 容器标签
	Labels map[string]string

	// Image 镜像名称
	Image string

	// LogPath 标准输出日志路径
	LogPath string

	// RootPath 根目录
	RootPath string

	// Mounts 挂载配置
	Mounts []Mount
}

// Mount 挂载配置
type Mount struct {
	HostPath      string `yaml:"host_path"`
	ContainerPath string `yaml:"container_path"`
}

// ContainerEvent 容器监听事件
type ContainerEvent struct {
	ContainerID string
	Type        ContainerEventType
}

// Runtime 运行时接口
type Runtime interface {
	// Containers 获取容器列表
	Containers(ctx context.Context) ([]SimpleContainer, error)
	// Inspect 获取容器详情
	Inspect(ctx context.Context, containerID string) (Container, error)
	// Subscribe 订阅容器变更事件
	Subscribe(ctx context.Context) (ch <-chan *ContainerEvent, errs <-chan error)
	// Type 获取 runtime 类型
	Type() RuntimeType
}
