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
	"fmt"
	"math"
	"time"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

// QueryPromQL promql 查询结构体
type QueryPromQL struct {
	PromQL              string   `json:"promql"`
	Start               string   `json:"start"`
	End                 string   `json:"end"`
	Step                string   `json:"step"`
	BKBizIDs            []string `json:"bk_biz_ids"`
	MaxSourceResolution string   `json:"max_source_resolution,omitempty"`
	NotAlignInfluxdb    bool     `json:"not_align_influxdb,omitempty"` // 不与influxdb对齐
	Limit               int      `json:"limit,omitempty"`
	Slimit              int      `json:"slimit,omitempty"`
	Match               string   `json:"match,omitempty"`
	// Timezone 时区
	Timezone string `json:"timezone,omitempty" example:"Asia/Shanghai"`
	// LookBackDelta 偏移量
	LookBackDelta string `json:"look_back_delta"`
	// 瞬时数据
	Instant bool `json:"instant"`
}

// queryPromQLExpr
type queryPromQLExpr struct {
	q string
	//promql     []string
	promqlByte []byte
	ref        *refMgr
	expr       parser.Expr
	nodes      []parser.Node
	vecIdx     []int
	vecGroups  []vecGroup
}

// NewQueryPromQLExpr
func NewQueryPromQLExpr(q string) *queryPromQLExpr {
	return &queryPromQLExpr{q: q, ref: &refMgr{char: "abcdefghijklmnopqrstuvwxyz"}}
}

// inspect 使用深度优先遍历语法树 并记录 VectorSelector 的索引位置
// 每个 VectorSelector 可以看做是一棵树的不为空的叶子节点
func (sp *queryPromQLExpr) inspect() {
	parser.Inspect(sp.expr, func(node parser.Node, _ []parser.Node) error {
		if node != nil {
			// 记录所有非空 Node 用于后面分析 Vector 分组情况
			sp.nodes = append(sp.nodes, node)
			idx := len(sp.nodes) - 1

			if _, ok := node.(*parser.VectorSelector); ok {
				sp.vecIdx = append(sp.vecIdx, idx)
			}
		}
		return nil
	})
}

// isGroupMember
func (sp *queryPromQLExpr) isGroupMember(node parser.Node) bool {
	switch node.(type) {
	case *parser.VectorSelector:
		return true
	case *parser.Call:
		return true
	case *parser.AggregateExpr:
		return true
	case *parser.SubqueryExpr:
		return true
	case *parser.MatrixSelector:
		return true
	default:
		return false
	}
}

// splitVecGroups 切分 vectorSelector 分组 即叶子节点的上层被哪些 Node 嵌套住
func (sp *queryPromQLExpr) splitVecGroups() error {
	vecGroups := make([]vecGroup, 0)
	preVec := 0

	for _, idx := range sp.vecIdx {
		group := make([]parser.Node, 0)
		start, end := math.MaxInt64, math.MinInt64

		// [idx: preVec] 的闭区间
		for j := idx; j >= preVec; j-- {
			node := sp.nodes[j]
			// 如果是二元操作符的话就不做进一步解析了
			if _, ok := node.(*parser.BinaryExpr); ok {
				break
			}

			// 只有部分的 Node 类型会属于一个 VectorSelector 分组
			if sp.isGroupMember(node) {
				if int(node.PositionRange().Start) > start || int(node.PositionRange().End) < end {
					continue
				}
			} else {
				continue
			}

			// 记录整个分组的 PositionRange
			if int(node.PositionRange().Start) < start {
				start = int(node.PositionRange().Start)
			}
			if int(node.PositionRange().End) > end {
				end = int(node.PositionRange().End)
			}

			var l []int
			var r []int
			for i := node.PositionRange().Start; i < node.PositionRange().End; i++ {
				if sp.q[i] == '(' {
					l = append(l, int(i))
				}
				if sp.q[i] == ')' {
					r = append(r, int(i))
				}
			}

			if len(r) > 0 && len(l) > 0 && len(r) != len(l) {
				if len(r) > len(l) {
					end = r[len(l)-1] + 1
				} else {
					start = l[len(r)-1]
				}
			}

			group = append(group, node)
		}

		preVec = idx + 1
		vecGroups = append(vecGroups, vecGroup{
			ID:       sp.ref.Next(),
			Nodes:    group,
			StartPos: start,
			EndPos:   end,
		})
	}

	sp.vecGroups = vecGroups
	return nil
}

