// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package datasource

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/utils"
)

type Subscriber struct {
	RegisterFn      func(d *define.DataSource)
	UnregisterFn    func(d *define.DataSource)
	ListDataSources func() []define.DataSource
	PluginRunMode   define.PluginRunMode
}

var (
	watcher     *consul.Watcher
	stopWatcher chan struct{}
	subscribers = make(map[string]Subscriber)
)

func RegisterDataSourceSubscriber(name string, s Subscriber) {
	subscribers[name] = s
}

// StartWatchDataSource 监听 Consul 变更
func StartWatchDataSource() {
	logger := logging.GetLogger()

	if watcher != nil {
		return
	}

	watchPrefix := utils.ResolveUnixPaths(config.Configuration.Consul.ServicePath, "data_id", define.ServiceID) + "/"
	watcher = consul.NewConsulWatcher(watchPrefix)

	go watcher.Start()

	logger.Infof("Watcher(datasource) started, prefix: %s", watchPrefix)

	stopWatcher = make(chan struct{})

	for {
		select {
		case event := <-watcher.Events():
			logger.Debugf("Watcher(datasource) new event received: %+v", event)
			for _, subscriber := range subscribers {
				s := subscriber
				if s.PluginRunMode != event.DataSource.MustGetPluginOption().GetRunMode() {
					continue
				}
				// 对订阅者逐一发布消息
				switch event.Type {
				case define.WatchEventAdded:
					go s.RegisterFn(event.DataSource)
				case define.WatchEventModified:
					go func() {
						s.UnregisterFn(event.DataSource)
						s.RegisterFn(event.DataSource)
					}()
				case define.WatchEventDeleted:
					go s.UnregisterFn(event.DataSource)
				}
			}
		case <-stopWatcher:
			return
		}
	}
}

// StopWatchDataSource 停止监听
func StopWatchDataSource() {
	logger := logging.GetLogger()

	if watcher != nil {
		watcher.Stop()
	}
	close(stopWatcher)

	logger.Infof("Watcher(datasource) stopped")
}

// ListAllSubscribers 获取当前注册的监听进程
func ListAllSubscribers() map[string]Subscriber {
	return subscribers
}
