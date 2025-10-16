// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

var tsDBRouter = NewTsDBRouter()

var Print = func() string {
	var res string
	// tableInfo: tableId -> info
	res += fmt.Sprintln("tableInfo: tableID => [info]")
	res += fmt.Sprintln("----------------------------------------")
	for k, v := range tableInfos {
		res += fmt.Sprintf("%v => %+v\n", k, v)
	}
	res += fmt.Sprintln("----------------------------------------")

	// tsDBRouter:  dataID -> [tableInfo]
	res += fmt.Sprintln("tsDBRouter:  dataID => [tableInfo]")
	res += fmt.Sprintln("----------------------------------------")
	tsDBRouter.Range(func(k, v any) bool {
		if tableInfos, ok := v.([]*consul.TableID); ok {
			res += fmt.Sprintf("%v => %v\n", k, tableInfos)
		}
		return true
	})
	res += fmt.Sprintln("----------------------------------------")

	// metricRouter: metric -> [dataID]
	res += fmt.Sprintln("metricRouter: metric => [dataID]")
	res += fmt.Sprintln("----------------------------------------")
	metricRouter.Range(func(k, v any) bool {
		if dataID, ok := v.(consul.DataIDs); ok {
			res += fmt.Sprintf("%v => %v\n", k, dataID)
		}
		return true
	})
	res += fmt.Sprintln("----------------------------------------")

	// bizRouter:  bizID -> [dataID]
	res += fmt.Sprintln(" bizRouter:  bizID => [dataID]")
	res += fmt.Sprintln("----------------------------------------")
	bizRouter.Range(func(k, v any) bool {
		if dataID, ok := v.(consul.DataIDs); ok {
			res += fmt.Sprintf("%v => %v\n", k, dataID)
		}
		return true
	})
	res += fmt.Sprintln("----------------------------------------")

	// tableRouter:  db.measurement -> tableID
	res += fmt.Sprintln("tableRouter:  db.measurement => ClusterID, DB, Measurement, IsSplitMeasurement")
	res += fmt.Sprintln("----------------------------------------")
	tableRouter.Range(func(k, v any) bool {
		if tableID, ok := v.(*consul.TableID); ok {
			res += fmt.Sprintf(
				"%v => %v, %v, %v, %v\n",
				k, tableID.ClusterID, tableID.DB, tableID.Measurement, tableID.IsSplitMeasurement,
			)
		}
		return true
	})
	res += fmt.Sprintln("----------------------------------------")
	return res
}

// TsDBRouter :map[consul.DataID][]*consul.TableID // dataID -> [tableInfo]
type TsDBRouter struct {
	*sync.Map
}

// NewTsDBRouter
func NewTsDBRouter() *TsDBRouter {
	return &TsDBRouter{
		Map: &sync.Map{},
	}
}

// GetTsDBRouter
func GetTsDBRouter() *TsDBRouter {
	return tsDBRouter
}

// Value
func (r *TsDBRouter) Value(v any) []*consul.TableID {
	return v.([]*consul.TableID)
}

// GetTableIDs
func (r *TsDBRouter) GetTableIDs(dataID ...consul.DataID) []*consul.TableID {
	var tmpTableIDs []*consul.TableID
	for _, id := range dataID {
		value, ok := r.Load(id)
		if ok {
			v := r.Value(value)
			tmpTableIDs = append(tmpTableIDs, v...)
		}
	}
	return tmpTableIDs
}

// AddTables
func (r *TsDBRouter) AddTables(dataID consul.DataID, tables []*consul.TableID) {
	r.Store(dataID, tables)
}

// GetTableIDsByDataID : 根据projectID获取tableID列表
var GetTableIDsByDataID = func(dataID consul.DataID) []*consul.TableID {
	return GetTsDBRouter().GetTableIDs(dataID)
}

var bizRouter = NewBizRouter()

// BizRouter
type BizRouter struct {
	*sync.Map
	keys []int
}

// NewBizRouter
func NewBizRouter() *BizRouter {
	return &BizRouter{
		Map: &sync.Map{},
	}
}

// GetBizRouter
func GetBizRouter() *BizRouter {
	return bizRouter
}

// Value
func (b *BizRouter) Value(v any) consul.DataIDs {
	return v.(consul.DataIDs)
}

// GetRouter
func (b *BizRouter) GetRouter(ids ...int) consul.DataIDs {
	var result consul.DataIDs
	for _, id := range ids {
		if val, ok := b.Load(id); ok {
			result = append(result, b.Value(val)...)
		}
	}
	return result
}

// AddRouter
func (b *BizRouter) AddRouter(id int, dataID ...consul.DataID) {
	var tmp consul.DataIDs
	if res, ok := b.Load(id); ok {
		tmp = b.Value(res)
	}
	tmp = append(tmp, dataID...)
	b.Store(id, tmp)
	b.keys = append(b.keys, id)
}

// Keys
func (b *BizRouter) Keys() []int {
	return b.keys
}

var tableRouter = NewTableRouter()

// TableRouter
type TableRouter struct {
	*sync.Map
}

// NewTableRouter
func NewTableRouter() *TableRouter {
	return &TableRouter{
		Map: &sync.Map{},
	}
}