func (sp *queryPromQLExpr) QueryTs() (*QueryTs, error) {
	var err error
	sp.expr, err = parser.ParseExpr(sp.q)
	if err != nil {
		return &QueryTs{}, err
	}

	//sp.promql = strings.Split(sp.q, "")
	sp.promqlByte = []byte(sp.q)

	sp.inspect()
	return sp.queryTs()
}

// parse promql to struct
func (sp *queryPromQLExpr) queryTs() (*QueryTs, error) {
	err := sp.splitVecGroups()
	if err != nil {
		return &QueryTs{}, err
	}
	queryList := make([]*Query, 0)

	for _, group := range sp.vecGroups {
		query := &Query{}
		var (
			window     time.Duration
			isSubQuery bool
			step       string
		)
		for nodeIndex, node := range group.Nodes {
			switch e := node.(type) {
			// 一个 vecGroup 里有且仅有一个 *parser.VectorSelector Node
			case *parser.VectorSelector:
				query, err = vectorQuery(e, query, group.ID)
				if err != nil {
					return &QueryTs{}, err
				}
			case *parser.MatrixSelector:
				window = e.Range
			case *parser.SubqueryExpr:
				window = e.Range
				step = e.Step.String()
				isSubQuery = true

				query.Offset = e.Offset.String()
				var offset string
				if e.OriginalOffset < 0 {
					offset = (-e.OriginalOffset).String()
					query.OffsetForward = true
				} else {
					offset = e.OriginalOffset.String()
				}
				// 无 offset 偏移就置空 不用赋值了
				if offset != "0s" {
					query.Offset = offset
				}

				// @-modifier
				query.Timestamp = e.Timestamp
				query.StartOrEnd = e.StartOrEnd
				query.VectorOffset = e.Offset
			case *parser.Call:
				// 判断是否存在 matrix，是则写入到 timeAggregation
				var (
					callType parser.ValueType
					position int
				)

				// 获取指标的位置
				for index, argType := range e.Func.ArgTypes {
					switch argType {
					// matrix 或者 vector 类型则获取他的位置，以及记录类型
					case parser.ValueTypeMatrix, parser.ValueTypeVector:
						callType = argType
						position = index
					}
				}

				// 获取参数，参数的长度有可能会大于等于 argTypes，所以要单独循环获取
				vargsList := make([]interface{}, 0)
				for _, arg := range e.Args {
					// 其他类型判断是否是常量参数，是则加入到函数参数列表里面
					switch at := arg.(type) {
					case *parser.NumberLiteral:
						vargsList = append(vargsList, at.Val)
					case *parser.StringLiteral:
						vargsList = append(vargsList, at.Val)
					default:
						continue
					}
				}

				if callType == parser.ValueTypeMatrix {
					if window == 0 {
						return &QueryTs{}, fmt.Errorf("%s is not matrix type, because window is %d", e.Func.Name, window)
					}

					timeAggregation := TimeAggregation{
						Function:   e.Func.Name,
						Window:     Window(window.String()),
						Position:   position,
						IsSubQuery: isSubQuery,

						// 节点位置，用于还原 promql 的定位
						NodeIndex: nodeIndex,
						Step:      step,
					}
					if len(vargsList) > 0 {
						timeAggregation.VargsList = vargsList
					}

					// 如果是 matrix 类型，则需要写入到 timeAggregation 里
					query.TimeAggregation = timeAggregation
				} else {
					// 如果是 vector 类型，则需要写入到 aggregateMethodList 里
					aggregateMethod := AggregateMethod{
						Method:   e.Func.Name,
						Position: position,
					}
					if len(vargsList) > 0 {
						aggregateMethod.VArgsList = vargsList
					}
					query.AggregateMethodList = append(query.AggregateMethodList, aggregateMethod)
				}
			case *parser.AggregateExpr:
				method := convertMethod(e.Op)
				if method == "" {
					return &QueryTs{}, fmt.Errorf("aggregate expr op: %d is not exist", e.Op)
				}
				vargsList := make([]interface{}, 1)
				aggregateMethod := AggregateMethod{
					Method:     method,
					Dimensions: e.Grouping,
					Without:    e.Without,
				}

				// 获取参数
				if e.Param != nil {
					switch v := e.Param.(type) {
					case *parser.NumberLiteral:
						vargsList[0] = v.Val
					case *parser.StringLiteral:
						vargsList[0] = v.Val
					default:
						return &QueryTs{}, fmt.Errorf("aggregate expr type: %T is not exists", v)
					}
					aggregateMethod.VArgsList = vargsList
				}

				query.AggregateMethodList = append(query.AggregateMethodList, aggregateMethod)
			}
		}

		queryList = append(queryList, query)
	}

	var (
		metricMerge []byte
		start       = 0
		end         = len(sp.promqlByte)
	)
	for _, vec := range sp.vecGroups {
		left := sp.promqlByte[start:vec.StartPos]

		metricMerge = append(metricMerge, left...)
		metricMerge = append(metricMerge, vec.ID...)
		start = vec.EndPos
	}
	if end > start {
		metricMerge = append(metricMerge, sp.promqlByte[start:end]...)
	}

	ret := &QueryTs{
		QueryList:   queryList,
		MetricMerge: string(metricMerge),
	}

	return ret, nil
}

