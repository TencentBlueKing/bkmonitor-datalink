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
	pl "github.com/prometheus/prometheus/promql"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/prometheus"
)

const (
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
		mdl, err = newModel(ctx)
		mtx.Unlock()
	}
	return mdl, err
}

type model struct {
	cfg *Config

	m map[cmdb.Resource]cmdb.Index
	g graph.Graph[string, string]
}

// newModel 初始化
func newModel(ctx context.Context) (*model, error) {
	var (
		err error
		cfg = configData
	)

	// 初始化 graph 存储结构
	g := graph.New(graph.StringHash)

	// 初始化资源 map 配置
	m := make(map[cmdb.Resource]cmdb.Index, len(cfg.Resource))

	// 按照 index 数量倒序，用于判断资源归属
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

func (r *model) getResourceIndex(ctx context.Context, resource cmdb.Resource) (cmdb.Index, error) {
	if r.m == nil {
		return nil, fmt.Errorf("reation m is nil")
	}

	if v, ok := r.m[resource]; ok {
		return v, nil
	} else {
		return nil, fmt.Errorf("resource is empty %s", resource)
	}
}

// getIndexMatcher 获取该资源过滤条件
func (r *model) getIndexMatcher(ctx context.Context, resource cmdb.Resource, matcher cmdb.Matcher) (cmdb.Matcher, bool, error) {
	var err error
	indexMatcher := make(cmdb.Matcher)
	index, err := r.getResourceIndex(ctx, resource)
	if err != nil {
		return indexMatcher, false, err
	}
	allMatch := true
	for _, i := range index {
		if v, ok := matcher[i]; ok {
			indexMatcher[i] = v
		} else {
			allMatch = false
		}
	}

	return indexMatcher, allMatch, nil
}

// getResourceFromMatch 通过查询条件判断归属哪个资源
func (r *model) getResourceFromMatch(ctx context.Context, matcher cmdb.Matcher) (cmdb.Resource, error) {
	for _, resource := range r.cfg.Resource {
		_, allMatch, err := r.getIndexMatcher(ctx, resource.Name, matcher)
		if err != nil {
			return "", err
		}

		if allMatch {
			return resource.Name, nil
		}
	}

	return "", fmt.Errorf("resource is empty with %+v", matcher)
}

func (r *model) checkPath(graphPath []string, pathResource []cmdb.Resource) bool {
	// 路径长度至少要 >= 2
	if len(graphPath) < 2 {
		return false
	}

	// 如果不传则判断为命中
	if len(pathResource) == 0 {
		return true
	}

	// 如果长度为 1，且为空，则直接判断直连路径，长度为 2
	if len(pathResource) == 1 && pathResource[0] == "" && len(graphPath) == 2 {
		return true
	}

	// 如果指定的路径大于需要判断的路径则完全无法命中
	if len(pathResource) > len(graphPath) {
		return false
	}

	var startIndex = -1
	for idx, sp := range graphPath {
		if sp == string(pathResource[0]) {
			startIndex = idx
			break
		}
	}

	if startIndex < 0 {
		return false
	}

	for idx, pr := range pathResource {
		if string(pr) != graphPath[startIndex+idx] {
			return false
		}
	}

	return true
}

func (r *model) getPaths(ctx context.Context, source, target cmdb.Resource, pathResource []cmdb.Resource) ([][]string, error) {
	// 暂时不使用全路径
	allGraphPaths, err := graph.AllPathsBetween(r.g, string(source), string(target))
	if err != nil {
		return nil, err
	}
	// 从最短路径开始验证
	sort.SliceStable(allGraphPaths, func(i, j int) bool {
		return len(allGraphPaths[i]) < len(allGraphPaths[j])
	})

	// 兼容原来的节点屏蔽功能，因为没有指定路径，原路径 pod -> node -> system, 最短路径可能会命中：pod -> apm_service_instance -> system，所以需要多路径查询匹配
	paths := make([][]string, 0)
	for _, p := range allGraphPaths {
		if r.checkPath(p, pathResource) {
			paths = append(paths, p)
		}
	}

	if len(paths) == 0 {
		return nil, fmt.Errorf("empty paths with %s => %s through %v", source, target, pathResource)
	}

	return paths, nil
}

func (r *model) queryResourceMatcher(ctx context.Context, opt QueryResourceOptions) (source cmdb.Resource, matcher cmdb.Matcher, hitPath []string, ts []cmdb.MatchersWithTimestamp, err error) {
	var (
		user = metadata.GetUser(ctx)
	)

	ctx, span := trace.NewSpan(ctx, "get-resource-matcher")
	defer span.End(&err)

	span.Set("source", user.Source)
	span.Set("username", user.Name)
	span.Set("space-uid", opt.SpaceUid)
	span.Set("startTs", opt.StartTs)
	span.Set("endTs", opt.EndTs)
	span.Set("step", opt.Step.String())
	span.Set("source", opt.Source)
	span.Set("target", opt.Target)
	span.Set("matcher", opt.Matcher)
	span.Set("target", opt.PathResource)

	queryMatcher := opt.Matcher.Rename()

	span.Set("query-matcher", queryMatcher)

	if opt.Source == "" {
		opt.Source, err = r.getResourceFromMatch(ctx, queryMatcher)
		if err != nil {
			err = errors.WithMessage(err, "get resource error")
			return
		}
	}

	source = opt.Source
	matcher, _, err = r.getIndexMatcher(ctx, opt.Source, queryMatcher)
	if err != nil {
		err = errors.WithMessagef(err, "get index matcher error")
		return
	}

	if opt.SpaceUid == "" {
		err = errors.New("space uid is empty")
		return
	}

	if opt.StartTs == 0 || opt.EndTs == 0 {
		err = errors.New("timestamp is empty")
		return
	}

	span.Set("source", opt.Source)
	span.Set("index-matcher", matcher)

	paths, err := r.getPaths(ctx, opt.Source, opt.Target, opt.PathResource)
	if err != nil {
		err = errors.WithMessagef(err, "get path error")
		return
	}

	span.Set("paths", paths)

	for _, path := range paths {
		ts, err = r.doRequest(ctx, opt.LookBackDelta, opt.SpaceUid, opt.StartTs, opt.EndTs, opt.Step, path, matcher, opt.Instant)
		if err != nil {
			err = errors.WithMessagef(err, "path [%v] do request error", path)
			continue
		}

		if len(ts) > 0 {
			hitPath = path
			span.Set("hit_path", hitPath)
			break
		}
	}

	// 查询不到数据无需返回异常
	if len(ts) == 0 {
		return
	}

	return
}

type QueryResourceOptions struct {
	LookBackDelta string
	SpaceUid      string
	Step          time.Duration
	StartTs       int64
	EndTs         int64
	Target        cmdb.Resource
	Source        cmdb.Resource
	Matcher       cmdb.Matcher
	PathResource  []cmdb.Resource
	Instant       bool
}

func (r *model) QueryResourceMatcher(ctx context.Context, lookBackDelta, spaceUid string, timestamp int64, target, source cmdb.Resource, matcher cmdb.Matcher, pathResource []cmdb.Resource) (cmdb.Resource, cmdb.Matcher, []string, cmdb.Matchers, error) {
	opt := QueryResourceOptions{
		LookBackDelta: lookBackDelta,
		SpaceUid:      spaceUid,
		Step:          time.Duration(0),
		StartTs:       timestamp,
		EndTs:         timestamp,
		Source:        source,
		Target:        target,
		Matcher:       matcher,
		PathResource:  pathResource,
		Instant:       true,
	}
	resource, matcher, path, ret, err := r.queryResourceMatcher(ctx, opt)
	if err != nil {
		return resource, matcher, path, nil, err
	}

	return resource, matcher, path, shimMatcherWithTimestamp(ret), nil
}

func (r *model) QueryResourceMatcherRange(ctx context.Context, lookBackDelta, spaceUid string, step time.Duration, startTs, endTs int64, target, source cmdb.Resource, matcher cmdb.Matcher, pathResource []cmdb.Resource) (cmdb.Resource, cmdb.Matcher, []string, []cmdb.MatchersWithTimestamp, error) {
	opt := QueryResourceOptions{
		LookBackDelta: lookBackDelta,
		SpaceUid:      spaceUid,
		Step:          step,
		StartTs:       startTs,
		EndTs:         endTs,
		Source:        source,
		Target:        target,
		Matcher:       matcher,
		PathResource:  pathResource,
		Instant:       false,
	}
	return r.queryResourceMatcher(ctx, opt)
}

func (r *model) doRequest(ctx context.Context, lookBackDeltaStr, spaceUid string, startTs, endTs int64, step time.Duration, path []string, matcher map[string]string, instant bool) ([]cmdb.MatchersWithTimestamp, error) {
	// 按照关联路径遍历查询
	var (
		lookBackDelta time.Duration
		err           error
	)

	ctx, span := trace.NewSpan(ctx, "query-do-request")
	defer span.End(&err)

	span.Set("lookBackDeltaStr", lookBackDeltaStr)
	span.Set("spaceUid", spaceUid)
	span.Set("startTs", startTs)
	span.Set("endTs", endTs)
	span.Set("step", step.String())
	span.Set("path", path)
	span.Set("matcher", matcher)
	span.Set("instant", instant)

	if lookBackDeltaStr != "" {
		lookBackDelta, err = time.ParseDuration(lookBackDeltaStr)
		if err != nil {
			return nil, err
		}
	}

	queryTs, err := r.makeQuery(ctx, spaceUid, path, matcher, step)
	if err != nil {
		return nil, err
	}

	metadata.GetQueryParams(ctx).SetIsSkipK8s(true)
	queryReference, err := queryTs.ToQueryReference(ctx)
	if err != nil {
		return nil, err
	}
	metadata.SetQueryReference(ctx, queryReference)

	var instance tsdb.Instance

	if metadata.GetQueryParams(ctx).IsDirectQuery() {
		vmExpand := queryReference.ToVmExpand(ctx)

		metadata.SetExpand(ctx, vmExpand)
		instance = prometheus.GetTsDbInstance(ctx, &metadata.Query{
			StorageType: consul.VictoriaMetricsStorageType,
		})
		if instance == nil {
			err = fmt.Errorf("%s storage get error", consul.VictoriaMetricsStorageType)
			return nil, err
		}
	} else {
		instance = prometheus.NewInstance(ctx, promql.GlobalEngine, &prometheus.QueryRangeStorage{
			QueryMaxRouting: QueryMaxRouting,
			Timeout:         Timeout,
		}, lookBackDelta, QueryMaxRouting)
	}

	realPromQL, err := queryTs.ToPromQL(ctx)
	if err == nil {
		span.Set("promql", realPromQL)
	}

	promQL, err := queryTs.ToPromExpr(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("query ts to prom expr error: %s", err)
	}

	statement := promQL.String()
	start := time.Unix(startTs, 0)
	end := time.Unix(endTs, 0)

	var matrix pl.Matrix
	var vector pl.Vector
	if instant {
		vector, err = instance.DirectQuery(ctx, statement, end)
		matrix = vectorToMatrix(vector)
	} else {
		matrix, err = instance.DirectQueryRange(ctx, statement, start, end, step)
	}
	if err != nil {
		return nil, fmt.Errorf("instance query error: %s", err)
	}

	if len(matrix) == 0 {
		return nil, fmt.Errorf("instance data empty, promql: %s", realPromQL)
	}

	merged := make(map[int64]cmdb.Matchers)
	for _, series := range matrix {
		for _, p := range series.Points {
			lbs := make(cmdb.Matcher, len(series.Metric))
			for _, m := range series.Metric {
				lbs[m.Name] = m.Value
			}
			merged[p.T] = append(merged[p.T], lbs)
		}
	}

	// 按时间戳聚合并排序
	ret := make([]cmdb.MatchersWithTimestamp, 0, len(merged))
	for k, v := range merged {
		ret = append(ret, cmdb.MatchersWithTimestamp{
			Timestamp: k,
			Matchers:  v,
		})
	}

	sort.Slice(ret, func(i, j int) bool {
		return ret[i].Timestamp < ret[j].Timestamp
	})

	return ret, nil
}

func (r *model) makeQuery(ctx context.Context, spaceUid string, path []string, matcher map[string]string, step time.Duration) (*structured.QueryTs, error) {
	const ascii = 97 // a

	queryTs := &structured.QueryTs{
		SpaceUid: spaceUid,
	}

	timeAggregation := structured.TimeAggregation{}
	if step.Seconds() > 0 {
		if step < time.Minute {
			step = time.Minute
		}

		timeAggregation.Function = structured.CountOT
		timeAggregation.Window = structured.Window(step.String())
	}

	cmdbPath, err := pathParser(path)
	if err != nil {
		err = errors.WithMessagef(err, "path parser %s", path)
		return nil, err
	}

	for i, p := range cmdbPath {
		if len(p.V) < 2 {
			return nil, fmt.Errorf("path format is wrong %v", p)
		}

		metric := getMetric(p)
		if metric == "" {
			return nil, fmt.Errorf("metric is empty %v", p)
		}

		sourceIndex, err := r.getResourceIndex(ctx, p.V[0])
		if err != nil {
			return nil, err
		}
		targetIndex, err := r.getResourceIndex(ctx, p.V[1])
		if err != nil {
			return nil, err
		}

		onConnect := strings.Join(sourceIndex, ",")
		groupBy := strings.Join(targetIndex, ",")

		ref := string(rune(ascii + i))

		if i == 0 {
			queryTs.QueryList = append(queryTs.QueryList, &structured.Query{
				TimeAggregation: timeAggregation,
				FieldName:       metric,
				ReferenceName:   ref,
				Conditions:      convertMapToConditions(matcher, sourceIndex, targetIndex),
			})
			queryTs.MetricMerge = fmt.Sprintf(`(count(%s) by (%s))`, ref, groupBy)
		} else {
			// 如果查询条件在其他 relation 中也存在，也需要补充，比如（bcs_cluster_id）
			queryTs.QueryList = append(queryTs.QueryList, &structured.Query{
				TimeAggregation: timeAggregation,
				FieldName:       metric,
				ReferenceName:   ref,
				Conditions:      convertMapToConditions(matcher, sourceIndex, targetIndex),
			})

			queryTs.MetricMerge = fmt.Sprintf(`count(%s and on(%s) %s) by (%s)`, ref, onConnect, queryTs.MetricMerge, groupBy)
		}
	}

	return queryTs, nil
}

func vectorToMatrix(vector pl.Vector) pl.Matrix {
	var matrix pl.Matrix
	for _, sample := range vector {
		matrix = append(matrix, pl.Series{
			Metric: sample.Metric,
			Points: []pl.Point{sample.Point},
		})
	}
	return matrix
}

// shimMatcherWithTimestamp 如果是 instant 查询，则保留一个数据（理论上也只有一个数据）
func shimMatcherWithTimestamp(matchers []cmdb.MatchersWithTimestamp) cmdb.Matchers {
	if len(matchers) == 0 {
		return nil
	}

	pick := matchers[len(matchers)-1]
	return pick.Matchers
}

func getMetric(relation cmdb.Relation) string {
	if len(relation.V) == 2 {
		v := []string{string(relation.V[0]), string(relation.V[1])}
		sort.Strings(v)
		return fmt.Sprintf("%s_relation", strings.Join(v, "_with_"))
	}
	return ""
}

func convertMapToConditions(matcher cmdb.Matcher, sourceIndex, targetIndex cmdb.Index) structured.Conditions {
	cond := structured.Conditions{}

	allIndex := make(map[string]struct{})
	for _, index := range []cmdb.Index{sourceIndex, targetIndex} {
		for _, i := range index {
			allIndex[i] = struct{}{}
		}
	}

	for i := range allIndex {
		// 如果查询条件里面有关键维度，则必须相等，否则必须不为空
		if v, ok := matcher[i]; ok {
			// 为空的条件不加入过滤判断
			if v == "" {
				continue
			}

			cond.FieldList = append(cond.FieldList, structured.ConditionField{
				DimensionName: i,
				Value:         []string{v},
				Operator:      structured.ConditionEqual,
			})
		} else {
			cond.FieldList = append(cond.FieldList, structured.ConditionField{
				DimensionName: i,
				Value:         []string{""},
				Operator:      structured.ConditionNotEqual,
			})
		}
	}

	// 所有条件均为 and 拼接
	for i := 0; i < len(cond.FieldList)-1; i++ {
		cond.ConditionList = append(cond.ConditionList, "and")
	}
	return cond
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
