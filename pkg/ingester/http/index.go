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
	"io/ioutil"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/datasource"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/poller"
)

func Ping(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"result":  true,
		"message": "pong",
	})
}

// SendEvent 接收事件数据并发送到队列中
func SendEvent(c *gin.Context) {
	logger := logging.GetLogger()

	receiverID := c.Param("receiverID")

	rawData, err := ioutil.ReadAll(c.Request.Body)
	if err != nil {
		logger.Warnf("Error occured when reading request body: %+v", err)
		c.JSON(http.StatusBadRequest, define.NewHttpResponseFailed(c, err))
		return
	}

	r := GetReceiver(receiverID)

	logger.Debugf("using plugin: %+v, datasource: %+v, raw data: %s", r.Plugin, r.DataSource, rawData)

	// 反序列化
	data, err := r.UnmarshalEvents(rawData)
	if err != nil {
		logger.Warnf("unmarshal response data error: %+v", err)
		c.JSON(http.StatusBadRequest, define.NewHttpResponseFailed(c, err))
		r.UpdateMetric(false, 1)
		return
	}

	// 类型转换
	events, err := r.ConvertEvents(data)
	if err != nil {
		logger.Warnf("convert to event data error: %+v", err)
		c.JSON(http.StatusBadRequest, define.NewHttpResponseFailed(c, err))
		r.UpdateMetric(false, 1)
		return
	}

	payload := define.Payload{
		IgnoreResult: c.Query("ignore_result") != "",
	}

	payload.AddEvents(events...)

	if c.Query("debug") != "" {
		// debug 模式，不发送数据，仅返回解析后的数据
		c.JSON(http.StatusOK, define.NewHttpResponseSuccess(c, events))
		return
	}

	err = r.Push(payload)

	if err != nil {
		c.JSON(http.StatusInternalServerError, define.NewHttpResponseFailed(c, err))
		r.UpdateMetric(false, payload.GetEventCount())
		return
	}

	c.JSON(http.StatusOK, define.NewHttpResponseSuccess(c, nil))
	r.UpdateMetric(true, payload.GetEventCount())
}

func Plugin(c *gin.Context) {
	var plugins []map[string]interface{}
	subscribers := datasource.ListAllSubscribers()
	for name, subscriber := range subscribers {
		for _, dataSource := range subscriber.ListDataSources() {
			plugin, err := dataSource.GetPluginOption()
			if err != nil {
				continue
			}
			plugins = append(plugins, map[string]interface{}{
				"bk_data_id":   dataSource.DataID,
				"plugin_id":    plugin.PluginID,
				"plugin_type":  name,
				"backend_type": dataSource.MQConfig.ClusterType,
			})
		}
	}
	c.JSON(http.StatusOK, define.NewHttpResponseSuccess(c, plugins))
}

func PollerTask(c *gin.Context) {
	var tasks []map[string]interface{}
	registerdTasks := poller.ListRegisteredTask()
	for taskID, task := range registerdTasks {
		tasks = append(tasks, map[string]interface{}{
			"bk_data_id": task.DataSource.DataID,
			"plugin_id":  task.Plugin.PluginID,
			"bk_biz_id":  task.Plugin.BusinessID,
			"task_id":    taskID,
		})
	}
	c.JSON(http.StatusOK, define.NewHttpResponseSuccess(c, tasks))
}

func ReceiverTask(c *gin.Context) {
	var receivers []map[string]interface{}
	for receiverID, task := range receiverRegistry {
		receivers = append(receivers, map[string]interface{}{
			"bk_data_id":  task.DataSource.DataID,
			"plugin_id":   task.Plugin.PluginID,
			"bk_biz_id":   task.Plugin.BusinessID,
			"receiver_id": receiverID,
		})
	}
	c.JSON(http.StatusOK, define.NewHttpResponseSuccess(c, receivers))
}
