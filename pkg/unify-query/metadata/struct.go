// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/VictoriaMetrics/metricsql"
	"github.com/prometheus/prometheus/model/labels"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/set"
)

const (
	StaticField = "value"

	UUID = "query_uuid"
)

const (
	ConditionEqual       = "eq"
	ConditionNotEqual    = "ne"
	ConditionRegEqual    = "req"
	ConditionNotRegEqual = "nreq"
	ConditionContains    = "contains"
	ConditionNotContains = "ncontains"

	ConditionExisted    = "existed"
	ConditionNotExisted = "nexisted"

	ConditionExact = "exact"
	ConditionGt    = "gt"
	ConditionGte   = "gte"
	ConditionLt    = "lt"
	ConditionLte   = "lte"
)

type VmCondition string

type TimeField struct {
	Name string
	Type string
	Unit string
}

// Aggregate 聚合方法
type Aggregate struct {
	Name  string
	Field string

	Dimensions []string
	Without    bool

	Window   time.Duration
	TimeZone string

	Args []interface{}
}

// OffSetInfo Offset的信息存储，供promql查询转换为influxdb查询语句时使用
type OffSetInfo struct {
	OffSet  time.Duration
	Limit   int
	SOffSet int
	SLimit  int
}

type Aggregates []Aggregate

// Query 查询扩展信息，为后面查询提供定位
type Query struct {
	SourceType string
	Password   string // 查询鉴权

	ClusterID string // 存储 ID

	StorageType string // 存储类型

	StorageIDs  []string
	StorageID   string
	StorageName string

	ClusterName string
	TagsKey     []string

	DataSource string
	DataLabel  string
	TableID    string
	MetricName string

	// vm 的 rt
	VmRt string

	// 兼容 InfluxDB 结构体
	RetentionPolicy string    // 存储 RP
	DB              string    // 存储 DB
	Measurement     string    // 存储 Measurement
	Field           string    // 存储 Field
	TimeField       TimeField // 时间字段
	Timezone        string    // 存储 Timezone
	Fields          []string  // 存储命中的 Field 列表，一般情况下为一个，当 Field 为模糊匹配时，解析为多个
	Measurements    []string  // 存储命中的 Measurement 列表，一般情况下为一个，当 Measurement 为模糊匹配时，解析为多个

	// 用于 promql 查询
	IsHasOr bool // 标记是否有 or 条件

	Aggregates Aggregates // 聚合方法列表，从内到外排序

	Condition string // 过滤条件

	// BkSql 过滤条件
	BkSqlCondition string

	// Vm 过滤条件
	VmCondition    VmCondition
	VmConditionNum int

	Filters []map[string]string // 查询中自带查询条件，用于拼接

	OffsetInfo OffSetInfo // limit等偏移量配置

	SegmentedEnable bool // 是否开启分段查询

	// Es 查询扩展
	QueryString   string
	AllConditions AllConditions

	HighLight HighLight

	Source      []string
	From        int
	Size        int
	Orders      Orders
	NeedAddTime bool
}

type HighLight struct {
	MaxAnalyzedOffset int  `json:"max_analyzed_offset,omitempty"`
	Enable            bool `json:"enable,omitempty"`
}

type Orders map[string]bool

type AllConditions [][]ConditionField

type QueryList []*Query

type QueryMetric struct {
	QueryList QueryList

	ReferenceName string
	MetricName    string

	IsCount bool // 标记是否为 count 方法
}

// ConditionField 过滤条件的字段描述
type ConditionField struct {
	// DimensionName 过滤字段
	DimensionName string
	// Value 查询值
	Value []string
	// Operator 操作符，包含：eq, ne, erq, nreq, contains, ncontains
	Operator string

	// IsWildcard 是否是通配符
	IsWildcard bool
}

// TimeAggregation 时间聚合字段
type TimeAggregation struct {
	// Function 时间聚合方法
	Function string
	// Window 聚合周期
	WindowDuration time.Duration

	Without bool
}

