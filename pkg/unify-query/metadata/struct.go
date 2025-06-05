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
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/VictoriaMetrics/metricsql"
	"github.com/prometheus/prometheus/model/labels"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
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
	Name string `json:"name,omitempty"`
	Type string `json:"type,omitempty"`
	Unit string `json:"unit,omitempty"`
}

// Aggregate 聚合方法
type Aggregate struct {
	Name  string `json:"name,omitempty"`
	Field string `json:"field,omitempty"`

	Dimensions []string `json:"dimensions,omitempty"`
	Without    bool     `json:"without,omitempty"`

	Window   time.Duration `json:"window,omitempty"`
	TimeZone string        `json:"time_zone,omitempty"`

	Args []interface{} `json:"args,omitempty"`
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
	SourceType string `json:"source_type,omitempty"`
	Password   string `json:"password,omitempty"` // 查询鉴权

	ClusterID string `json:"cluster_id,omitempty"` // 存储 ID

	StorageType string `json:"storage_type,omitempty"` // 存储类型

	StorageIDs  []string `json:"storage_ids,omitempty"`
	StorageID   string   `json:"storage_id,omitempty"`
	StorageName string   `json:"storage_name,omitempty"`

	ClusterName string   `json:"cluster_name,omitempty"`
	TagsKey     []string `json:"tags_key,omitempty"`

	DataSource string `json:"data_source,omitempty"`
	DataLabel  string `json:"data_label,omitempty"`
	TableID    string `json:"table_id,omitempty"`
	MetricName string `json:"metric_name,omitempty"`

	// vm 的 rt
	VmRt string `json:"vm_rt,omitempty"`

	// 兼容 InfluxDB 结构体
	RetentionPolicy string     `json:"retention_policy,omitempty"` // 存储 RP
	DB              string     `json:"db,omitempty"`               // 存储 DB
	Measurement     string     `json:"measurement,omitempty"`      // 存储 Measurement
	Field           string     `json:"field,omitempty"`            // 存储 Field
	TimeField       TimeField  `json:"time_field,omitempty"`       // 时间字段
	Timezone        string     `json:"timezone,omitempty"`         // 存储 Timezone
	Fields          []string   `json:"fields,omitempty"`           // 存储命中的 Field 列表，一般情况下为一个，当 Field 为模糊匹配时，解析为多个
	FieldAlias      FieldAlias `json:"field_alias,omitempty"`
	Measurements    []string   `json:"measurements,omitempty"` // 存储命中的 Measurement 列表，一般情况下为一个，当 Measurement 为模糊匹配时，解析为多个

	// 用于 promql 查询
	IsHasOr bool `json:"is_has_or,omitempty"` // 标记是否有 or 条件

	Aggregates Aggregates `json:"aggregates,omitempty"` // 聚合方法列表，从内到外排序

	Condition string `json:"condition,omitempty"` // 过滤条件

	// BkSql 过滤条件
	BkSqlCondition string `json:"bk_sql_condition,omitempty"`

	// Vm 过滤条件
	VmCondition    VmCondition `json:"vm_condition,omitempty"`
	VmConditionNum int         `json:"vm_condition_num,omitempty"`

	Filters []map[string]string `json:"filters,omitempty"` // 查询中自带查询条件，用于拼接

	OffsetInfo OffSetInfo `json:"offset_info,omitempty"` // limit等偏移量配置

	SegmentedEnable bool `json:"segmented_enable,omitempty"` // 是否开启分段查询

	// 查询扩展
	QueryString string `json:"query_string,omitempty"`
	IsPrefix    bool   `json:"is_prefix,omitempty"`

	AllConditions AllConditions `json:"all_conditions,omitempty"`

	HighLight *HighLight `json:"high_light,omitempty"`

	Source []string `json:"source,omitempty"`
	From   int      `json:"from,omitempty"`
	Size   int      `json:"size,omitempty"`

	Scroll             string             `json:"scroll,omitempty"`
	ResultTableOptions ResultTableOptions `json:"result_table_options,omitempty"`

	Orders      Orders    `json:"orders,omitempty"`
	NeedAddTime bool      `json:"need_add_time,omitempty"`
	Collapse    *Collapse `json:"collapse,omitempty"`
}

type HighLight struct {
	MaxAnalyzedOffset int  `json:"max_analyzed_offset,omitempty"`
	Enable            bool `json:"enable,omitempty"`
}

type Collapse struct {
	Field string `json:"field,omitempty"`
}

type Order struct {
	Name string
	Ast  bool
}

type Orders []Order

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

	// IsPrefix 是否是前缀匹配
	IsPrefix bool

	// IsSuffix 是否是后缀匹配
	IsSuffix bool
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

type QueryReference map[string][]*QueryMetric

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
func (qMetric *QueryMetric) ToJson(isSort bool) string {
	if isSort {
		sort.SliceStable(qMetric.QueryList, func(i, j int) bool {
			a := qMetric.QueryList[i].TableID
			b := qMetric.QueryList[j].TableID
			return a < b
		})
	}

	s, _ := json.Marshal(qMetric)
	return string(s)
}

// Range 遍历查询列表
func (qRef QueryReference) Range(name string, fn func(qry *Query)) {
	for refName, references := range qRef {
		if name != "" {
			if refName != name {
				continue
			}
		}

		for _, reference := range references {
			if reference == nil {
				continue
			}
			for _, query := range reference.QueryList {
				if query == nil {
					continue
				}

				fn(query)
			}
		}
	}
}

// ToVmExpand 判断是否是直查，如果都是 vm 查询的情况下，则使用直查模式
func (qRef QueryReference) ToVmExpand(_ context.Context) (vmExpand *VmExpand) {
	vmClusterNames := set.New[string]()
	vmResultTable := set.New[string]()
	metricFilterCondition := make(map[string]string)

	for referenceName, references := range qRef {
		if len(references) == 0 {
			continue
		}

		// 因为是直查，reference 还需要承担聚合语法生成，所以 vm 不支持同指标的拼接，所以这里只取第一个 reference
		reference := references[0]
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

func (os Orders) SortSliceList(list []map[string]any) {
	if len(os) == 0 {
		return
	}
	if len(list) == 0 {
		return
	}

	sort.SliceStable(list, func(i, j int) bool {
		for _, o := range os {
			a, _ := list[i][o.Name].(string)
			b, _ := list[j][o.Name].(string)

			if a != b {
				if o.Ast {
					r := a < b
					return r
				} else {
					r := a > b
					return r
				}
			}
		}
		return true
	})
}
