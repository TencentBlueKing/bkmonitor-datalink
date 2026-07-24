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
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/define"
	"github.com/containerd/containerd"
	apievents "github.com/containerd/containerd/api/events"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/runtime"
	"github.com/containerd/typeurl/v2"
	"github.com/go-logr/logr"
)

const (
	ContainerdTaskDirName   = "io.containerd.runtime.v2.task"
	ContainerdRootFsDirName = "rootfs"
)

// ContainerdBase containerd base struct
type ContainerdBase struct {
	containerdClient *containerd.Client
	log              logr.Logger
}

// Type runtime type
func (r *ContainerdBase) Type() define.RuntimeType {
	return define.RuntimeTypeContainerd
}

// Subscribe watch container event
func (r *ContainerdBase) Subscribe(ctx context.Context) (<-chan *define.ContainerEvent, <-chan error, error) {
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
	// containerd 在 gRPC Subscribe 建立失败时，会在返回前把错误写入
	// errors channel。这里同步提取该错误，避免公共层误判订阅已经 ready。
	select {
	case err, ok := <-errors:
		if !ok {
			return nil, nil, fmt.Errorf("containerd subscription closed during startup")
		}
		return nil, nil, fmt.Errorf("start containerd event subscription: %w", err)
	default:
	}

	r.log.Info("start watch containerd event")

	go func() {
		defer close(eventChanel)
		defer close(errorChanel)
		for {
			select {
			case event, ok := <-events:
				if !ok {
					return
				}
				containerEvent, ok := r.parseEvent(event)
				if !ok {
					continue
				}
				// supervisor 重连会取消旧 context；发送也必须可取消，否则旧订阅
				// 可能永久阻塞在无人接收的 channel 上。
				select {
				case eventChanel <- containerEvent:
				case <-ctx.Done():
					return
				}
				r.log.Info(fmt.Sprintf("event received: %v", containerEvent))
			case err, ok := <-errors:
				if !ok {
					return
				}
				select {
				case errorChanel <- err:
				case <-ctx.Done():
					return
				}
				r.log.Error(err, "event receive error")
			case <-ctx.Done():
				return
			}
		}
	}()

	return eventChanel, errorChanel, nil
}

// parseEvent parse containerd event to ContainerEvent
func (r *ContainerdBase) parseEvent(event *events.Envelope) (*define.ContainerEvent, bool) {
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
