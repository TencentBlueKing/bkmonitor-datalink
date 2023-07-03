// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"fmt"
	"strconv"
	"sync"

	"github.com/jmespath/go-jmespath"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/datasource"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/monitor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/processor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/utils"
)

type Receiver struct {
	DataSource *define.DataSource
	Plugin     *define.HttpPushPlugin
	Backend    processor.IBackend

	unmarshalFn        utils.UnmarshalFn
	compiledEventsPath *jmespath.JMESPath
}

func (r *Receiver) Push(payload define.Payload) error {
	if r.Backend == nil {
		backend, err := processor.NewBackend(r.DataSource)
		if err != nil {
			return fmt.Errorf("receiver(%d) get backend error: %+v", r.DataSource.DataID, err)
		}
		r.Backend = backend
	}
	payload.PluginID = r.Plugin.PluginID
	payload.DataID = r.DataSource.DataID
	return r.Backend.Send(payload)
}

func (r *Receiver) CheckAuth(token string) bool {
	return r.DataSource.Token == token
}

func (r *Receiver) Init() {
	if r.Plugin.EventsPath != "" {
		r.compiledEventsPath = jmespath.MustCompile(r.Plugin.EventsPath)
	}
	r.unmarshalFn = utils.GetUnmarshalFn(r.Plugin.SourceFormat)
}

func (r *Receiver) ConvertEvents(v interface{}) ([]define.Event, error) {
	// 获取事件数据
	var eventValue interface{}
	var err error
	if r.Plugin.EventsPath == "" {
		eventValue = v
	} else {
		eventValue, err = r.compiledEventsPath.Search(v)
		if err != nil {
			return nil, fmt.Errorf("fetch events by events_path error: %+v", err)
		}
	}

	var events []define.Event
	if r.Plugin.MultipleEvents {
		eventList, ok := eventValue.([]interface{})
		if !ok {
			return nil, fmt.Errorf("eventsPath(%s) is not type of `[]Event`", r.Plugin.EventsPath)
		}
		for _, rawEvent := range eventList {
			event, ok := rawEvent.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("event(%+v) is not type of `Event`", rawEvent)
			}
			events = append(events, event)
		}
	} else {
		event, ok := eventValue.(map[string]interface{})
		events = []define.Event{event}
		if !ok {
			return nil, fmt.Errorf("eventsPath(%s) is not type of `Event`", r.Plugin.EventsPath)
		}
	}

	return events, nil
}

func (r *Receiver) UnmarshalEvents(rawData []byte) (interface{}, error) {
	// 对原始数据进行反序列化
	var data interface{}
	err := r.unmarshalFn(rawData, &data)
	if err != nil {
		return nil, fmt.Errorf("unmarshal raw data error: %+v", err)
	}
	return data, nil
}

func (r *Receiver) Close() {
	r.Backend.Close()
}

func (r *Receiver) UpdateMetric(isSuccess bool, count int) {
	labelValue := monitor.SuccessLabelValue

	if !isSuccess {
		labelValue = monitor.FailedLabelValue
	}

	monitor.EventCounter.WithLabelValues(labelValue, r.Plugin.PluginID,
		strconv.Itoa(r.DataSource.DataID)).Add(float64(count))
}

var (
	receiverRegistry      = make(map[string]*Receiver)
	receiverRegistryMutex = sync.RWMutex{}
)

func GetReceiverID(plugin *define.Plugin, dataID int) string {
	receiverID := fmt.Sprintf("%s_%d", plugin.PluginID, dataID)
	if plugin.IsGlobalPlugin() {
		// 当业务ID为0的时候，表示全局，仅支持plugin_id即可
		receiverID = plugin.PluginID
	}
	return receiverID
}

func GetReceiver(receiverID string) *Receiver {
	receiverRegistryMutex.RLock()
	defer receiverRegistryMutex.RUnlock()
	receiver, ok := receiverRegistry[receiverID]
	if !ok {
		return nil
	}
	return receiver
}

func RegisterReceiver(d *define.DataSource) {
	logger := logging.GetLogger()

	plugin, err := define.NewHttpPushPlugin(d.Option)
	if err != nil {
		logger.Errorf("Receiver(%d) register failed: %+v", d.DataID, err)
		return
	}

	if plugin.GetRunMode() != define.PluginRunModePush {
		return
	}

	backend, err := processor.NewBackend(d)
	if err != nil {
		logger.Errorf("Receiver(%d) register failed: %+v", d.DataID, err)
		return
	}

	receiver := &Receiver{
		DataSource: d,
		Plugin:     plugin,
		Backend:    backend,
	}
	receiver.Init()

	receiverRegistryMutex.Lock()
	defer receiverRegistryMutex.Unlock()
	receiverID := GetReceiverID(&plugin.Plugin, d.DataID)
	receiverRegistry[receiverID] = receiver
	logger.Infof("Receiver(%d) register success, plugin_id: %s", d.DataID, plugin.PluginID)
}

func UnregisterReceiver(d *define.DataSource) {
	logger := logging.GetLogger()
	plugin := d.MustGetPluginOption()
	receiverID := GetReceiverID(plugin, d.DataID)
	receiver := GetReceiver(receiverID)
	if receiver == nil {
		logger.Errorf("Receiver(%d) does not registered", d.DataID)
		return
	}
	receiver.Close()
	receiverRegistryMutex.Lock()
	defer receiverRegistryMutex.Unlock()
	delete(receiverRegistry, receiverID)
	logger.Infof("Receiver(%d) unregister success, plugin_id: %s", d.DataID, plugin.PluginID)
}

func ListDataSources() []define.DataSource {
	var dataSources []define.DataSource
	for _, receiver := range receiverRegistry {
		dataSources = append(dataSources, *receiver.DataSource)
	}
	return dataSources
}

var Subscriber = datasource.Subscriber{
	RegisterFn:      RegisterReceiver,
	UnregisterFn:    UnregisterReceiver,
	ListDataSources: ListDataSources,
	PluginRunMode:   define.PluginRunModePush,
}
