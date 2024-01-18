// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package redis

import (
	"context"
	"sync"

	storeRedis "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type Watcher struct {
	instance *storeRedis.Instance
	wg       *sync.WaitGroup
}

var watcher *Watcher

// NewWatcher new a subscription service
func NewWatcher(ctx context.Context, wg *sync.WaitGroup) *Watcher {
	if watcher != nil {
		return watcher
	}
	if wg == nil {
		wg = new(sync.WaitGroup)
	}
	instance := storeRedis.GetInstance()
	watcher = &Watcher{
		instance: instance,
		wg:       wg,
	}
	return watcher
}

// Watch sub a channel, watch periodic task
func (s *Watcher) Watch(ctx context.Context, receiveChan chan<- string) {
	ch := s.instance.Subscribe(storeRedis.StoragePeriodicTaskChannelKey)
	for {
		select {
		case <-ctx.Done():
			logger.Warnf("subscription service exist")
			return
		// get payload from redis
		case msg := <-ch:
			// TODO Process message and returned to the implements
			receiveChan <- msg.Payload
			logger.Infof("subscribe msg: %s", msg.String())
			// refresh payload to mem
		}
	}
}
