// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v1beta1

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dominikbraun/graph"
	"github.com/pkg/errors"
	"github.com/spf13/cast"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/set"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

type TimeGraph struct {
	lock sync.RWMutex

	nodeBuilder *NodeBuilder

	timeGraph map[int64]graph.Graph[uint64, uint64]
}

func NewTimeGraph() *TimeGraph {
	return &TimeGraph{
		nodeBuilder: NewNodeBuilder(),
		timeGraph:   make(map[int64]graph.Graph[uint64, uint64]),
	}
}

func (q *TimeGraph) Clean(ctx context.Context) {
	q.lock.Lock()
	defer q.lock.Unlock()

	q.nodeBuilder.Clean()
	q.timeGraph = nil
}

func (q *TimeGraph) Stat() string {
	q.lock.RLock()
	defer q.lock.RUnlock()

	var s strings.Builder
	s.WriteString(fmt.Sprintf("节点总数: %d\n", q.nodeBuilder.Length()))
	for t, g := range q.timeGraph {
		num, _ := g.Size()
		s.WriteString(fmt.Sprintf("时序边数: %d: %d\n", t, num))
	}

	return s.String()
}

func (q *TimeGraph) addNode(ctx context.Context, timestamp int64, ids ...uint64) error {
	for _, id := range ids {
		if q.timeGraph[timestamp] == nil {
			q.timeGraph[timestamp] = graph.New(func(t uint64) uint64 {
				return t
			}, graph.Directed())
		}

		err := q.timeGraph[timestamp].AddVertex(id)
		if err != nil {
			if errors.Is(err, graph.ErrVertexAlreadyExists) {
				continue
			}
			return err
		}
	}

	return nil
}

// GetNodesByResourceType 根据资源类型获取所有节点ID
func (q *TimeGraph) GetNodesByResourceType(resourceType cmdb.Resource) []cmdb.Matcher {
	return q.nodeBuilder.ResourceNodeInfo(resourceType)
}

func (q *TimeGraph) AddTimeRelation(ctx context.Context, source, target cmdb.Resource, info cmdb.Matcher, timestamps ...int64) error {
	if len(info) == 0 {
		return nil
	}
	q.lock.Lock()
	defer q.lock.Unlock()

	sourceNode, err := q.nodeBuilder.GetID(source, info)
	if err != nil {
		return err
	}
	targetNode, err := q.nodeBuilder.GetID(target, info)
	if err != nil {
		return err
	}

	for _, timestamp := range timestamps {
		err := q.addNode(ctx, timestamp, sourceNode, targetNode)
		if err != nil {
			return err
		}

		err = q.timeGraph[timestamp].AddEdge(sourceNode, targetNode)

		sourceType, sourceInfo := q.nodeBuilder.Info(sourceNode)
		targetType, targetInfo := q.nodeBuilder.Info(targetNode)

		log.Infof(ctx, "AddEdge: %s->%s %d %s:%v %s%v\n", source, target, timestamp, sourceType, sourceInfo, targetType, targetInfo)
		if err != nil {
			if errors.Is(err, graph.ErrEdgeAlreadyExists) {
				log.Infof(ctx, "Edge already exists: %s->%s", source, target)
				continue
			}

			return err
		}
	}

	return nil
}

func (q *TimeGraph) MakeQueryTs(ctx context.Context, spaceUID string, info map[string]string, start time.Time, end time.Time, step time.Duration, relation cmdb.Relation) (*structured.QueryTs, error) {
	source, target, metric := relation.Info()
	if metric == "" {
		return nil, nil
	}

	indexSet := set.New[string](ResourcesIndex(source, target)...)
	indexes := indexSet.ToArray()
	sort.Strings(indexes)

	var fieldList []structured.ConditionField
	for _, index := range indexes {
		if v, ok := info[index]; ok {
			fieldList = append(fieldList, structured.ConditionField{
				DimensionName: index,
				Value:         []string{v},
				Operator:      structured.ConditionEqual,
			})
		} else {
			fieldList = append(fieldList, structured.ConditionField{
				DimensionName: index,
				Value:         []string{""},
				Operator:      structured.ConditionNotEqual,
			})
		}
	}

	dimensions := indexSet.ToArray()
	sort.Strings(dimensions)

	var conditionList []string
	for i := 1; i < len(fieldList); i++ {
		conditionList = append(conditionList, structured.ConditionAnd)
	}

	query := &structured.Query{
		FieldName: metric,
		TimeAggregation: structured.TimeAggregation{
			Function: structured.CountOT,
			Window:   structured.Window(step.String()),
		},
		AggregateMethodList: structured.AggregateMethodList{
			{
				Method:     structured.COUNT,
				Dimensions: dimensions,
			},
		},
		Conditions: structured.Conditions{
			FieldList:     fieldList,
			ConditionList: conditionList,
		},
		ReferenceName: metadata.DefaultReferenceName,
	}

	return &structured.QueryTs{
		SpaceUid:    spaceUID,
		QueryList:   []*structured.Query{query},
		MetricMerge: metadata.DefaultReferenceName,
		Start:       cast.ToString(start.Unix()),
		End:         cast.ToString(end.Unix()),
		Step:        step.String(),
	}, nil
}
