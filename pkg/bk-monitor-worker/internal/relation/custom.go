// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package relation

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/prometheus/prometheus/prompb"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/relation"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	t "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/remote"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var customTsPool = sync.Pool{
	New: func() any {
		return make([]prompb.TimeSeries, 0)
	},
}

// ReportCustomRelation 上报自定义关联数据
func ReportCustomRelation(ctx context.Context, t *t.Task) error {
	logger.Infof("[ReportCustomRelation] start reporting custom relation data")

	// 获取数据库连接
	db := mysql.GetDBSession().DB

	// 查询所有 customrelationstatus 记录
	var statuses []relation.CustomRelationStatus
	qs := relation.NewCustomRelationStatusQuerySet(db)

	err := qs.All(&statuses)
	if err != nil {
		logger.Errorf("[ReportCustomRelation] query custom relation status error: %v", err)
		return err
	}

	if len(statuses) == 0 {
		logger.Infof("[ReportCustomRelation] no custom relation status records found")
		return nil
	}

	logger.Infof("[ReportCustomRelation] found %d custom relation status records", len(statuses))

	// 启动指标上报
	reporter, err := remote.NewSpaceReporter(config.BuildInResultTableDetailKey, config.PromRemoteWriteUrl)
	if err != nil {
		logger.Errorf("[ReportCustomRelation] create space reporter error: %v", err)
		return err
	}
	defer func() {
		err = reporter.Close(ctx)
	}()

	// 按 namespace 分组处理
	customRelationStatusMap := make(map[string][]relation.CustomRelationStatus)
	for _, status := range statuses {
		customRelationStatusMap[status.Namespace] = append(customRelationStatusMap[status.Namespace], status)
	}

	// 为每个业务构建relation指标
	now := time.Now()
	for namespace, statusList := range customRelationStatusMap {
		logger.Infof("[ReportCustomRelation] processing namespace: %s, records: %d", namespace, len(statusList))

		ts := customTsPool.Get().([]prompb.TimeSeries)
		for _, status := range statusList {
			// 解析Labels字段（JSON字符串）
			var labels map[string]string
			if status.Labels != "" {
				err = json.Unmarshal([]byte(status.Labels), &labels)
				if err != nil {
					logger.Warnf("[ReportCustomRelation] parse labels error for record %s: %v", status.Name, err)
					continue
				}
			}
			if len(labels) == 0 {
				logger.Warnf("[ReportCustomRelation] empty labels for record %s", status.Name)
				continue
			}

			sourceNode := Node{
				Name: status.FromResource,
			}

			ts = append(ts, (sourceNode.RelationMetric(Node{
				Name:   status.ToResource,
				Labels: labels,
			})).TimeSeries(now))
		}
		if len(ts) > 0 {
			if err = reporter.Do(ctx, namespace, ts...); err != nil {
				logger.Errorf("[ReportCustomRelation] report custom relation error: %v", err)
				return err
			}
		}

		ts = ts[:0]
		customTsPool.Put(ts)
	}

	return nil
}