// vectorQuery
func vectorQuery(
	e *parser.VectorSelector, query *Query, referenceName string,
) (*Query, error) {
	if e == nil {
		return query, nil
	}
	if query == nil {
		query = new(Query)
	}
	conds := make([]ConditionField, 0)
	route, err := MakeRouteFromLBMatchOrMetricName(e.LabelMatchers)
	if err != nil {
		return query, err
	}

	for _, label := range e.LabelMatchers {
		if label.Name == labels.MetricName {
			if label.Type == labels.MatchRegexp {
				query.IsRegexp = true
			}
			continue
		}

		// bk_database, bk_measurement 2个系统 label 需要过滤
		if label.Name == bkDatabaseLabelName || label.Name == bkMeasurementLabelName {
			continue
		}

		cond := ConditionField{
			DimensionName: label.Name,
			Value:         []string{label.Value},
		}

		op := convertOp(label.Type)
		if op == "" {
			return query, fmt.Errorf("failed to decode the '%s' operation symbol", label.Type)
		}
		cond.Operator = op
		conds = append(conds, cond)
	}

	// 匹配 Conditions 组合条件
	cl := make([]string, 0)
	for i := 0; i < len(conds)-1; i++ {
		cl = append(cl, "and")
	}

	query.DataSource = route.DataSource()
	query.TableID = route.TableID()
	query.ReferenceName = referenceName
	query.FieldName = route.MetricName()
	query.Conditions = Conditions{
		FieldList:     conds,
		ConditionList: cl,
	}

	var offset string
	if e.OriginalOffset < 0 {
		offset = (-e.OriginalOffset).String()
		query.OffsetForward = true
	} else {
		offset = e.OriginalOffset.String()
	}
	// 无 offset 偏移就置空 不用赋值了
	if offset != "0s" {
		query.Offset = offset
	}

	// @-modifier
	query.Timestamp = e.Timestamp
	query.StartOrEnd = e.StartOrEnd
	query.VectorOffset = e.Offset

	return query, nil
}