type QueryClusterMetric struct {
	MetricName      string
	Aggregates      Aggregates         // 聚合方法列表，从内到外排序
	Conditions      [][]ConditionField // 用户请求的完整过滤条件，来源 structured 定义
	TimeAggregation TimeAggregation
}

type QueryReference map[string]*QueryMetric

type Queries struct {
	Query QueryReference

	ctx                   context.Context
	directlyClusterID     string
	directlyMetricName    map[string]string
	directlyLabelsMatcher map[string][]*labels.Matcher
	directlyResultTable   map[string][]string
}

type ReplaceLabels map[string]ReplaceLabel

type ReplaceLabel struct {
	Source string
	Target string
}

func ReplaceVmCondition(condition VmCondition, replaceLabels ReplaceLabels) VmCondition {
	if len(replaceLabels) == 0 {
		return condition
	}

	expr, err := metricsql.Parse(condition.ToMatch())
	if err != nil {
		return condition
	}

	me, ok := expr.(*metricsql.MetricExpr)
	if !ok {
		return condition
	}

	var cond []byte
	for i, f := range me.LabelFilterss {
		var dst []byte
		for j, l := range f {
			if rl, exist := replaceLabels[l.Label]; exist {
				if l.Value == rl.Source {
					l.Value = rl.Target
				}
			}

			if j == 0 {
				dst = l.AppendString(dst)
			} else {
				dst = append(dst, ',', ' ')
				dst = l.AppendString(dst)
			}
		}

		if i == 0 {
			cond = dst
		} else {
			cond = append(cond, " or "...)
			cond = append(cond, dst...)
		}
	}

	return VmCondition(cond)
}

// ToJson 通过 tableID 排序，并且返回 json 序列化
func (qMetric QueryMetric) ToJson(isSort bool) string {
	if isSort {
		sort.SliceIsSorted(qMetric.QueryList, func(i, j int) bool {
			return qMetric.QueryList[i].TableID < qMetric.QueryList[j].TableID
		})
	}

	s, _ := json.Marshal(qMetric)
	return string(s)
}

// ToVmExpand 判断是否是直查，如果都是 vm 查询的情况下，则使用直查模式
func (qRef QueryReference) ToVmExpand(_ context.Context) (vmExpand *VmExpand) {
	vmClusterNames := set.New[string]()
	vmResultTable := set.New[string]()
	metricFilterCondition := make(map[string]string)

	for referenceName, reference := range qRef {
		if 0 < len(reference.QueryList) {
			vmConditions := set.New[string]()
			for _, query := range reference.QueryList {
				if query.VmRt == "" {
					continue
				}

				vmResultTable.Add(query.VmRt)
				vmConditions.Add(string(query.VmCondition))
				vmClusterNames.Add(query.StorageName)
			}

			filterCondition := ""
			if vmConditions.Size() > 0 {
				filterCondition = fmt.Sprintf(`%s`, strings.Join(vmConditions.ToArray(), ` or `))
			}

			metricFilterCondition[referenceName] = filterCondition
		}
	}

	if vmResultTable.Size() == 0 {
		return
	}

	vmExpand = &VmExpand{
		MetricFilterCondition: metricFilterCondition,
		ResultTableList:       vmResultTable.ToArray(),
	}
	sort.Strings(vmExpand.ResultTableList)

	// 当所有的 vm 集群都一样的时候，才进行传递
	if vmClusterNames.Size() == 1 {
		vmExpand.ClusterName = vmClusterNames.First()
	}

	return
}

func (vs VmCondition) String() string {
	return string(vs)
}

func (vs VmCondition) ToMatch() string {
	return fmt.Sprintf("{%s}", vs)
}

// LastAggName 获取最新的聚合函数
func (a Aggregates) LastAggName() string {
	if len(a) == 0 {
		return ""
	}

	return a[len(a)-1].Name
}
