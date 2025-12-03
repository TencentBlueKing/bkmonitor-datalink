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
	"sync"
	"time"

	"github.com/dominikbraun/graph"
	"github.com/pkg/errors"
	pl "github.com/prometheus/prometheus/promql"
	"github.com/spf13/cast"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/query"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
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

		g: g,
	}, nil
}

func (r *model) resources(ctx context.Context) ([]cmdb.Resource, error) {
	rs := make([]cmdb.Resource, 0, len(AllResources()))
	for k := range AllResources() {
		rs = append(rs, k)
	}
	sort.Slice(rs, func(i, j int) bool {
		return rs[i] < rs[j]
	})
	return rs, nil
}

// getIndexMatcher 获取该资源过滤条件
func (r *model) getIndexMatcher(ctx context.Context, resource cmdb.Resource, matcher cmdb.Matcher) (cmdb.Matcher, bool, error) {
	var err error
	indexMatcher := make(cmdb.Matcher)
	index := ResourcesIndex(resource)
	if len(index) == 0 {
		err = fmt.Errorf("index is empty with %+v", resource)
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

	startIndex := -1
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

func (r *model) queryResourceMatcher(ctx context.Context, opt QueryResourceOptions) (source cmdb.Resource, sourceInfo cmdb.Matcher, hitPath []string, target cmdb.Resource, ts []cmdb.MatchersWithTimestamp, err error) {
	user := metadata.GetUser(ctx)

	ctx, span := trace.NewSpan(ctx, "get-resource-indexMatcher")
	defer span.End(&err)

	span.Set("source", user.Source)
	span.Set("username", user.Name)
	span.Set("space-uid", opt.SpaceUid)
	span.Set("startTs", opt.Start)
	span.Set("endTs", opt.End)
	span.Set("step", opt.Step)
	span.Set("source", opt.Source)
	span.Set("target", opt.Target)
	span.Set("index-indexMatcher", opt.IndexMatcher)
	span.Set("target", opt.PathResource)

	opt.IndexMatcher = opt.IndexMatcher.Rename()
	span.Set("query-index-indexMatcher", opt.IndexMatcher)

	if opt.Source == "" {
		opt.Source, err = r.getResourceFromMatch(ctx, opt.IndexMatcher)
		if err != nil {
			err = errors.WithMessage(err, "get resource error")
			return source, sourceInfo, hitPath, target, ts, err
		}
	}

	// 如果 target 为空，则使用 source 作为 target，用于 info 数据展示
	if opt.Target == "" {
		opt.Target = opt.Source
	}

	if opt.SpaceUid == "" {
		err = errors.New("space uid is empty")
		return source, sourceInfo, hitPath, target, ts, err
	}

	if opt.Start == "" || opt.End == "" {
		err = errors.New("timestamp is empty")
		return source, sourceInfo, hitPath, target, ts, err
	}

	span.Set("query-source", opt.Source)

	source = opt.Source
	target = opt.Target
	sourceInfo, _, err = r.getIndexMatcher(ctx, opt.Source, opt.IndexMatcher)
	if err != nil {
		err = errors.WithMessagef(err, "get index matcher error")
		return source, sourceInfo, hitPath, target, ts, err
	}

	paths, err := r.getPaths(ctx, opt.Source, opt.Target, opt.PathResource)
	if err != nil {
		err = errors.WithMessagef(err, "get path error")
		return source, sourceInfo, hitPath, target, ts, err
	}

	span.Set("paths", paths)
	metadata.GetQueryParams(ctx).SetIsSkipK8s(true)

	var errorMessage []string

	for _, path := range paths {
		reqTs, reqErr := r.doRequest(ctx, path, opt)
		if reqErr != nil {
			errorMessage = append(errorMessage, fmt.Sprintf("path [%v] do request error: %s", path, reqErr))
			continue
		}

		hitPath = path
		if len(reqTs) > 0 {
			ts = reqTs
			break
		}
	}

	if len(ts) == 0 {
		metadata.NewMessage(
			metadata.MsgQueryRelation,
			"%s",
			"查询不到数据",
		).Warn(ctx)
	}

	span.Set("hit_path", hitPath)
	return source, sourceInfo, hitPath, target, ts, err
}

type QueryResourceOptions struct {
	LookBackDelta string
	SpaceUid      string
	Step          string
	Start         string
	End           string
	Target        cmdb.Resource
	Source        cmdb.Resource

	IndexMatcher  cmdb.Matcher
	ExpandMatcher cmdb.Matcher

	PathResource []cmdb.Resource

	ExpandShow bool

	Instant bool
}

func (r *model) QueryResourceMatcher(ctx context.Context, lookBackDelta, spaceUid string, timestamp string, target, source cmdb.Resource, indexMatcher, expandMatcher cmdb.Matcher, expandShow bool, pathResource []cmdb.Resource) (cmdb.Resource, cmdb.Matcher, []string, cmdb.Resource, cmdb.Matchers, error) {
	opt := QueryResourceOptions{
		LookBackDelta: lookBackDelta,
		SpaceUid:      spaceUid,
		Start:         timestamp,
		End:           timestamp,
		Source:        source,
		Target:        target,
		IndexMatcher:  indexMatcher,
		ExpandMatcher: expandMatcher,
		PathResource:  pathResource,
		ExpandShow:    expandShow,
		Instant:       true,
	}
	source, sourceInfo, path, target, ret, err := r.queryResourceMatcher(ctx, opt)
	if err != nil {
		return "", nil, path, "", nil, err
	}

	return source, sourceInfo, path, target, shimMatcherWithTimestamp(ret), nil
}

func (r *model) QueryResourceMatcherRange(ctx context.Context, lookBackDelta, spaceUid string, step string, start, end string, target, source cmdb.Resource, indexMatcher, expandMatcher cmdb.Matcher, expandShow bool, pathResource []cmdb.Resource) (cmdb.Resource, cmdb.Matcher, []string, cmdb.Resource, []cmdb.MatchersWithTimestamp, error) {
	opt := QueryResourceOptions{
		LookBackDelta: lookBackDelta,
		SpaceUid:      spaceUid,
		Step:          step,
		Start:         start,
		End:           end,
		Source:        source,
		Target:        target,
		IndexMatcher:  indexMatcher,
		ExpandMatcher: expandMatcher,
		ExpandShow:    expandShow,
		PathResource:  pathResource,
		Instant:       false,
	}
	return r.queryResourceMatcher(ctx, opt)
}

func (r *model) doRequest(ctx context.Context, path []string, opt QueryResourceOptions) ([]cmdb.MatchersWithTimestamp, error) {
	// 按照关联路径遍历查询
	var (
		lookBackDelta time.Duration
		err           error
	)

	ctx, span := trace.NewSpan(ctx, "query-do-request")
	defer span.End(&err)

	indexMatcher, _, err := r.getIndexMatcher(ctx, opt.Source, opt.IndexMatcher)
	if err != nil {
		return nil, errors.WithMessagef(err, "get index indexMatcher error")
	}

	span.Set("query-resource-options", opt)

	if opt.LookBackDelta != "" {
		lookBackDelta, err = time.ParseDuration(opt.LookBackDelta)
		if err != nil {
			return nil, err
		}
	}

	queryMaker := &QueryFactory{
		Path:   path,
		Source: opt.Source,

		Start: opt.Start,
		End:   opt.End,
		Step:  opt.Step,

		IndexMatcher:  indexMatcher,
		ExpandMatcher: opt.ExpandMatcher,

		Target:     opt.Target,
		ExpandShow: opt.ExpandShow,
	}

	queryTs, err := queryMaker.MakeQueryTs()
	if err != nil {
		return nil, err
	}

	queryReference, err := queryTs.ToQueryReference(ctx)
	if err != nil {
		return nil, err
	}

	var instance tsdb.Instance

	qb := metadata.GetQueryParams(ctx)

	if qb.IsDirectQuery() {
		vmExpand := query.ToVmExpand(ctx, queryReference)

		metadata.SetExpand(ctx, vmExpand)
		instance = prometheus.GetTsDbInstance(ctx, &metadata.Query{
			StorageType: metadata.VictoriaMetricsStorageType,
		})
		if instance == nil {
			err = fmt.Errorf("%s storage get error", metadata.VictoriaMetricsStorageType)
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

	var matrix pl.Matrix
	var vector pl.Vector
	if opt.Instant {
		vector, err = instance.DirectQuery(ctx, statement, qb.End)
		matrix = vectorToMatrix(vector)
	} else {
		matrix, _, err = instance.DirectQueryRange(ctx, statement, qb.AlignStart, qb.End, qb.Step)
	}
	if err != nil {
		return nil, fmt.Errorf("instance query error: %s", err)
	}

	if len(matrix) == 0 {
		metadata.NewMessage(
			metadata.MsgQueryRelation,
			"%s",
			"查询不到数据",
		).Warn(ctx)
		return nil, nil
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

// buildTimeGraphFromRelations 从关系路径构建 TimeGraph
func (r *model) buildTimeGraphFromRelations(ctx context.Context, spaceUid string, start, end time.Time, step time.Duration, sourceInfo cmdb.Matcher, relations []cmdb.Relation, lookBackDelta string) (*TimeGraph, error) {
	var err error
	ctx, span := trace.NewSpan(ctx, "build-time-graph-from-relations")
	defer span.End(&err)

	tg := NewTimeGraph()

	var lookBackDeltaDuration time.Duration
	if lookBackDelta != "" {
		lookBackDeltaDuration, err = time.ParseDuration(lookBackDelta)
		if err != nil {
			return nil, err
		}
	}

	var instance tsdb.Instance
	qb := metadata.GetQueryParams(ctx)

	if qb.IsDirectQuery() {
		instance = prometheus.GetTsDbInstance(ctx, &metadata.Query{
			StorageType: metadata.VictoriaMetricsStorageType,
		})
		if instance == nil {
			return nil, fmt.Errorf("%s storage get error", metadata.VictoriaMetricsStorageType)
		}
	} else {
		instance = prometheus.NewInstance(ctx, promql.GlobalEngine, &prometheus.QueryRangeStorage{
			QueryMaxRouting: QueryMaxRouting,
			Timeout:         Timeout,
		}, lookBackDeltaDuration, QueryMaxRouting)
	}

	metadata.GetQueryParams(ctx).SetIsSkipK8s(true)

	for _, relation := range relations {
		ctx = metadata.InitHashID(ctx)

		queryTs, err := tg.MakeQueryTs(ctx, spaceUid, sourceInfo, start, end, step, relation)
		if err != nil {
			return nil, errors.WithMessagef(err, "make query ts error for relation %v", relation)
		}

		queryRef, err := queryTs.ToQueryReference(ctx)
		if err != nil {
			return nil, errors.WithMessagef(err, "to query reference error")
		}
		metadata.SetExpand(ctx, query.ToVmExpand(ctx, queryRef))

		expr, err := queryTs.ToPromExpr(ctx, nil)
		if err != nil {
			return nil, errors.WithMessagef(err, "to prom expr error")
		}
		stmt := expr.String()

		var matrix pl.Matrix
		if qb.IsDirectQuery() {
			// instant 查询
			vector, err := instance.DirectQuery(ctx, stmt, qb.End)
			if err != nil {
				return nil, errors.WithMessagef(err, "direct query error")
			}
			matrix = vectorToMatrix(vector)
		} else {
			// range 查询
			matrix, _, err = instance.DirectQueryRange(ctx, stmt, qb.AlignStart, qb.End, qb.Step)
			if err != nil {
				return nil, errors.WithMessagef(err, "direct query range error")
			}
		}

		// 将查询结果添加到 TimeGraph
		for _, series := range matrix {
			info := make(map[string]string, len(series.Metric))
			for _, m := range series.Metric {
				info[m.Name] = m.Value
			}

			timestamps := make([]int64, len(series.Points))
			for i, point := range series.Points {
				timestamps[i] = point.T
			}

			source, target, _ := relation.Info()
			err = tg.AddTimeRelation(ctx, source, target, info, timestamps...)
			if err != nil {
				return nil, errors.WithMessagef(err, "add time relation error")
			}
		}
	}

	return tg, nil
}

// buildRelationsFromPaths 从路径列表中提取所有关系并去重
func (r *model) buildRelationsFromPaths(paths [][]cmdb.Resource) []cmdb.Relation {
	// 使用 map 去重关系
	relationMap := make(map[cmdb.Relation]struct{})

	for _, path := range paths {
		if len(path) < 2 {
			continue
		}
		// 从路径中提取所有相邻的关系
		for i := 0; i < len(path)-1; i++ {
			relation := cmdb.Relation{
				V: [2]cmdb.Resource{path[i], path[i+1]},
			}
			relationMap[relation] = struct{}{}
		}
	}

	// 转换为切片
	allRelations := make([]cmdb.Relation, 0, len(relationMap))
	for relation := range relationMap {
		allRelations = append(allRelations, relation)
	}

	return allRelations
}

// QueryPathResources 查询指定时间点的路径上的所有资源（instant 查询）
// 参数:
//   - sourceType: 源资源类型
//   - targetTypes: 目标资源类型列表
//   - pathResources: 可选，指定的路径列表（支持多条路径），如果为空则自动查找所有路径
//   - matcher: 节点的匹配条件
//   - ts: 时间戳
func (r *model) QueryPathResources(ctx context.Context, lookBackDelta, spaceUid string, ts string, sourceType cmdb.Resource, targetTypes []cmdb.Resource, pathResources [][]cmdb.Resource, matcher cmdb.Matcher) ([]cmdb.PathResourcesResult, error) {
	var err error
	ctx, span := trace.NewSpan(ctx, "query-path-resources")
	defer span.End(&err)

	span.Set("source_type", sourceType)
	span.Set("target_types", targetTypes)
	span.Set("path_resources", pathResources)
	span.Set("space_uid", spaceUid)
	span.Set("timestamp", ts)

	if spaceUid == "" {
		err = errors.New("space uid is empty")
		return nil, err
	}

	if ts == "" {
		err = errors.New("timestamp is empty")
		return nil, err
	}

	if sourceType == "" {
		err = errors.New("source type is empty")
		return nil, err
	}

	if len(targetTypes) == 0 {
		err = errors.New("target types is empty")
		return nil, err
	}

	// 解析时间戳
	timestamp, err := cast.ToInt64E(ts)
	if err != nil {
		return nil, errors.WithMessagef(err, "parse timestamp error")
	}
	queryTime := time.Unix(timestamp, 0)

	// 1. 确定要查询的路径列表
	var allPaths [][]cmdb.Resource

	if len(pathResources) > 0 {
		// 如果指定了路径，使用指定的路径（支持多条路径）
		for _, path := range pathResources {
			if len(path) >= 2 {
				allPaths = append(allPaths, path)
			}
		}
	} else {
		// 如果没有指定路径，使用 sourceType 和 targetTypes 查找所有路径
		for _, targetType := range targetTypes {
			paths, err := r.getPaths(ctx, sourceType, targetType, nil)
			if err != nil {
				// 如果找不到路径，记录日志但继续处理其他目标类型
				metadata.NewMessage(
					metadata.MsgQueryRelation,
					"找不到路径: %s => %s, error: %v",
					sourceType, targetType, err,
				).Warn(ctx)
				continue
			}
			// 将字符串路径转换为 Resource 路径
			for _, pathStr := range paths {
				path := make([]cmdb.Resource, 0, len(pathStr))
				for _, s := range pathStr {
					path = append(path, cmdb.Resource(s))
				}
				allPaths = append(allPaths, path)
			}
		}
	}

	if len(allPaths) == 0 {
		return nil, errors.New("no paths found")
	}

	// 2. 从所有路径中提取关系并去重
	allRelations := r.buildRelationsFromPaths(allPaths)

	// 3. 构建 TimeGraph（instant 查询，start 和 end 相同）
	step := time.Minute * 5 // 默认步长
	tg, err := r.buildTimeGraphFromRelations(ctx, spaceUid, queryTime, queryTime, step, matcher, allRelations, lookBackDelta)
	if err != nil {
		return nil, errors.WithMessagef(err, "build time graph error")
	}
	defer tg.Clean(ctx)

	// 4. 对每个目标类型调用 FindShortestPath，收集所有结果
	var allResults []cmdb.PathResourcesResult
	for _, targetType := range targetTypes {
		results, err := tg.FindShortestPath(ctx, sourceType, targetType, matcher)
		if err != nil {
			// 如果某个目标类型查找失败，记录日志但继续处理其他目标类型
			metadata.NewMessage(
				metadata.MsgQueryRelation,
				"查找路径失败: %s => %s, error: %v",
				sourceType, targetType, err,
			).Warn(ctx)
			continue
		}

		// 转换为 cmdb.PathResourcesResult
		for _, result := range results {
			allResults = append(allResults, cmdb.PathResourcesResult{
				Timestamp:  result.Timestamp,
				TargetType: result.TargetType,
				Path:       result.Path,
			})
		}
	}

	return allResults, nil
}

// QueryPathResourcesRange 查询指定时间段的路径上的所有资源（query_range 查询）
// 参数:
//   - sourceType: 源资源类型
//   - targetTypes: 目标资源类型列表
//   - pathResources: 可选，指定的路径列表（支持多条路径），如果为空则自动查找所有路径
//   - matcher: 节点的匹配条件
//   - startTs, endTs: 时间范围
//   - step: 查询步长
func (r *model) QueryPathResourcesRange(ctx context.Context, lookBackDelta, spaceUid string, step string, startTs, endTs string, sourceType cmdb.Resource, targetTypes []cmdb.Resource, pathResources [][]cmdb.Resource, matcher cmdb.Matcher) ([]cmdb.PathResourcesResult, error) {
	var err error
	ctx, span := trace.NewSpan(ctx, "query-path-resources-range")
	defer span.End(&err)

	span.Set("source_type", sourceType)
	span.Set("target_types", targetTypes)
	span.Set("path_resources", pathResources)
	span.Set("space_uid", spaceUid)
	span.Set("start_ts", startTs)
	span.Set("end_ts", endTs)
	span.Set("step", step)

	if spaceUid == "" {
		err = errors.New("space uid is empty")
		return nil, err
	}

	if startTs == "" || endTs == "" {
		err = errors.New("timestamp is empty")
		return nil, err
	}

	if sourceType == "" {
		err = errors.New("source type is empty")
		return nil, err
	}

	if len(targetTypes) == 0 {
		err = errors.New("target types is empty")
		return nil, err
	}

	// 解析时间范围
	start, err := cast.ToInt64E(startTs)
	if err != nil {
		return nil, errors.WithMessagef(err, "parse start timestamp error")
	}
	end, err := cast.ToInt64E(endTs)
	if err != nil {
		return nil, errors.WithMessagef(err, "parse end timestamp error")
	}

	startTime := time.Unix(start, 0)
	endTime := time.Unix(end, 0)

	// 解析步长
	stepDuration, err := time.ParseDuration(step)
	if err != nil {
		return nil, errors.WithMessagef(err, "parse step error")
	}

	// 1. 确定要查询的路径列表
	var allPaths [][]cmdb.Resource

	if len(pathResources) > 0 {
		// 如果指定了路径，使用指定的路径（支持多条路径）
		for _, path := range pathResources {
			if len(path) >= 2 {
				allPaths = append(allPaths, path)
			}
		}
	} else {
		// 如果没有指定路径，使用 sourceType 和 targetTypes 查找所有路径
		for _, targetType := range targetTypes {
			paths, err := r.getPaths(ctx, sourceType, targetType, nil)
			if err != nil {
				// 如果找不到路径，记录日志但继续处理其他目标类型
				metadata.NewMessage(
					metadata.MsgQueryRelation,
					"找不到路径: %s => %s, error: %v",
					sourceType, targetType, err,
				).Warn(ctx)
				continue
			}
			// 将字符串路径转换为 Resource 路径
			for _, pathStr := range paths {
				path := make([]cmdb.Resource, 0, len(pathStr))
				for _, s := range pathStr {
					path = append(path, cmdb.Resource(s))
				}
				allPaths = append(allPaths, path)
			}
		}
	}

	if len(allPaths) == 0 {
		return nil, errors.New("no paths found")
	}

	// 2. 从所有路径中提取关系并去重
	allRelations := r.buildRelationsFromPaths(allPaths)

	// 3. 构建 TimeGraph
	tg, err := r.buildTimeGraphFromRelations(ctx, spaceUid, startTime, endTime, stepDuration, matcher, allRelations, lookBackDelta)
	if err != nil {
		return nil, errors.WithMessagef(err, "build time graph error")
	}
	defer tg.Clean(ctx)

	// 4. 对每个目标类型调用 FindShortestPath，收集所有结果
	var allResults []cmdb.PathResourcesResult
	for _, targetType := range targetTypes {
		results, err := tg.FindShortestPath(ctx, sourceType, targetType, matcher)
		if err != nil {
			// 如果某个目标类型查找失败，记录日志但继续处理其他目标类型
			metadata.NewMessage(
				metadata.MsgQueryRelation,
				"查找路径失败: %s => %s, error: %v",
				sourceType, targetType, err,
			).Warn(ctx)
			continue
		}

		// 转换为 cmdb.PathResourcesResult
		for _, result := range results {
			allResults = append(allResults, cmdb.PathResourcesResult{
				Timestamp:  result.Timestamp,
				TargetType: result.TargetType,
				Path:       result.Path,
			})
		}
	}

	return allResults, nil
}
