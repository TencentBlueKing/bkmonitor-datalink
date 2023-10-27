// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consul

import (
	consul "github.com/hashicorp/consul/api"
	"github.com/hashicorp/consul/api/watch"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/logging"
)

type IndexItem struct {
	ModifyIndex uint64
	DataSource  *define.DataSource
}

// Watcher consul的监听
type Watcher struct {
	plan         *watch.Plan
	eventChan    chan define.WatchEvent
	clientConfig *consul.Config
	indexCache   map[string]IndexItem
}

func (w *Watcher) Start() {
	err := w.plan.RunWithConfig(w.clientConfig.Address, w.clientConfig)
	if err != nil {
		panic(err)
	}
}

func (w *Watcher) Stop() {
	if !w.plan.IsStopped() {
		w.plan.Stop()
	}
}

// Events
func (w *Watcher) Events() <-chan define.WatchEvent {
	return w.eventChan
}

func NewConsulWatcher(prefix string) *Watcher {
	logger := logging.GetLogger()

	planWatcher := &Watcher{
		eventChan:    make(chan define.WatchEvent, config.Configuration.Consul.EventBufferSize),
		indexCache:   make(map[string]IndexItem),
		clientConfig: NewConfig(),
	}

	watchConfig := make(map[string]interface{})

	watchConfig["type"] = "keyprefix"
	watchConfig["prefix"] = prefix

	watchPlan, err := watch.Parse(watchConfig)
	if err != nil {
		panic(err)
	}

	watchPlan.Handler = func(lastIndex uint64, result interface{}) {
		logger.Infof("Watcher(datasource) triggered, last index: %d", lastIndex)
		kvPairs := result.(consul.KVPairs)
		newKeyIndexCache := make(map[string]IndexItem)
		for _, kvPair := range kvPairs {
			datasourceKVPair, err := ParseDataSourceFromShadowKVPair(kvPair)
			if err != nil {
				logger.Errorf("datasource parse error: %+v, origin data: %s", err, kvPair.Value)
				continue
			}
			kvPair = datasourceKVPair.Pair
			var eventType define.WatchEventType
			oldItem, ok := planWatcher.indexCache[kvPair.Key]

			if !ok {
				eventType = define.WatchEventAdded
				logger.Debugf("consul key->(%s) is added, modify index->(%d), data->(%s)",
					kvPair.Key, kvPair.ModifyIndex, kvPair.Value)
			} else if oldItem.ModifyIndex != kvPair.ModifyIndex {
				eventType = define.WatchEventModified
				logger.Debugf("consul key->(%s) is modified, modify index->(%d), data->(%s)",
					kvPair.Key, kvPair.ModifyIndex, kvPair.Value)
			} else {
				newKeyIndexCache[kvPair.Key] = oldItem
				continue
			}

			newKeyIndexCache[kvPair.Key] = IndexItem{
				ModifyIndex: kvPair.ModifyIndex,
				DataSource:  datasourceKVPair.DataSource,
			}
			planWatcher.eventChan <- define.WatchEvent{
				Type:       eventType,
				DataSource: datasourceKVPair.DataSource,
			}
		}
		// 删除的key
		for key, item := range planWatcher.indexCache {
			if _, ok := newKeyIndexCache[key]; !ok {
				logger.Debugf("consul key->(%s) is deleted", key)
				planWatcher.eventChan <- define.WatchEvent{
					Type:       define.WatchEventDeleted,
					DataSource: item.DataSource,
				}
			}
		}

		planWatcher.indexCache = newKeyIndexCache
	}
	planWatcher.plan = watchPlan

	return planWatcher
}
