// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package periodic

import (
	"context"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"sync"

	redisWatcher "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/watcher/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type WatchServiceOptions struct {
	watchChanSize int
}

type WatchService struct {
	config WatchServiceOptions

	ctx context.Context

	redisWatch *redisWatcher.Watcher
	watchChan  chan string
}

func (t *WatchService) StartWatch() {
	logger.Infof("TaskScheduler start watch.")
	t.redisWatch.Watch(t.ctx, t.watchChan)
}

func initConfig() WatchServiceOptions {
	return WatchServiceOptions{watchChanSize: config.TaskWatchChanSize}
}

func NewWatchService(ctx context.Context) *WatchService {
	rw := redisWatcher.NewWatcher(ctx, new(sync.WaitGroup))
	options := initConfig()
	w := &WatchService{ctx: ctx, config: options, redisWatch: rw, watchChan: make(chan string, options.watchChanSize)}
	return w
}
