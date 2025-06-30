// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package task

import (
	"context"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/clustermetrics/es"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// CollectESTask 采集es指标任务
func CollectESTask(ctx context.Context, t *task.Task) error {
	var params es.CollectESTaskParams
	if err := jsonx.Unmarshal(t.Payload, &params); err != nil {
		return errors.Wrapf(err, "parse params for collectAndReportMetricsParams with [%s] error", t.Payload)
	}
	c := params.ClusterInfo
	err := es.CollectAndReportMetrics(c)
	if err != nil {
		logger.Errorf("es_cluster_info: [%v] name [%s] try to collect and report metrics failed, %v", c.ClusterID, c.ClusterName, err)
	} else {
		logger.Infof("es_cluster_info: [%v] name [%s] collect and report metrics success", c.ClusterID, c.ClusterName)
	}
	return nil
}
