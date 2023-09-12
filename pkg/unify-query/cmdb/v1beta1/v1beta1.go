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
	oleltrace "go.opentelemetry.io/otel/trace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/prometheus"
)

const (
	ReferenceName   = "a"
	QueryMaxRouting = 2
	Timeout         = time.Minute
)

var (
	mdl *model
	mtx sync.Mutex
)

func GetModel(ctx context.Context) (cmdb.CMDB, error) {
	var err error
	if mdl == nil {
		mtx.Lock()
		mdl, err = NewModel(ctx)
		mtx.Unlock()
	}
	return mdl, err
}

// NewModel 初始化
func NewModel(ctx context.Context) (*model, error) {
	var (
		err error
		cfg = configData
	)

	// 初始化 graph 存储结构
	g := graph.New(graph.StringHash)

	// 初始化资源 map 配置
	m := make(map[cmdb.Resource]cmdb.Index, len(cfg.Resource))

	// 按照 index 数量倒序，用于判断资源
	sort.SliceStable(cfg.Resource, func(i, j int) bool {
		return len(cfg.Resource[i].Index) > len(cfg.Resource[j].Index)
	})

	for _, r := range cfg.Resource {
		m[r.Name] = r.Index

		if err = g.AddVertex(string(r.Name)); err != nil {
			return nil, fmt.Errorf("add vertex error: %s", err.Error())
		}
	}
	for _, r := range cfg.Relation {
		if len(r.Resources) != 2 {
			return nil, fmt.Errorf("wrong model %+v", r.Resources)
		}

		if err = g.AddEdge(string(r.Resources[0]), string(r.Resources[1])); err != nil {
			return nil, fmt.Errorf("add edge error: %s", err.Error())
		}
	}

	return &model{
		cfg: cfg,

		m: m,
		g: g,
	}, nil
}

type model struct {
	cfg *Config

	m map[cmdb.Resource]cmdb.Index
	g graph.Graph[string, string]
}

func (r *model) resources(ctx context.Context) ([]cmdb.Resource, error) {
	rs := make([]cmdb.Resource, 0, len(r.m))
	for k := range r.m {
		rs = append(rs, k)
	}
	sort.Slice(rs, func(i, j int) bool {
		return rs[i] < rs[j]
	})
	return rs, nil
}

func (r *model) getResource(ctx context.Context, resource cmdb.Resource) (cmdb.Index, error) {
	if r.m == nil {
		return nil, fmt.Errorf("reation m is nil")
	}

	if v, ok := r.m[resource]; ok {
		return v, nil
	} else {
		return nil, fmt.Errorf("resource is empty %s", resource)
	}
}

func (r *model) getResourceFromMatch(ctx context.Context, matcher cmdb.Matcher) (cmdb.Resource, cmdb.Matcher, error) {
	for _, resource := range r.cfg.Resource {
		if indexMatcher := indexInMather(ctx, resource.Index, matcher); indexMatcher != nil {
			return resource.Name, indexMatcher, nil
		}
	}
	return "", nil, fmt.Errorf("empty resource with %+v", matcher)
}

func (r *model) getPaths(ctx context.Context, source, target cmdb.Resource, matcher cmdb.Matcher) (cmdb.Paths, error) {
	// 获取最短路径
	p, err := graph.ShortestPath(r.g, string(source), string(target))
	if err != nil {
		return nil, fmt.Errorf("%s => %s error: %s", source, target, err)
	}
	path, err := pathParser(p)
	if err != nil {
		return nil, fmt.Errorf("path parser %v error: %s", p, err)
	}
	return cmdb.Paths{path}, nil

	// 暂时不使用全路径
	//allGraphPaths, err := graph.AllPathsBetween(r.g, string(source), string(target))
	//if err != nil {
	//	return nil, err
	//}
	//// 从最短路径开始验证
	//sort.SliceStable(allGraphPaths, func(i, j int) bool {
	//	return len(allGraphPaths[i]) < len(allGraphPaths[j])
	//})
	//
	//allPaths := make(cmdb.Paths, 0, len(allGraphPaths))
	//for _, p := range allGraphPaths {
	//	paths, err := pathParser(p)
	//	if err != nil {
	//		continue
	//	}
	//	allPaths = append(allPaths, paths)
	//}
	//return allPaths, nil
}

