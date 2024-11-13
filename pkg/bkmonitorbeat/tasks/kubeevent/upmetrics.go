// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kubeevent

import (
	"fmt"
	"strconv"
	"time"

	"github.com/elastic/beats/libbeat/common"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
)

func CodeMetrics(dataID int32, taskConfig define.TaskConfig, receive, report, cleaned int64) *tasks.GatherUpEvent {
	dims := common.MapStr{
		"task_id":         strconv.Itoa(int(taskConfig.GetTaskID())),
		"bk_collect_type": taskConfig.GetType(),
		"bk_biz_id":       strconv.Itoa(int(taskConfig.GetBizID())),
	}

	// 从配置文件中获取维度字段
	for _, labels := range taskConfig.GetLabels() {
		for k, v := range labels {
			dims[k] = v
		}
	}

	ev := &tasks.GatherUpEvent{
		DataID:     dataID,
		Time:       time.Now(),
		Dimensions: dims,
		Metrics: common.MapStr{
			define.NameKubeEventReceiveEvents: float64(receive),
			define.NameKubeEventReportEvents:  float64(report),
			define.NameKubeEventCleanedEvents: float64(cleaned),
		},
	}

	var kvs []define.LogKV
	for k, v := range dims {
		kvs = append(kvs, define.LogKV{K: k, V: v})
	}
	for k, v := range ev.Metrics {
		define.RecordLog(fmt.Sprintf("[%s] %s{} %f", taskConfig.GetType(), k, v), kvs)
	}
	return ev
}