// GetTableRouter
func GetTableRouter() *TableRouter {
	return tableRouter
}

// Key
func (t *TableRouter) Key(db, measurement string) string {
	return fmt.Sprintf("%s.%s", db, measurement)
}

// GetTableID
func (t *TableRouter) GetTableID(db, measurement string) *consul.TableID {
	var (
		res any
		ok  bool
	)

	// 如果有配置measurement则取对应的配置
	res, ok = t.Load(t.Key(db, measurement))
	if ok {
		return res.(*consul.TableID)
	}
	// 没有配置measurement则取 __default__ 的，源数据会把 __default__ 的 measurement 置为空
	res, ok = t.Load(t.Key(db, ""))
	if ok {
		return res.(*consul.TableID)
	}

	return &consul.TableID{
		DB:                 db,
		Measurement:        measurement,
		IsSplitMeasurement: false,
	}
}

// AddTableID
func (t *TableRouter) AddTableID(tableID *consul.TableID) {
	t.Store(t.Key(tableID.DB, tableID.Measurement), tableID)
}

// GetTableIDByDBAndMeasurement 根据db和measurement获取tableID
var GetTableIDByDBAndMeasurement = func(db, measurement string) *consul.TableID {
	return GetTableRouter().GetTableID(db, measurement)
}

// ReloadTableInfos : 重载 influxdb 路由表信息
func ReloadTableInfos(pipelineConfMap map[string][]*consul.PipelineConfig) {
	var (
		err            error
		tmpTsDBRouter  = NewTsDBRouter()
		tmpBizRouter   = NewBizRouter()
		tmpTableRouter = NewTableRouter()
	)

	for _, pipelineConfList := range pipelineConfMap {
		for _, pipeConf := range pipelineConfList {

			dataID := pipeConf.DataID

			var tables []*consul.TableID
			for _, resultTable := range pipeConf.ResultTableList {

				// 添加biz -> dataIDList 到临时路由
				tmpBizRouter.AddRouter(resultTable.BizID, dataID)

				// 从 pipeline.resultTable 中获取tableInfo
				tableID := &consul.TableID{}
				err = resultTable.GetTSInfo(dataID, tableID)
				// 忽略错误
				if err != nil {
					continue
				}
				tables = append(tables, tableID)
				tmpTableRouter.AddTableID(tableID)
			}

			// 添加 dataID -> tableInfo 到临时路由
			if len(tables) != 0 {
				tmpTsDBRouter.AddTables(dataID, tables)
			}

		}
	}
	log.Debugf(context.TODO(), "set ts router: %v", tmpTsDBRouter)
	log.Debugf(context.TODO(), "set biz router: %v", tmpBizRouter)
	log.Debugf(context.TODO(), "set table router: %v", tmpTableRouter)

	tsDBRouter = tmpTsDBRouter
	bizRouter = tmpBizRouter
	tableRouter = tmpTableRouter
}

var metricRouter = NewMetricRouter()

// MetricRouter map[string]consul.DataIDs
type MetricRouter struct {
	*sync.Map
	keys []string
}

// NewMetricRouter
func NewMetricRouter() *MetricRouter {
	return &MetricRouter{
		Map: &sync.Map{},
	}
}

// GetMetricRouter
func GetMetricRouter() *MetricRouter {
	return metricRouter
}

// Value
func (m *MetricRouter) Value(v any) consul.DataIDs {
	return v.(consul.DataIDs)
}

// GetRouter :
func (m *MetricRouter) GetRouter(metrics ...string) consul.DataIDs {
	var (
		tmpDataIDMap = make(map[consul.DataID]struct{})
		result       consul.DataIDs
		index        int
	)
	for _, metric := range metrics {
		if val, ok := m.Load(metric); ok {
			for _, id := range m.Value(val) {
				tmpDataIDMap[id] = struct{}{}
			}
		}
	}
	result = make(consul.DataIDs, len(tmpDataIDMap))
	for id := range tmpDataIDMap {
		result[index] = id
		index++
	}
	sort.Sort(result)
	return result
}

// AddRouter :
func (m *MetricRouter) AddRouter(metric string, dataID ...consul.DataID) {
	var tmp consul.DataIDs
	if res, ok := m.Load(metric); ok {
		tmp = m.Value(res)
	}
	tmp = append(tmp, dataID...)
	m.Store(metric, tmp)
	m.keys = append(m.keys, metric)
}

// Keys : metrics
func (m *MetricRouter) Keys() []string {
	return m.keys
}

// ReloadMetricRouter: 重载 influxdb 指标路由表信息
// dataidMetrics: {dataid:[metrics]}, eg: {1001: ["usage", "idle", "iowait"], 1002: ["free", "total"]}
func ReloadMetricRouter(dataidMetrics map[int][]string) {
	tmpMetricRouter := NewMetricRouter()
	metricDataids := make(map[string]consul.DataIDs, 0)
	for dataid, metrics := range dataidMetrics {
		for _, metric := range metrics {
			metricDataids[metric] = append(metricDataids[metric], consul.DataID(dataid))
			tmpMetricRouter.AddRouter(metric, consul.DataID(dataid))
		}
	}
	log.Debugf(context.TODO(), "set metric router: %v", tmpMetricRouter)
	metricRouter = tmpMetricRouter
}