func (r *model) GetResourceMatcher(ctx context.Context, lookBackDelta, spaceUid string, timestamp int64, target cmdb.Resource, matcher cmdb.Matcher) (cmdb.Resource, cmdb.Matcher, cmdb.Matchers, error) {
	var (
		span oleltrace.Span
		user = metadata.GetUser(ctx)

		resultMatchers cmdb.Matchers
	)

	ctx, span = trace.IntoContext(ctx, trace.TracerName, "get-resource-matcher")
	if span != nil {
		defer span.End()
	}

	trace.InsertStringIntoSpan("source", user.Source, span)
	trace.InsertStringIntoSpan("username", user.Name, span)
	trace.InsertStringIntoSpan("space-uid", spaceUid, span)
	trace.InsertIntIntoSpan("timestamp", int(timestamp), span)
	trace.InsertStringIntoSpan("target", string(target), span)
	trace.InsertStringIntoSpan("matcher", fmt.Sprintf("%v", matcher), span)

	queryMatcher := matcher.Rename()

	trace.InsertStringIntoSpan("query-matcher", fmt.Sprintf("%v", queryMatcher), span)

	source, indexMatcher, err := r.getResourceFromMatch(ctx, queryMatcher)
	if err != nil {
		return source, indexMatcher, resultMatchers, fmt.Errorf("get resource error: %s", err)
	}

	if spaceUid == "" {
		return source, indexMatcher, resultMatchers, fmt.Errorf("space uid is empty")
	}

	if timestamp == 0 {
		return source, indexMatcher, resultMatchers, fmt.Errorf("timestamp is empty")
	}

	trace.InsertStringIntoSpan("source", string(source), span)
	trace.InsertStringIntoSpan("index-matcher", fmt.Sprintf("%v", indexMatcher), span)

	paths, err := r.getPaths(ctx, source, target, queryMatcher)
	if err != nil {
		return source, indexMatcher, resultMatchers, fmt.Errorf("get paths error: %s", err)
	}

	trace.InsertStringIntoSpan("paths", fmt.Sprintf("%v", paths), span)

	for _, path := range paths {
		resultMatchers, err = r.getDataWithMatchers(ctx, lookBackDelta, spaceUid, timestamp, path, indexMatcher)
		if err != nil {
			continue
		}
		trace.InsertStringIntoSpan("path", fmt.Sprintf("%v", path), span)
	}

	trace.InsertStringIntoSpan("result-matchers", fmt.Sprintf("%v", resultMatchers), span)

	return source, indexMatcher, resultMatchers, err
}

