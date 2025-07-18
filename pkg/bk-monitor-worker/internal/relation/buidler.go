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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/prometheus/prompb"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/remote"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	Set    = "set"
	Module = "module"
	Host   = "host"

	ExpandInfoColumn = "version_meta"
)

var (
	bkIDKeys = map[string]string{
		Set:    "bk_set_id",
		Module: "bk_module_id",
		Host:   "bk_host_id",
	}
)

var (
	defaultRelationMetricsBuilder = newRelationMetricsBuilder()

	tsPool = sync.Pool{
		New: func() any {
			return make([]prompb.TimeSeries, 0)
		},
	}
)

func getTsPool() []prompb.TimeSeries {
	return tsPool.Get().([]prompb.TimeSeries)
}

func putTsPool(ts []prompb.TimeSeries) {
	ts = ts[:0]
	tsPool.Put(ts)
}

// MetricsBuilder 关联指标构建器，生成指标缓存以及输出 prometheus 上报指标
type MetricsBuilder struct {
	spaceReport remote.Reporter

	lock sync.RWMutex
	// 业务ID -> 资源类型（set、module、host) -> resourceInfo
	resources map[int]map[string]*ResourceInfo
}

func newRelationMetricsBuilder() *MetricsBuilder {
	return &MetricsBuilder{
		resources: make(map[int]map[string]*ResourceInfo),
	}
}

func GetRelationMetricsBuilder() *MetricsBuilder {
	return defaultRelationMetricsBuilder
}

func (b *MetricsBuilder) WithSpaceReport(reporter remote.Reporter) *MetricsBuilder {
	b.spaceReport = reporter
	return b
}

func (b *MetricsBuilder) GetResourceInfo(bizID int, name string) *ResourceInfo {
	b.lock.Lock()
	defer b.lock.Unlock()

	if _, ok := b.resources[bizID]; !ok {
		b.resources[bizID] = make(map[string]*ResourceInfo)
	}
	if _, ok := b.resources[bizID][name]; !ok {
		b.resources[bizID][name] = &ResourceInfo{}
	}

	return b.resources[bizID][name]
}

// ClearAllMetrics 清理全部指标
func (b *MetricsBuilder) ClearAllMetrics() {
	b.lock.RLock()
	b.resources = make(map[int]map[string]*ResourceInfo)
	b.lock.RUnlock()
}

func (b *MetricsBuilder) ClearResourceWithID(bizID int, name string, ids ...string) {
	resourceInfo := b.GetResourceInfo(bizID, name)

	b.lock.RLock()
	for _, id := range ids {
		resourceInfo.Delete(id)
	}
	b.lock.RUnlock()
}

func (b *MetricsBuilder) toString(s any) string {
	var newValue string
	switch value := s.(type) {
	case string:
		newValue = value
	case int, int32, int64, uint32, uint64:
		newValue = fmt.Sprintf("%d", value)
	case float64:
		newValue = strconv.FormatFloat(value, 'f', -1, 64)
	case float32:
		newValue = strconv.FormatFloat(float64(value), 'f', -1, 32)
	default:
		newValue = fmt.Sprintf("%v", value)
	}

	return newValue
}

func (b *MetricsBuilder) BuildInfosCache(_ context.Context, bizID int, name string, infos []*Info) error {
	if infos == nil {
		return nil
	}
	b.lock.Lock()
	oldInfos := b.GetResourceInfo(bizID, name)
	for _, info := range infos {
		oldInfos.Add(info.ID, info)
	}
	b.lock.Unlock()

	logger.Infof("[cmdb_relation] build info cache bkcc__%d %s %d", bizID, name, len(infos))
	return nil
}

func (b *MetricsBuilder) ToNodeList() {

}

// String 以 string 格式获取所有指标数据
func (b *MetricsBuilder) String() string {
	var buf bytes.Buffer
	b.lock.RLock()
	defer b.lock.RUnlock()

	for bizID, infos := range b.resources {
		for id, info := range infos {
			buf.WriteString("\n")
		}
	}

	return buf.String()
}

// PushAll 推送全业务数据
func (b *MetricsBuilder) PushAll(ctx context.Context, timestamp time.Time) error {
	if b.spaceReport == nil {
		return fmt.Errorf("space reporter is nil")
	}

	// 提前把 bkBizIDs 取出来，缩小锁区间，控制在单业务下
	bkBizIDs := make([]int, 0, len(b.resources))
	b.lock.RLock()
	for bizID := range b.resources {
		bkBizIDs = append(bkBizIDs, bizID)
	}
	b.lock.RUnlock()

	for bkBizID := range bkBizIDs {
		ts := getTsPool()
		metricsMap := make(map[string]struct{})

		b.lock.RLock()
		if nodeMap, ok := b.resources[bkBizID]; ok {
			for _, nodes := range nodeMap {
				for _, relationMetric := range nodes.toRelationMetrics() {
					d := relationMetric.TimeSeries(bkBizID, timestamp)
					if _, ok = metricsMap[d.String()]; !ok {
						metricsMap[d.String()] = struct{}{}
						ts = append(ts, d)
					}
				}
			}
		}
		b.lock.RUnlock()

		if len(ts) > 0 {
			// 上传业务 timeSeries
			spaceUID := fmt.Sprintf("bkcc__%d", bkBizID)
			if err := b.spaceReport.Do(ctx, spaceUID, ts...); err != nil {
				return err
			}
			logger.Infof("[cmdb_relation] push %s cmdb relation metrics %d", spaceUID, len(ts))
		}

		putTsPool(ts)
	}

	return nil
}
