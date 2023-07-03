// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package poller

import (
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/datasource"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/monitor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/processor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/utils"
)

type Task struct {
	DataSource *define.DataSource
	Poller     Poller
	Plugin     *define.Plugin
	Backend    processor.IBackend
	ticker     *time.Ticker
	done       chan bool
}

func (t *Task) Pull() (define.Payload, error) {
	return t.Poller.Pull()
}

func (t *Task) Push(payload define.Payload) error {
	if t.Backend == nil {
		backend, err := processor.NewBackend(t.DataSource)
		if err != nil {
			return fmt.Errorf("PollerTask(%d) get backend error: %+v", t.DataSource.DataID, err)
		}
		t.Backend = backend
	}
	payload.PluginID = t.Plugin.PluginID
	payload.DataID = t.DataSource.DataID
	return t.Backend.Send(payload)
}

func (t *Task) UpdateEventCountMetric(isSuccess bool, count int) {
	labelValue := monitor.SuccessLabelValue

	if !isSuccess {
		labelValue = monitor.FailedLabelValue
	}

	monitor.EventCounter.WithLabelValues(labelValue, t.Plugin.PluginID,
		strconv.Itoa(t.DataSource.DataID)).Add(float64(count))
}

func (t *Task) UpdatePullCountMetric(isSuccess bool) {
	labelValue := monitor.SuccessLabelValue

	if !isSuccess {
		labelValue = monitor.FailedLabelValue
	}

	monitor.PollerCounter.WithLabelValues(labelValue, t.Plugin.PluginID, strconv.Itoa(t.DataSource.DataID)).Inc()
}

func (t *Task) Once() error {
	payload, err := t.Poller.Pull()
	if err != nil {
		t.UpdateEventCountMetric(false, 1)
		t.UpdatePullCountMetric(false)
		return fmt.Errorf("pull data failed: %+v", err)
	}
	err = t.Push(payload)
	if err != nil {
		t.UpdateEventCountMetric(false, payload.GetEventCount())
		t.UpdatePullCountMetric(false)
		return fmt.Errorf("push data failed: %+v", err)
	}
	t.UpdateEventCountMetric(true, payload.GetEventCount())
	t.UpdatePullCountMetric(true)
	return nil
}

func (t *Task) Start() {
	if t.IsRunning() {
		return
	}
	// 启动 ticker
	t.ticker = time.NewTicker(time.Duration(t.Poller.GetInterval()) * time.Second)
	t.done = make(chan bool)

	go func() {
		logger := logging.GetLogger()

		defer utils.RecoverError(func(e error) {
			logger.Errorf("PollerTask(%d) painc when run: %+v", t.DataSource.DataID, e)
		})

		// 先立即执行一次
		err := t.Once()
		if err != nil {
			logger.Errorf("PollerTask(%d) run failed: %+v", t.DataSource.DataID, err)
		}
		for {
			select {
			case <-t.done:
				logger.Infof("PollerTask(%d) stop signal received", t.DataSource.DataID)
				return
			case timeObj := <-t.ticker.C:
				logger.Debugf("PollerTask(%d) ticked on %d", t.DataSource.DataID, timeObj.Unix())
				err := t.Once()
				if err != nil {
					logger.Errorf("PollerTask(%d) run failed: %+v", t.DataSource.DataID, err)
				}
			}
		}
	}()
}

func (t *Task) Stop() {
	if !t.IsRunning() {
		return
	}

	t.ticker.Stop()
	t.ticker = nil

	close(t.done)
	t.done = nil

	t.Backend.Close()
}

func (t *Task) IsRunning() bool {
	return t.ticker != nil
}

var (
	taskRegistry      = make(map[string]*Task)
	taskRegistryMutex = sync.RWMutex{}
)

func ListRegisteredTask() map[string]*Task {
	return taskRegistry
}

func GetRegisteredTask(taskID string) *Task {
	taskRegistryMutex.RLock()
	defer taskRegistryMutex.RUnlock()
	task, ok := taskRegistry[taskID]
	if !ok {
		return nil
	}
	return task
}

func GetTaskID(plugin *define.Plugin, dataID int) string {
	taskID := fmt.Sprintf("%s_%d", plugin.PluginID, dataID)
	if plugin.IsGlobalPlugin() {
		// 当业务ID为0的时候，表示全局，仅支持plugin_id即可
		taskID = plugin.PluginID
	}
	return taskID
}

// RegisterTask 注册一个拉取任务
func RegisterTask(d *define.DataSource) {
	logger := logging.GetLogger()

	plugin := d.MustGetPluginOption()

	if plugin.GetRunMode() != define.PluginRunModePull {
		return
	}

	poller, err := NewPoller(d)
	if err != nil {
		logger.Errorf("PollerTask(%d) register failed: %+v", d.DataID, err)
		return
	}

	backend, err := processor.NewBackend(d)
	if err != nil {
		logger.Errorf("PollerTask(%d) register failed: %+v", d.DataID, err)
		return
	}

	newTask := &Task{
		DataSource: d,
		Plugin:     plugin,
		Poller:     poller,
		Backend:    backend,
	}

	newTask.Start()

	taskRegistryMutex.Lock()
	defer taskRegistryMutex.Unlock()
	taskRegistry[GetTaskID(plugin, d.DataID)] = newTask
	logger.Infof("PollerTask(%d) register success, plugin_id: %s", d.DataID, plugin.PluginID)
}

// UnregisterTask
func UnregisterTask(d *define.DataSource) {
	logger := logging.GetLogger()
	plugin := d.MustGetPluginOption()
	taskID := GetTaskID(plugin, d.DataID)
	task := GetRegisteredTask(taskID)
	if task == nil {
		logger.Errorf("PollerTask(%d) does not registered", d.DataID)
		return
	}

	task.Stop()

	taskRegistryMutex.Lock()
	defer taskRegistryMutex.Unlock()
	delete(taskRegistry, taskID)

	logger.Infof("PollerTask(%d) unregister success, plugin_id: %s", d.DataID, plugin.PluginID)
}

func ListDataSources() []define.DataSource {
	var dataSources []define.DataSource
	for _, task := range taskRegistry {
		dataSources = append(dataSources, *task.DataSource)
	}
	return dataSources
}

var Subscriber = datasource.Subscriber{
	RegisterFn:      RegisterTask,
	UnregisterFn:    UnregisterTask,
	ListDataSources: ListDataSources,
	PluginRunMode:   define.PluginRunModePull,
}
