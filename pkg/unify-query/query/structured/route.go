// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package structured

import (
	"errors"
	"fmt"
	"strings"

	"github.com/prometheus/prometheus/model/labels"
)

type TableID string

// Split 按照格式解析 TableID
func (t TableID) Split() (string, string) {
	info := strings.Split(string(t), ".")
	var a, b string
	if len(info) > 0 {
		a = info[0]
	}
	if len(info) > 1 {
		b = info[1]
	}
	return a, b
}

// Route 数据路由
type Route struct {
	clusterID   string
	dataSource  string // 数据源, 如 bkmonitor
	db          string // 数据库, 如 system
	measurement string // 数据表, 如 cpu_summary
	metricName  string // 指标名, 如 usage
	matchType   labels.MatchType
}

// MatchType 获取路由查询规则
func (r *Route) MatchType() labels.MatchType {
	return r.matchType
}

// DataSource
func (r *Route) DataSource() string {
	return r.dataSource
}

// ClusterID
func (r *Route) ClusterID() string {
	return r.clusterID
}

// SetClusterID
func (r *Route) SetClusterID(clusterID string) {
	r.clusterID = clusterID
}

// DB
func (r *Route) DB() string {
	return r.db
}

// Measurement
func (r *Route) Measurement() string {
	return r.measurement
}

// MetricName
func (r *Route) MetricName() string {
	return r.metricName
}

// SetMetricName
func (r *Route) SetMetricName(name string) {
	r.metricName = name
}

// TableID 返回当前路由的 table_id
func (r *Route) TableID() TableID {
	table := r.DB()
	if r.Measurement() != "" {
		table = fmt.Sprintf("%s.%s", table, r.Measurement())
	}
	return TableID(table)
}

// RealMetricName 这里替换成真正对外的指标名称，需要支持指标二段式
// 拼装规则 {dataSource}:{db}:{measurement}:{metricName}
func (r *Route) RealMetricName() string {
	metricList := make([]string, 0)

	// 拼接前缀
	if r.DataSource() != "" {
		metricList = append(metricList, r.DataSource())
	} else {
		metricList = append(metricList, BkMonitor)
	}

	if r.DB() != "" {
		metricList = append(metricList, r.DB())
	}

	if r.Measurement() != "" {
		metricList = append(metricList, r.Measurement())
	}

	if r.MetricName() != "" {
		metricList = append(metricList, r.MetricName())
	}

	return strings.Join(metricList, ":")
}

// MakeRouteFromTableID table 转换为路由表, 格式规范 {DB}.{Measurement}
func MakeRouteFromTableID(tableID TableID) (*Route, error) {
	route := &Route{
		dataSource: BkMonitor,
	}
	route.db, route.measurement = tableID.Split()
	return route, nil
}

// MakeRouteFromLabelMatch labelMatch 转换为路由
func MakeRouteFromLabelMatch(matches []*labels.Matcher) (*Route, error) {
	r := &Route{}
	for _, m := range matches {
		if m.Name == bkDatabaseLabelName {
			r.db = m.Value
		} else if m.Name == bkMeasurementLabelName {
			r.measurement = m.Value
		} else if m.Name == labels.MetricName {
			r.metricName = m.Value
		}
	}

	// 至少有下面 3 个 label 才满足规范
	if r.db == "" || r.measurement == "" || r.metricName == "" {
		return nil, errors.New("wrong label match format")
	}

	return r, nil
}

// MakeRouteFromMetricName 反向生成路由信息 dataSource:db:tableId(measurement):metricName
// 这里针对两种时序查询格式支持
// 1. 指定库表的描述: bkmonitor:${db}:${table}:${metric}
// 2. 指定dataID范围的: bkmonitor:${metric}  这种情况在解析之后查询时必须要配合dataIDList
func MakeRouteFromMetricName(name string) (*Route, error) {
	sn := strings.Split(name, ":")
	metricName := sn[len(sn)-1]
	if metricName == "" {
		return nil, ErrMetricMissing
	}

	// 如果第一位不是 dataSource 里面，则补充 bkmonitor
	var split []string
	if len(sn) < 4 {
		if _, ok := dataSourceMap[sn[0]]; !ok {
			split = make([]string, 0, len(split)+1)
			split = append(split, BkMonitor)
		} else {
			split = make([]string, 0, len(split))
		}
	}
	split = append(split, sn...)

	switch len(split) {
	case 4:
		// 第一种格式
		return &Route{
			dataSource:  split[0],
			db:          split[1],
			measurement: split[2],
			metricName:  split[3],
		}, nil
	case 3:
		return &Route{
			dataSource:  split[0],
			db:          split[1],
			measurement: "",
			metricName:  split[2],
		}, nil
	case 2:
		// 第二种格式
		// 防止有重复的查询，这里做去重处理
		return &Route{
			dataSource:  split[0],
			db:          "",
			measurement: "",
			metricName:  split[1],
		}, nil
	default:
		// TODO: 这里可能还有其他情况 比如 DB/TableID?
		return &Route{metricName: metricName}, nil
	}
}

// MakeRouteFromLBMatchOrMetricName :
func MakeRouteFromLBMatchOrMetricName(matches []*labels.Matcher) (*Route, error) {
	// label match 是精确匹配，优先使用
	route, err := MakeRouteFromLabelMatch(matches)
	if err == nil {
		return route, nil
	}

	for _, m := range matches {
		if m.Name == labels.MetricName {
			route, err = MakeRouteFromMetricName(m.Value)
			if err == nil {
				return route, nil
			}
			break
		}
	}

	return nil, errors.New("wrong label match or metric name format")
}

// MakeRouteByDBTable:
func MakeRouteByDBTable(db, measurement string) *Route {
	return &Route{
		dataSource:  BkMonitor,
		db:          db,
		measurement: measurement,
		metricName:  "",
	}
}
