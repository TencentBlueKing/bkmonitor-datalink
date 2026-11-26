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
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/define"
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
	containers, err := r.cli.ContainerList(ctx, container.ListOptions{Filters: filters.NewArgs()})

	if err != nil {
		return nil, dockerAPIError(err)
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
	type inspectResult struct {
		container container.InspectResponse
		err       error
	}
	containerCh := make(chan inspectResult, 1)
	ctx, cancelFunc := context.WithTimeout(ctx, 3*time.Second)
	defer cancelFunc()

	cli := r.cli
	go func() {
		c, err := cli.ContainerInspect(ctx, containerID)
		containerCh <- inspectResult{container: c, err: err}
	}()

	select {
	case result := <-containerCh:
		if result.err != nil {
			return define.Container{}, normalizeDockerInspectError(containerID, dockerAPIError(result.err))
		}
		c := result.container
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
		return define.Container{}, fmt.Errorf("docker inspect container info [%s] timed out: %w", containerID, ctx.Err())
	}
}

func normalizeDockerInspectError(containerID string, err error) error {
	var notFound errdefs.ErrNotFound
	if errors.As(err, &notFound) {
		// Keep Docker error types inside the Docker adapter so adding another
		// Runtime never requires changing the common sidecar reconciliation code.
		return fmt.Errorf("%w: container [%s]: %v", define.ErrContainerNotFound, containerID, err)
	}
	return fmt.Errorf("docker inspect container info [%s] failed: %w", containerID, err)
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
func (r *DockerRuntime) Subscribe(ctx context.Context) (<-chan *define.ContainerEvent, <-chan error, error) {
	eventChanel := make(chan *define.ContainerEvent)
	errorChanel := make(chan error)

	filter := filters.NewArgs()
	filter.Add("type", DockerContainerType)
	filter.Add("event", DockerStopEvent)
	filter.Add("event", DockerStartEvent)
	filter.Add("event", DockerDieEvent)
	options := events.ListOptions{Filters: filter}

	cli := r.cli
	// Docker SDK 的 Events 只通过异步 errors channel 报启动失败。先执行
	// 同步 Ping，把 socket/daemon 不可用转换为 Subscribe 的同步 error。
	if _, err := cli.Ping(ctx); err != nil {
		return nil, nil, fmt.Errorf("ping docker before event subscription: %w", err)
	}
	events, errors := cli.Events(ctx, options)
	select {
	case err, ok := <-errors:
		if !ok {
			return nil, nil, fmt.Errorf("docker subscription closed during startup")
		}
		return nil, nil, fmt.Errorf("start docker event subscription: %w", dockerAPIError(err))
	default:
	}

	r.log.Info("start watch docker event")

	go func() {
		defer close(eventChanel)
		defer close(errorChanel)
		for {
			select {
			case event, ok := <-events:
				if !ok {
					return
				}
				containerEvent, ok := r.parseEvent(&event)
				if !ok {
					continue
				}
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
				err = dockerAPIError(err)
				r.log.Error(err, "event receive error")
				select {
				case errorChanel <- err:
				case <-ctx.Done():
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	return eventChanel, errorChanel, nil
}

// The daemon reports version mismatches on versioned requests, even after a successful ping.
func dockerAPIError(err error) error {
	if err != nil && (strings.Contains(err.Error(), "Minimum supported API version is") ||
		strings.Contains(err.Error(), "Maximum supported API version is")) {
		return fmt.Errorf("Docker API version incompatible: %w", err)
	}
	return err
}

// NewDockerRuntime new docker runtime
func NewDockerRuntime(ctx context.Context) (define.Runtime, error) {
	endpoint := client.FromEnv
	if config.DockerSocket != "" {
		endpoint = client.WithHost(config.DockerSocket)
	}
	cli, err := client.NewClientWithOpts(endpoint, client.WithVersion(config.DockerApiVersion))
	if err != nil {
		return nil, fmt.Errorf("create docker client: %w", err)
	}
	if config.DockerApiVersion == "" {
		ctx, cancel := context.WithTimeout(ctx, RuntimeOperationTimeout)
		defer cancel()
		ping, err := cli.Ping(ctx)
		if err != nil {
			cli.Close()
			return nil, fmt.Errorf("ping docker for API version negotiation: %w", err)
		}
		// Events reads the client version before sending its request. Finish
		// negotiation before publishing the client to concurrent callers.
		cli.NegotiateAPIVersionPing(ping)
	}
	return &DockerRuntime{
		log: ctrl.Log.WithName("docker"),
		cli: cli,
	}, nil
}
