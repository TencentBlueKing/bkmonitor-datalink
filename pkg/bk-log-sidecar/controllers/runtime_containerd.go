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
	"encoding/json"
	"fmt"

	v1 "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/define"
)

// criClient wraps v1.RuntimeServiceClient for container listing and inspection.
// The actual CRI version (v1 vs v1alpha2) is determined by the gRPC connection's
// interceptor, not by this struct — the protobuf wire format is identical.
type criClient struct {
	client v1.RuntimeServiceClient
}

func (c *criClient) ListContainers(ctx context.Context) ([]define.SimpleContainer, error) {
	resp, err := c.client.ListContainers(ctx, &v1.ListContainersRequest{
		Filter: &v1.ContainerFilter{
			State: &v1.ContainerStateValue{
				State: v1.ContainerState_CONTAINER_RUNNING,
			},
		},
	})
	if err != nil {
		return nil, err
	}
	var result []define.SimpleContainer
	for _, container := range resp.GetContainers() {
		if container == nil {
			continue
		}
		result = append(result, define.SimpleContainer{ID: container.Id})
	}
	return result, nil
}

func (c *criClient) ContainerStatus(ctx context.Context, containerID string) (*v1.ContainerStatusResponse, error) {
	return c.client.ContainerStatus(ctx, &v1.ContainerStatusRequest{
		ContainerId: containerID,
		Verbose:     true,
	})
}

// ContainerdRuntime implements define.Runtime for all containerd versions.
// CRI v1 vs v1alpha2 is handled at the gRPC connection level via interceptor.
type ContainerdRuntime struct {
	ContainerdBase
	cri *criClient
}

func (r *ContainerdRuntime) Containers(ctx context.Context) ([]define.SimpleContainer, error) {
	return r.cri.ListContainers(ctx)
}

func (r *ContainerdRuntime) Inspect(ctx context.Context, containerID string) (define.Container, error) {
	containerStatus, err := r.cri.ContainerStatus(ctx, containerID)
	if err != nil {
		return define.Container{}, err
	}

	var mounts []define.Mount
	for _, mount := range containerStatus.Status.Mounts {
		mounts = append(mounts, define.Mount{
			HostPath:      mount.HostPath,
			ContainerPath: mount.ContainerPath,
		})
	}

	// 方案一：优先用 PID 拼接容器文件系统的根路径
	// 方案二：如果 PID 不存在，则使用 containerd 的 merged 路径
	// 但是方案二存在一个问题，如果容器是在 sidecar 之后创建的，这个路径从容器内拿到的是空 (尽管宿主机上该目录确实存在)，原因待查
	var containerInfo struct {
		Pid int `json:"pid"`
	}
	err = json.Unmarshal([]byte(containerStatus.Info["info"]), &containerInfo)
	if err != nil {
		r.log.Info(fmt.Sprintf("container [%s] info unmarshal error: %s", containerID, containerStatus.Info["info"]))
	}
	rootPath, logPath, err := resolveContainerdV2Path(containerStatus, containerInfo.Pid)
	if err != nil {
		r.log.Error(err, fmt.Sprintf("container [%s] failed to eval symlink for log path [%s]", containerID, logPath))
	}

	// 获取不到镜像名称时使用 Image ID
	image := containerStatus.Status.ImageRef
	if containerStatus.Status.Image != nil {
		image = containerStatus.Status.Image.Image
	}
	return define.Container{
		ID:       containerStatus.Status.Id,
		Labels:   containerStatus.Status.Labels,
		Image:    image,
		LogPath:  logPath,
		RootPath: rootPath,
		Mounts:   mounts,
	}, nil
}
