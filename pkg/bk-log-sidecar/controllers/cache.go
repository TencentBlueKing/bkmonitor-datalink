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
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/define"
)

func castContainer(c interface{}) *define.Container {
	return c.(*define.Container)
}

func (s *BkLogSidecar) periodCacheContainer() {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := s.cacheContainer(); err != nil {
				s.log.Error(err, "periodic container cache refresh failed")
			}
		case <-s.stopCh:
			s.log.Info("stop periodCacheContainer")
			return
		}
	}
}

func (s *BkLogSidecar) cacheContainer() error {
	s.log.Info("cache container info start")
	ctx := context.Background()
	runtime, err := s.getRuntime()
	if err != nil {
		return fmt.Errorf("initialize runtime: %w", err)
	}
	containers, err := runtime.Containers(ctx)
	if err != nil {
		return fmt.Errorf("list containers: %w", err)
	}

	for _, container := range containers {
		containerInfo, err := s.containerByID(container.ID)
		if err != nil {
			return err
		}
		if containerInfo == nil {
			continue
		}
		s.containerCache.Store(container.ID, containerInfo)
	}
	s.log.Info("cache container info end")
	return nil
}

func (s *BkLogSidecar) getContainerInfoByID(containerID string) (*define.Container, error) {
	containerInfo, ok := s.containerCache.Load(containerID)
	if ok {
		return castContainer(containerInfo), nil
	}

	container, err := s.containerByID(containerID)
	if err != nil {
		return nil, err
	}
	if container != nil {
		s.containerCache.Store(containerID, container)
	}
	return container, nil
}

func (s *BkLogSidecar) containerByID(containerID string) (*define.Container, error) {
	ctx := context.Background()
	runtime, err := s.getRuntime()
	if err != nil {
		return nil, fmt.Errorf("initialize runtime: %w", err)
	}
	container, err := runtime.Inspect(ctx, containerID)
	if err != nil {
		// Containers may disappear between List and Inspect. That race has
		// already reached its desired state, so retrying the whole snapshot for
		// a confirmed NotFound would only create unnecessary queue pressure.
		if errors.Is(err, define.ErrContainerNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("inspect container %s: %w", containerID, err)
	}
	return &container, nil
}
