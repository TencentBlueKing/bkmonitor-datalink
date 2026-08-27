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
	if config.RelationDataID == 0 {
		logger.Infof("[ReportCustomRelation] skip because taskConfig.relationDataID is not configured")
		return nil
	}
	if config.PromRemoteWriteUrl == "" {
		logger.Infof("[ReportCustomRelation] skip because Prometheus remote-write URL is not configured")
		return nil
	}

	db := mysql.GetDBSession().DB
	var statuses []relation.CustomRelationStatus
	qs := relation.NewCustomRelationStatusQuerySet(db)

	err := qs.All(&statuses)
	if err != nil {
		logger.Errorf("[ReportCustomRelation] query custom relation status error: %v", err)
		return err
	}

	return reportCustomRelationStatuses(ctx, statuses)
}

// ReportCustomRelationByNamespace immediately reports the latest custom relation
// instances for one business namespace after a metadata change notification.
func ReportCustomRelationByNamespace(ctx context.Context, namespace string) error {
	if namespace == "" {
		return nil
	}

	db := mysql.GetDBSession().DB
	var statuses []relation.CustomRelationStatus
	if err := relation.NewCustomRelationStatusQuerySet(db).NamespaceEq(namespace).All(&statuses); err != nil {
		logger.Errorf("[ReportCustomRelationByNamespace] query namespace=%s error: %v", namespace, err)
		return err
	}

	logger.Infof(
		"[ReportCustomRelationByNamespace] namespace=%s custom relation status count=%d",
		namespace,
		len(statuses),
	)
	return reportCustomRelationStatuses(ctx, statuses)
}

func reportCustomRelationStatuses(ctx context.Context, statuses []relation.CustomRelationStatus) (err error) {
	if len(statuses) == 0 {
		logger.Infof("[ReportCustomRelation] no custom relation status records found")
		return nil
	}

	logger.Infof("[ReportCustomRelation] found %d custom relation status records", len(statuses))

	reporter, err := remote.NewRelationDataIDReporter(
		config.RelationDataID,
		config.PromRemoteWriteUrl,
		config.PromRemoteWriteHeaders,
	)
	if err != nil {
		logger.Errorf("[ReportCustomRelation] create relation DataID reporter error: %v", err)
		return err
	}
	defer func() {
		closeErr := reporter.Close(ctx)
		if err == nil {
			err = closeErr
		}
	}()

	// namespace is source metadata only. All configured relation metrics share one DataID target.
	now := time.Now()
	ts := customTsPool.Get().([]prompb.TimeSeries)
	defer func() {
		customTsPool.Put(ts[:0])
	}()
	for _, status := range statuses {
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
		if err = reporter.Write(ctx, ts...); err != nil {
			logger.Errorf("[ReportCustomRelation] report custom relation error: %v", err)
			return err
		}
	}

	return nil
}
