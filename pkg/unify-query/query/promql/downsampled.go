// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package promql

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
)

const DefaultMeasurement = "__all__"
const LastModifyTimeFormat = `2006-01-02 15:04:05+0800`

// DownsampledQuery
type DownsampledQuery struct {
	Database    string
	WhereList   *WhereList
	Field       string
	Aggregation string
	Measurement string
	Window      time.Duration
}

// rpMap
type rpMap struct {
	name        string
	resolution  int64
	field       string
	aggregation string
}

// 查询rp里的resolution
func (q *DownsampledQuery) resolution(rpName string) int64 {
	rpBase := consul.DownsampledRetentionPolicy{
		Database: q.Database,
		RpName:   rpName,
	}
	if rp, ok := consul.DownsampledInfo.RetentionPolicies[rpBase.Key()]; ok {
		return rp.Resolution
	}
	return 0
}

// 判断downsampled database 配置中，tag条件是否符合
func (q *DownsampledQuery) checkTag(db consul.DownsampledDatabase) bool {
	if db.TagName == "" {
		return true
	}

	return q.WhereList.Check(db.TagName, db.TagValue)
}

// 判断downsampled database配置
func (q *DownsampledQuery) checkDatabase() bool {
	if db, ok := consul.DownsampledInfo.Databases[q.Database]; ok {
		if db.Enable && q.checkTag(db) {
			return true
		}
	}
	return false
}

// defaultRP 返回独立rp
func (q *DownsampledQuery) defaultRP() string {

	tmpRP := &consul.DownsampledRetentionPolicy{
		Database:    q.Database,
		Measurement: q.Measurement,
	}
	rp, ok := consul.DownsampledInfo.DBMeasurementRPMap[tmpRP.TableIDKey()]
	if ok {
		return rp.RpName
	}
	// 如果不存在独立rp，则试图查看是否是全库都使用独立RP
	tmpRP.Measurement = "__default__"
	if rp, ok = consul.DownsampledInfo.DBMeasurementRPMap[tmpRP.TableIDKey()]; ok {
		return rp.RpName
	}
	// 默认是让influxdb去判断默认rp，而不是autogen
	return ""
}

// 获取匹配的rp列表，并根据精度倒序
func (q *DownsampledQuery) getRpList() []rpMap {
	var rpMapList []rpMap
	var isCustomMeasurement bool

	// 判断是否使用定制measurement
	_, isCustomMeasurement = consul.DownsampledInfo.Measurements[q.Database][q.Measurement]

	// 如果不是定制的measurement，则使用默认的
	if !isCustomMeasurement {
		q.Measurement = DefaultMeasurement
	}

	// 获取所有匹配的rp列表
	aggregation, fieldType := q.getAggregationAndFieldType()
	cqBase := consul.DownsampledContinuousQuery{
		Database:    q.Database,
		Measurement: q.Measurement,
		Field:       q.Field,
		Aggregation: fieldType,
	}
	if cqs, ok := consul.DownsampledInfo.ContinuousQueries[cqBase.Key()]; ok {
		for _, cq := range cqs {
			t, err := time.ParseInLocation(LastModifyTimeFormat, cq.LastModifyTime, time.Local)
			resolution := q.resolution(cq.RpName)
			if err == nil {
				// 判断修改时间 + 精度周期是否大于当前时间
				checkT := t.Add(time.Duration(resolution) * time.Second)
				// 如果大于，则跳过，是担心当前的降精度数据还没有吗
				if checkT.Unix() > time.Now().Unix() {
					continue
				}
			}

			// 防止精度为0时，外部匹配rp时 integer divide by zero
			if resolution == 0 {
				continue
			}

			rpMapList = append(rpMapList, rpMap{
				name:        cq.RpName,
				resolution:  resolution,
				field:       q.getField(q.Field, fieldType),
				aggregation: aggregation,
			})
		}
	}

	// rp排序，按照 resolution 倒序
	sort.SliceStable(rpMapList, func(i, j int) bool {
		return rpMapList[i].resolution > rpMapList[j].resolution
	})
	return rpMapList
}

// 如果没有指定rpName则，自动匹配最近的，返回是否降精度，以及精度的rpName
func (q *DownsampledQuery) getRp() (string, string, string) {

	// 由于getRpList中可能会修改measurement，此处优先获取到defaultRP
	defaultRP := q.defaultRP()
	// 判断 downsampled database 配置，包括 database 和 tag
	if !q.checkDatabase() {
		return defaultRP, q.Field, q.Aggregation
	}

	m := q.getRpList()
	for _, v := range m {
		sec := int64(q.Window.Seconds())
		// 判断时间大于精度，同时要是精度的倍数
		if sec >= v.resolution && sec%v.resolution == 0 {
			return v.name, v.field, v.aggregation
		}
	}

	return defaultRP, q.Field, q.Aggregation
}

// getField
func (q *DownsampledQuery) getField(field, fieldType string) string {
	if fieldType != "" {
		return fmt.Sprintf("%s_%s", fieldType, field)
	}
	return field
}

// 获取降精度场景下对应的聚合方法和字段
func (q *DownsampledQuery) getAggregationAndFieldType() (string, string) {
	// 判断如果是降精度则需要根据聚合方法不同重新定位聚合方法以及字段
	switch q.Aggregation {
	// 如果是 count 的聚合，在降精度场景下需要使用 sum("count_value")
	case COUNT:
		return SUM, COUNT
	case MIN, MAX, SUM, LAST:
		return q.Aggregation, q.Aggregation
	default:
		return MEAN, MEAN
	}
}

// GetRp
func GetRp(
	_ context.Context, database, measurement, field, aggregation string, window time.Duration, whereList *WhereList,
) (string, string, string) {
	var q = &DownsampledQuery{
		Database:    database,
		Measurement: measurement,
		Field:       field,
		Aggregation: aggregation,
		Window:      window,
		WhereList:   whereList,
	}
	return q.getRp()
}
