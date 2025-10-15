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
	"github.com/containerd/containerd"
	apievents "github.com/containerd/containerd/api/events"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/runtime"
	"github.com/containerd/typeurl"
	"github.com/go-logr/logr"
	"k8s.io/cri-api/pkg/apis/runtime/v1alpha2"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/define"
)

const (
	ContainerdTaskDirName   = "io.containerd.runtime.v2.task"
	ContainerdRootFsDirName = "rootfs"
)

// ContainerdRuntime container runtime
type ContainerdRuntime struct {
	containerdClient *containerd.Client
	criClient        v1alpha2.RuntimeServiceClient
	log              logr.Logger
}

// Type runtime type
func (r *ContainerdRuntime) Type() define.RuntimeType {
	return define.RuntimeTypeContainerd
}

// Containers list of containers
func (r *ContainerdRuntime) Containers(ctx context.Context) ([]define.SimpleContainer, error) {
	containers, err := r.criClient.ListContainers(ctx, &v1alpha2.ListContainersRequest{
		Filter: &v1alpha2.ContainerFilter{
			State: &v1alpha2.ContainerStateValue{
				State: v1alpha2.ContainerState_CONTAINER_RUNNING,
			},
		},
	})
	if err != nil {
		return nil, err
	}
	var result []define.SimpleContainer
	for _, container := range containers.GetContainers() {
		if container == nil {
			continue
		}
		result = append(result, define.SimpleContainer{
			ID: container.Id,
		})
	}
	return result, nil
}

// Inspect check container status and mount info
func (r *ContainerdRuntime) Inspect(ctx context.Context, containerID string) (define.Container, error) {
	containerStatus, err := r.criClient.ContainerStatus(ctx, &v1alpha2.ContainerStatusRequest{
		ContainerId: containerID,
		Verbose:     true,
	})
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

	rootPath, logPath, err := resolveContainerdPath(containerStatus, containerInfo.Pid)
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

func (r *ContainerdRuntime) parseEvent(event *events.Envelope) (*define.ContainerEvent, bool) {
	var eventType define.ContainerEventType
	var eventDecoded interface {
		Field([]string) (string, bool)
	}

	switch event.Topic {
	case runtime.TaskStartEventTopic:
		eventType = define.ContainerEventCreate
		eventDecoded = &apievents.TaskStart{}
	case runtime.TaskResumedEventTopic:
		eventType = define.ContainerEventCreate
		eventDecoded = &apievents.TaskResumed{}
	case runtime.TaskPausedEventTopic:
		eventType = define.ContainerEventStop
		eventDecoded = &apievents.TaskPaused{}
	case runtime.TaskDeleteEventTopic:
		eventType = define.ContainerEventDelete
		eventDecoded = &apievents.TaskDelete{}
	default:
		return nil, false
	}

	err := typeurl.UnmarshalTo(event.Event, eventDecoded)
	if err != nil {
		r.log.Error(err, "parse containerd event error")
		return nil, false
	}

	containerID, ok := eventDecoded.Field([]string{"container_id"})
	if !ok {
		return nil, false
	}

	return &define.ContainerEvent{
		ContainerID: containerID,
		Type:        eventType,
	}, true
}

// Subscribe watch container event
func (r *ContainerdRuntime) Subscribe(ctx context.Context) (<-chan *define.ContainerEvent, <-chan error) {
	eventChanel := make(chan *define.ContainerEvent)
	errorChanel := make(chan error)

	// 只过滤这四种事件
	filterOpts := []string{
		fmt.Sprintf("topic==\"%s\"", runtime.TaskStartEventTopic),
		fmt.Sprintf("topic==\"%s\"", runtime.TaskResumedEventTopic),
		fmt.Sprintf("topic==\"%s\"", runtime.TaskPausedEventTopic),
		fmt.Sprintf("topic==\"%s\"", runtime.TaskDeleteEventTopic),
	}

	events, errors := r.containerdClient.Subscribe(ctx, filterOpts...)

	r.log.Info("start watch containerd event")

	go func() {
		for {
			select {
			case event := <-events:
				containerEvent, ok := r.parseEvent(event)
				if !ok {
					continue
				}
				eventChanel <- containerEvent
				r.log.Info(fmt.Sprintf("event received: %v", containerEvent))
			case err := <-errors:
				errorChanel <- err
				r.log.Error(err, "event receive error")
			case <-ctx.Done():
				return
			}
		}
	}()

	return eventChanel, errorChanel
}
