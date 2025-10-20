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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/utils"
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
			s.cacheContainer()
		case <-s.stopCh:
			s.log.Info("stop periodCacheContainer")
			return
		}
	}
}

func (s *BkLogSidecar) cacheContainer() {
	s.log.Info("cache container info start")
	ctx := context.Background()
	containers, err := s.getRuntime().Containers(ctx)
	if utils.NotNil(err) {
		s.log.Error(err, "list container failed")
		return
	}

	for _, container := range containers {
		containerInfo := s.containerByID(container.ID)
		if containerInfo == nil {
			continue
		}
		s.containerCache.Store(container.ID, containerInfo)
	}
	s.log.Info("cache container info end")
}

func (s *BkLogSidecar) getContainerInfoByID(containerID string) *define.Container {
	containerInfo, ok := s.containerCache.Load(containerID)
	if ok {
		return castContainer(containerInfo)
	} else {
		container := s.containerByID(containerID)
		if container != nil {
			s.containerCache.Store(containerID, container)
			return container
		}
		return nil
	}
}

func (s *BkLogSidecar) containerByID(containerID string) *define.Container {
	ctx := context.Background()
	container, err := s.getRuntime().Inspect(ctx, containerID)
	if err != nil {
		s.log.Info(fmt.Sprintf("get container by id [%s] error: %s", containerID, err))
		return nil
	}
	return &container
}
