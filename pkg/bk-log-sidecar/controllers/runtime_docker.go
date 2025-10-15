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
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/utils"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	DockerContainerType = "container"
	DockerStartEvent    = "start"
	DockerDieEvent      = "die"
	DockerStopEvent     = "stop"
)

// DockerRuntime docker runtime
type DockerRuntime struct {
	cli *client.Client
	log logr.Logger
}

// Type runtime type
func (r *DockerRuntime) Type() define.RuntimeType {
	return define.RuntimeTypeDocker
}

// Containers list of containers
func (r *DockerRuntime) Containers(ctx context.Context) ([]define.SimpleContainer, error) {
	containers, err := r.cli.ContainerList(ctx, types.ContainerListOptions{Filters: filters.NewArgs()})

	if err != nil {
		return nil, err
	}
	var result []define.SimpleContainer
	for _, c := range containers {
		result = append(result, define.SimpleContainer{
			ID: c.ID,
		})
	}
	return result, nil
}

// Inspect docker container inspect
func (r *DockerRuntime) Inspect(ctx context.Context, containerID string) (define.Container, error) {
	containerCh := make(chan types.ContainerJSON)
	ctx, cancelFunc := context.WithTimeout(ctx, 3*time.Second)

	defer func() {
		close(containerCh)
	}()

	go func() {
		c, err := r.cli.ContainerInspect(ctx, containerID)
		if utils.NotNil(err) {
			r.log.Error(err, fmt.Sprintf("docker inspect container info [%s] failed", containerID))
			cancelFunc()
			return
		}
		containerCh <- c
	}()

	select {
	case c := <-containerCh:
		rootPath := define.ContainerRootPath(c)

		var mounts []define.Mount
		for _, mount := range c.Mounts {
			mounts = append(mounts, define.Mount{
				HostPath:      mount.Source,
				ContainerPath: mount.Destination,
			})
		}

		containerInfo := define.Container{
			ID:       c.ID,
			Labels:   c.Config.Labels,
			Image:    c.Image,
			LogPath:  c.LogPath,
			RootPath: rootPath,
			Mounts:   mounts,
		}

		return containerInfo, nil
	case <-ctx.Done():
		return define.Container{}, fmt.Errorf("docker inspect container info [%s] timeout or other error", containerID)
	}
}

func (r *DockerRuntime) parseEvent(event *events.Message) (*define.ContainerEvent, bool) {
	r.log.Info(fmt.Sprintf("receive docker events.Message [%s] for container [%s]", event.Action, event.ID))

	var eventType define.ContainerEventType

	switch event.Action {
	case DockerStartEvent:
		eventType = define.ContainerEventCreate
	case DockerDieEvent:
		eventType = define.ContainerEventDelete
	case DockerStopEvent:
		eventType = define.ContainerEventStop
	default:
		r.log.Info(fmt.Sprintf("not expecting events.Message [%s] for container [%s]", event.Action, event.ID))
		return nil, false
	}

	return &define.ContainerEvent{
		Type:        eventType,
		ContainerID: event.ID,
	}, true
}

// Subscribe watch docker event
func (r *DockerRuntime) Subscribe(ctx context.Context) (<-chan *define.ContainerEvent, <-chan error) {
	eventChanel := make(chan *define.ContainerEvent)
	errorChanel := make(chan error)

	filter := filters.NewArgs()
	filter.Add("type", DockerContainerType)
	filter.Add("event", DockerStopEvent)
	filter.Add("event", DockerStartEvent)
	filter.Add("event", DockerDieEvent)
	options := types.EventsOptions{Filters: filter}

	events, errors := r.cli.Events(ctx, options)

	r.log.Info("start watch docker event")

	go func() {
		for {
			select {
			case event := <-events:
				containerEvent, ok := r.parseEvent(&event)
				if !ok {
					continue
				}
				eventChanel <- containerEvent
				r.log.Info(fmt.Sprintf("event received: %v", containerEvent))
			case err := <-errors:
				r.log.Error(err, "event receive error")
				match, api := utils.ExtractDockerApiVersion(err)
				if match {
					cli, err := client.NewClientWithOpts(client.WithHost(config.DockerSocket), client.WithVersion(api))
					if utils.IsNil(err) {
						r.cli = cli
						events, errors = r.cli.Events(ctx, options)
					}
				}
				errorChanel <- err
			case <-ctx.Done():
				return
			}
		}
	}()

	return eventChanel, errorChanel
}

// NewDockerRuntime new docker runtime
func NewDockerRuntime() define.Runtime {
	var cli *client.Client
	var err error
	if len(config.DockerSocket) > 0 {
		cli, err = client.NewClientWithOpts(client.WithHost(config.DockerSocket), client.WithVersion(config.DockerApiVersion))
	} else {
		cli, err = client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	}
	utils.CheckError(err)
	return &DockerRuntime{
		log: ctrl.Log.WithName("docker"),
		cli: cli,
	}
}