func (r *model) getDataWithMatchers(ctx context.Context, lookBackDeltaStr, spaceUid string, timestamp int64, path cmdb.Path, matchers ...cmdb.Matcher) (cmdb.Matchers, error) {
	// 按照关联路径遍历查询
	var (
		lookBackDelta time.Duration
		err           error
		indexMatchers = matchers
	)
	if lookBackDeltaStr != "" {
		lookBackDelta, err = time.ParseDuration(lookBackDeltaStr)
		if err != nil {
			return indexMatchers, err
		}
	}

	for _, p := range path {
		if len(p.V) < 2 {
			return indexMatchers, fmt.Errorf("path format is wrong %v", p)
		}

		metric := getMetric(p)
		if metric == "" {
			return indexMatchers, fmt.Errorf("metric is empty %v", p)
		}

		queryTs := &structured.QueryTs{
			SpaceUid: spaceUid,
			QueryList: []*structured.Query{
				{
					FieldName:     metric,
					ReferenceName: ReferenceName,
				},
			},
			MetricMerge: ReferenceName,
		}

		queryReference, err := queryTs.ToQueryReference(ctx)
		if err != nil {
			return indexMatchers, err
		}

		condition := getConditions(false, indexMatchers...)
		vmCondition := getConditions(true, indexMatchers...)

		for _, qm := range queryReference {
			for _, ql := range qm.QueryList {
				ql.Condition = condition
				ql.VmCondition = vmCondition
			}
		}

		metadata.SetQueryReference(ctx, queryReference)
		instance := prometheus.NewInstance(ctx, promql.GlobalEngine, &prometheus.QueryRangeStorage{
			QueryMaxRouting: QueryMaxRouting,
			Timeout:         Timeout,
		}, lookBackDelta)

		end := time.Unix(timestamp, 0)
		promQL, err := queryTs.ToPromExpr(ctx, true, false)
		if err != nil {
			return nil, fmt.Errorf("query ts to prom expr error: %s", err)
		}
		res, err := instance.Query(ctx, promQL.String(), end)
		if err != nil {
			return nil, fmt.Errorf("instance query error: %s", err)
		}

		if len(res) == 0 {
			return nil, fmt.Errorf("instance query empty, metric: %s, indexMatcher: %+v", metric, indexMatchers)
		}

		indexMatchers = make(cmdb.Matchers, 0, len(res))
		for _, rs := range res {
			matcher := make(cmdb.Matcher, len(rs.Metric))
			for _, m := range rs.Metric {
				matcher[m.Name] = m.Value
			}
			matcher, err = r.getIndexMatcher(ctx, p.V[1], matcher)
			if err != nil {
				return nil, fmt.Errorf("get index matcher error: %s", err)
			}
			indexMatchers = append(indexMatchers, matcher)
		}
	}

	return indexMatchers, nil
}

func (r *model) getIndexMatcher(ctx context.Context, resource cmdb.Resource, matcher cmdb.Matcher) (cmdb.Matcher, error) {
	index, err := r.getResource(ctx, resource)
	if len(index) == 0 {
		return nil, fmt.Errorf("resource %s get index empty error %s", resource, err)
	}

	indexMatcher := make(cmdb.Matcher, len(index))
	for _, idx := range index {
		if v, ok := matcher[idx]; ok {
			indexMatcher[idx] = v
		} else {
			return nil, fmt.Errorf("matcher %v have not key %s", matcher, idx)
		}
	}
	return indexMatcher, nil
}

func getConditions(vm bool, matchers ...cmdb.Matcher) string {
	condition := make([][]promql.ConditionField, 0, len(matchers))
	for _, m := range matchers {
		conditionField := make([]promql.ConditionField, 0, len(m))
		for k, v := range m {
			conditionField = append(conditionField, promql.ConditionField{
				DimensionName: k,
				Value:         []string{v},
				Operator:      promql.EqualOperator,
			})
		}
		condition = append(condition, conditionField)
	}
	return promql.MakeOrExpression(condition, vm)
}

func getMetric(relation cmdb.Relation) string {
	if len(relation.V) == 2 {
		v := []string{string(relation.V[0]), string(relation.V[1])}
		sort.Strings(v)
		return fmt.Sprintf("%s_relation", strings.Join(v, "_with_"))
	}
	return ""
}

func pathParser(p []string) (cmdb.Path, error) {
	if len(p) < 2 {
		return nil, fmt.Errorf("path format is wrong %s", p)
	}

	path := make(cmdb.Path, 0, len(p)-1)
	for i := 0; i < len(p)-1; i++ {
		v := []cmdb.Resource{cmdb.Resource(p[i]), cmdb.Resource(p[i+1])}
		path = append(path, cmdb.Relation{
			V: v,
		})
	}
	return path, nil
}

func indexInMather(ctx context.Context, index cmdb.Index, matcher cmdb.Matcher) cmdb.Matcher {
	indexMatcher := make(cmdb.Matcher)
	for _, i := range index {
		if v, ok := matcher[i]; ok {
			indexMatcher[i] = v
		} else {
			return nil
		}
	}

	return indexMatcher
}
