// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
	"time"

	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

const hopFetchStart int64 = 1700000000000

func hopFetchFrames() []int64 {
	return []int64{hopFetchStart, hopFetchStart + 60000, hopFetchStart + 120000, hopFetchStart + 180000}
}

// hopFetchProvider 是容器根场景的缩小版：静态双向、静态单向、动态跨类型与动态自关联，
// 容器、Pod 与节点使用联合主键并共享集群、Pod 标签。
func hopFetchProvider() contractSchemaProvider {
	primary := map[ResourceType][]string{
		"container": {"cluster", "pod", "name"},
		"pod":       {"cluster", "pod"},
		"node":      {"cluster", "node"},
		"version":   {"version"},
		"service":   {"svc"},
	}
	fields := map[ResourceType][]string{}
	for resource, keys := range primary {
		fields[resource] = append([]string(nil), keys...)
	}
	return contractSchemaProvider{
		resources: []ResourceType{"container", "pod", "node", "version", "service"},
		primary:   primary,
		fields:    fields,
		schemas: []RelationSchema{
			{RelationType: "container_with_pod", Category: RelationCategoryStatic, FromType: "container", ToType: "pod", MetricName: "container_with_pod"},
			{RelationType: "node_with_pod", Category: RelationCategoryStatic, FromType: "node", ToType: "pod", MetricName: "node_with_pod"},
			{RelationType: "version_with_container", Category: RelationCategoryStatic, FromType: "version", ToType: "container", IsDirectional: true, MetricName: "version_with_container"},
			{RelationType: "pod_to_service", Category: RelationCategoryDynamic, FromType: "pod", ToType: "service", IsDirectional: true, MetricName: "pod_to_service"},
			{RelationType: "service_calls", Category: RelationCategoryDynamic, FromType: "service", ToType: "service", IsDirectional: true, MetricName: "service_calls"},
		},
	}
}

// hopFetchFixture 按指标收集互不重复的序列。
type hopFetchFixture struct {
	data map[string]pl.Matrix
	seen map[string]bool
}

func newHopFetchFixture() *hopFetchFixture {
	return &hopFetchFixture{data: map[string]pl.Matrix{}, seen: map[string]bool{}}
}

func (f *hopFetchFixture) add(metric string, labels map[string]string, times ...int64) {
	pairs := make([]string, 0, len(labels))
	for name, value := range labels {
		pairs = append(pairs, name+"="+value)
	}
	sort.Strings(pairs)
	key := metric + "{" + strings.Join(pairs, ",") + "}"
	if len(times) == 0 || f.seen[key] {
		return
	}
	f.seen[key] = true
	f.data[metric] = append(f.data[metric], contractMatrix(labels, times...)...)
}

type hopFetchCall struct {
	metric     string
	conditions structured.Conditions
}

// hopFetchBackend 像存储一样执行下推条件（含 Contains）并按请求网格返回样本；
// limit>0 时序列数超过 limit 的响应按单次响应过大拒收。calls 记录每次物理读取。
type hopFetchBackend struct {
	t        *testing.T
	data     map[string]pl.Matrix
	limit    int
	calls    []hopFetchCall
	rejected int
	probes   int
}

func (b *hopFetchBackend) query(_ context.Context, ts *structured.QueryTs, _ string, _ bool, start, end time.Time, step time.Duration) (pl.Matrix, bool, error) {
	query := ts.QueryList[0]
	if frontierProbeForTest(ts) {
		b.probes++
		var matched pl.Matrix
		for _, series := range b.data[query.FieldName] {
			labels := make(map[string]string, len(series.Metric))
			for _, label := range series.Metric {
				labels[label.Name] = label.Value
			}
			if wideFrontierMatches(b.t, query.Conditions, labels) {
				matched = append(matched, series)
			}
		}
		return frontierProbeAnswer(ts, matched), false, nil
	}
	b.calls = append(b.calls, hopFetchCall{metric: query.FieldName, conditions: cloneContractConditions(query.Conditions)})
	var matrix pl.Matrix
	for _, series := range b.data[query.FieldName] {
		labels := make(map[string]string, len(series.Metric))
		for _, label := range series.Metric {
			labels[label.Name] = label.Value
		}
		if !wideFrontierMatches(b.t, query.Conditions, labels) {
			continue
		}
		kept := pl.Series{Metric: series.Metric}
		for _, point := range series.Points {
			if point.T < start.UnixMilli() || point.T > end.UnixMilli() {
				continue
			}
			if step > 0 && (point.T-start.UnixMilli())%step.Milliseconds() != 0 {
				continue
			}
			kept.Points = append(kept.Points, point)
		}
		if len(kept.Points) > 0 {
			matrix = append(matrix, kept)
		}
	}
	if b.limit > 0 && len(matrix) > b.limit {
		b.rejected++
		return nil, false, metadata.ErrBackendResponseTooLarge
	}
	return matrix, false, nil
}

// hopFetchNarrowed 表示读取至少有一个字段按取值收窄，不是整条读取关系。
func hopFetchNarrowed(conditions structured.Conditions) bool {
	for _, field := range conditions.FieldList {
		if field.Operator == structured.ConditionEqual || field.Operator == structured.ConditionContains {
			return true
		}
	}
	return false
}

func hopFetchModel(provider contractSchemaProvider, backend *hopFetchBackend) *Model {
	return &Model{schemaProvider: provider, timeGraphQueryReference: selectorQueryReference, timeGraphVMQueryWithPartial: backend.query}
}

// queryHopFetchTopology 用同一后端分别取快照和紧凑格式，返回快照、按身份规范化的快照，
// 以及紧凑格式还原后的快照加上版本数与出现次数。
func queryHopFetchTopology(t *testing.T, provider contractSchemaProvider, backend *hopFetchBackend, request cmdb.SharedTopologyQuery) (cmdb.SharedTopologyResult, []string, []string) {
	t.Helper()
	model := hopFetchModel(provider, backend)
	request.ResponseFormat = ""
	result, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
	require.NoError(t, err)
	request.ResponseFormat = cmdb.CompactTopologyFormat
	compact, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
	require.NoError(t, err)
	require.NotNil(t, compact.Compact)
	summary := append(canonicalTopologyForTest(t, decodeCompactForTest(t, compact.Compact)), fmt.Sprintf("node-versions=%d edge-versions=%d node-occurrences=%d edge-occurrences=%d",
		len(compact.Compact.Nodes), len(compact.Compact.Edges), compact.Compact.NodeOccurrences, compact.Compact.EdgeOccurrences))
	return result, canonicalTopologyForTest(t, result.Snapshots), summary
}

func randomHopFetchFixture(rng *rand.Rand, frames []int64) map[string]pl.Matrix {
	fixture := newHopFetchFixture()
	// 每条边只在部分评估点出现，至少一个。
	at := func() []int64 {
		var times []int64
		for _, frame := range frames {
			if rng.Intn(3) > 0 {
				times = append(times, frame)
			}
		}
		if len(times) == 0 {
			times = append(times, frames[rng.Intn(len(frames))])
		}
		return times
	}
	service := func() string { return fmt.Sprintf("s%d", rng.Intn(4)) }
	for _, cluster := range []string{"c0", "c1"} {
		for p := 0; p < 4; p++ {
			pod := fmt.Sprintf("p%d", p)
			for c := 0; c < 1+rng.Intn(3); c++ {
				name := fmt.Sprintf("n%d", c)
				fixture.add("container_with_pod", map[string]string{"cluster": cluster, "pod": pod, "name": name}, at()...)
				if rng.Intn(2) == 0 {
					fixture.add("version_with_container", map[string]string{"version": fmt.Sprintf("v%d", rng.Intn(3)), "cluster": cluster, "pod": pod, "name": name}, at()...)
				}
			}
			// 同一 Pod 可能在窗口内换节点。
			fixture.add("node_with_pod", map[string]string{"cluster": cluster, "node": fmt.Sprintf("node%d", rng.Intn(2)), "pod": pod}, at()...)
			if rng.Intn(3) == 0 {
				fixture.add("node_with_pod", map[string]string{"cluster": cluster, "node": fmt.Sprintf("node%d", rng.Intn(2)), "pod": pod}, at()...)
			}
			if rng.Intn(2) == 0 {
				fixture.add("pod_to_service", map[string]string{"from_cluster": cluster, "from_pod": pod, "to_svc": service()}, at()...)
			}
		}
	}
	for i := 0; i < 8; i++ {
		fixture.add("service_calls", map[string]string{"from_svc": service(), "to_svc": service()}, at()...)
	}
	return fixture.data
}

// 随机关系夹具上，逐跳取数与整条读取关系（不启用候选计划）在同一后端上给出相同的快照
// 和紧凑格式：多个根、联合主键、两种方向的动态关系、只在部分评估点出现的边、H 取 1、2、3、6。
// 候选计划的每次读取都按到达的身份或根条件收窄，不读取整条关系；关闭 Matrix 复用时答案不变。
func TestTopologyHopFetchMatchesBulkReadOnRandomFixtures(t *testing.T) {
	oldPlan, oldReuse := SharedTopologyPlanCandidates, SharedTopologyReuseMatrix
	t.Cleanup(func() { SharedTopologyPlanCandidates, SharedTopologyReuseMatrix = oldPlan, oldReuse })
	frames := hopFetchFrames()
	relationTypes := []string{"container_with_pod", "node_with_pod", "version_with_container", "pod_to_service", "service_calls"}
	roots := []struct {
		source   cmdb.Resource
		identity cmdb.Matcher
	}{
		{"container", cmdb.Matcher{"cluster": "c0", "pod": "p0"}},
		{"container", cmdb.Matcher{"cluster": "c0", "pod": "p1", "name": "n0"}},
		{"pod", cmdb.Matcher{"cluster": "c1"}},
		{"service", cmdb.Matcher{"svc": "s0"}},
	}
	for seed := int64(0); seed < 8; seed++ {
		rng := rand.New(rand.NewSource(seed))
		data := randomHopFetchFixture(rng, frames)
		for _, hops := range []int{1, 2, 3, 6} {
			for _, root := range roots {
				direction := []string{"", "outbound", "inbound"}[rng.Intn(3)]
				var allowed []string
				if rng.Intn(3) == 0 {
					drop := rng.Intn(len(relationTypes))
					for i, relationType := range relationTypes {
						if i != drop {
							allowed = append(allowed, relationType)
						}
					}
				}
				request := cmdb.SharedTopologyQuery{
					SpaceUID: "space", SourceType: root.source, SourceInfo: root.identity,
					StartTime: frames[0] / 1000, EndTime: frames[len(frames)-1] / 1000, Step: "60s",
					MaxHops: hops, DynamicRelationDirection: direction, AllowedRelationTypes: allowed,
				}
				t.Run(fmt.Sprintf("seed-%d/H%d/%s-%d/direction-%s/types-%d", seed, hops, root.source, len(root.identity), direction, len(allowed)), func(t *testing.T) {
					SharedTopologyPlanCandidates, SharedTopologyReuseMatrix = false, true
					_, wantSnapshots, wantCompact := queryHopFetchTopology(t, hopFetchProvider(), &hopFetchBackend{t: t, data: data}, request)
					for _, reuse := range []bool{true, false} {
						SharedTopologyPlanCandidates, SharedTopologyReuseMatrix = true, reuse
						backend := &hopFetchBackend{t: t, data: data}
						_, gotSnapshots, gotCompact := queryHopFetchTopology(t, hopFetchProvider(), backend, request)
						require.Equal(t, wantSnapshots, gotSnapshots, "reuse=%v", reuse)
						require.Equal(t, wantCompact, gotCompact, "reuse=%v", reuse)
						for _, call := range backend.calls {
							require.True(t, hopFetchNarrowed(call.conditions), "reuse=%v: %s must be read by reached identities or root conditions", reuse, call.metric)
						}
					}
				})
			}
		}
	}
}

// 故障定位的失败形态：容器根只带集群和 Pod（不完整主键），默认关系范围原先落到整条读取，
// 一条关系的整条读取就超过单次响应上限。逐跳取数在同一后端上成功，答案与不限规模的整条
// 读取一致；每次读取都按根条件或到达的身份收窄，超限的读取按身份分批、对半拆分。
func TestTopologyHopFetchContainerRootStaysUnderResponseLimit(t *testing.T) {
	oldPlan, oldReuse := SharedTopologyPlanCandidates, SharedTopologyReuseMatrix
	t.Cleanup(func() { SharedTopologyPlanCandidates, SharedTopologyReuseMatrix = oldPlan, oldReuse })
	frames := hopFetchFrames()
	fixture := newHopFetchFixture()
	for p := 0; p < 180; p++ {
		pod := fmt.Sprintf("p%03d", p)
		node := "node0"
		if p >= 90 {
			node = "node1"
		}
		fixture.add("node_with_pod", map[string]string{"cluster": "c0", "node": node, "pod": pod}, frames...)
		for _, name := range []string{"n0", "n1"} {
			fixture.add("container_with_pod", map[string]string{"cluster": "c0", "pod": pod, "name": name}, frames...)
			if p < 10 {
				fixture.add("version_with_container", map[string]string{"version": "v0", "cluster": "c0", "pod": pod, "name": name}, frames...)
			}
		}
	}
	fixture.add("pod_to_service", map[string]string{"from_cluster": "c0", "from_pod": "p000", "to_svc": "s0"}, frames...)
	fixture.add("pod_to_service", map[string]string{"from_cluster": "c0", "from_pod": "p001", "to_svc": "s0"}, frames[1:]...)
	fixture.add("service_calls", map[string]string{"from_svc": "s0", "to_svc": "s1"}, frames...)
	fixture.add("service_calls", map[string]string{"from_svc": "s1", "to_svc": "s0"}, frames[:2]...)
	const limit = 100
	require.Greater(t, len(fixture.data["container_with_pod"]), limit, "the whole relation does not fit one response")

	for _, hops := range []int{2, 6} {
		t.Run(fmt.Sprintf("H%d", hops), func(t *testing.T) {
			request := cmdb.SharedTopologyQuery{
				SpaceUID: "space", SourceType: "container", SourceInfo: cmdb.Matcher{"cluster": "c0", "pod": "p000"},
				StartTime: frames[0] / 1000, EndTime: frames[len(frames)-1] / 1000, Step: "60s", MaxHops: hops,
			}
			SharedTopologyPlanCandidates, SharedTopologyReuseMatrix = false, true
			want, wantSnapshots, wantCompact := queryHopFetchTopology(t, hopFetchProvider(), &hopFetchBackend{t: t, data: fixture.data}, request)
			require.Len(t, want.Snapshots, len(frames))
			bulk := &hopFetchBackend{t: t, data: fixture.data, limit: limit}
			_, err := hopFetchModel(hopFetchProvider(), bulk).QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
			require.ErrorIs(t, err, metadata.ErrBackendResponseTooLarge, "the whole-relation read exceeds the response limit")

			SharedTopologyPlanCandidates = true
			backend := &hopFetchBackend{t: t, data: fixture.data, limit: limit}
			got, gotSnapshots, gotCompact := queryHopFetchTopology(t, hopFetchProvider(), backend, request)
			require.Equal(t, wantSnapshots, gotSnapshots)
			require.Equal(t, wantCompact, gotCompact)
			require.NotEmpty(t, backend.calls)
			for _, call := range backend.calls {
				require.True(t, hopFetchNarrowed(call.conditions), "%s must not be read as a whole relation", call.metric)
			}
			for _, snapshot := range got.Snapshots {
				require.False(t, snapshot.Partial)
			}
			if hops == 6 {
				// 节点 node0 上 90 个 Pod 的全部容器都在答案里，总量超过单次响应上限。
				require.Greater(t, len(got.Snapshots[0].Nodes), 2*limit)
				require.Positive(t, backend.rejected, "oversized reads are split by identity instead of failing")
			}
		})
	}
}

// 两个最后一层节点之间的边只能在 H 跳后补读：c1、c2 都在第二跳到达，到达时刻不同，
// 两者之间的边只在两端同时可达的评估点出现，与整条读取的答案一致；补读按两端身份收窄。
func TestTopologyHopFetchReadsInducedEdgesBetweenLastLayerNodes(t *testing.T) {
	oldPlan, oldReuse := SharedTopologyPlanCandidates, SharedTopologyReuseMatrix
	t.Cleanup(func() { SharedTopologyPlanCandidates, SharedTopologyReuseMatrix = oldPlan, oldReuse })
	provider := contractSchemaProvider{
		resources: []ResourceType{"a", "b", "c"},
		primary:   map[ResourceType][]string{"a": {"a_id"}, "b": {"b_id"}, "c": {"c_id"}},
		fields:    map[ResourceType][]string{"a": {"a_id"}, "b": {"b_id"}, "c": {"c_id"}},
		schemas: []RelationSchema{
			{RelationType: "ab", Category: RelationCategoryStatic, FromType: "a", ToType: "b", MetricName: "ab"},
			{RelationType: "bc", Category: RelationCategoryStatic, FromType: "b", ToType: "c", MetricName: "bc"},
			{RelationType: "peer", Category: RelationCategoryDynamic, FromType: "c", ToType: "c", IsDirectional: true, MetricName: "peer"},
		},
	}
	frames := hopFetchFrames()[:3]
	fixture := newHopFetchFixture()
	fixture.add("ab", map[string]string{"a_id": "root", "b_id": "b1"}, frames...)
	fixture.add("bc", map[string]string{"b_id": "b1", "c_id": "c1"}, frames[0], frames[1])
	fixture.add("bc", map[string]string{"b_id": "b1", "c_id": "c2"}, frames[1], frames[2])
	fixture.add("peer", map[string]string{"from_c_id": "c1", "to_c_id": "c2"}, frames...)
	// c3 不可达，这条边不能出现在结果中。
	fixture.add("peer", map[string]string{"from_c_id": "c2", "to_c_id": "c3"}, frames...)
	for _, direction := range []string{"", "outbound"} {
		t.Run("direction-"+direction, func(t *testing.T) {
			request := cmdb.SharedTopologyQuery{
				SpaceUID: "space", SourceType: "a", SourceInfo: cmdb.Matcher{"a_id": "root"},
				StartTime: frames[0] / 1000, EndTime: frames[len(frames)-1] / 1000, Step: "60s",
				MaxHops: 2, DynamicRelationDirection: direction,
			}
			SharedTopologyPlanCandidates, SharedTopologyReuseMatrix = false, true
			_, wantSnapshots, wantCompact := queryHopFetchTopology(t, provider, &hopFetchBackend{t: t, data: fixture.data}, request)
			SharedTopologyPlanCandidates = true
			backend := &hopFetchBackend{t: t, data: fixture.data}
			got, gotSnapshots, gotCompact := queryHopFetchTopology(t, provider, backend, request)
			require.Equal(t, wantSnapshots, gotSnapshots)
			require.Equal(t, wantCompact, gotCompact)

			wantPeers := 1
			if direction == "" {
				// 双向时同一条序列另有一条入向关系。
				wantPeers = 2
			}
			for index, snapshot := range got.Snapshots {
				ids := map[uint64]string{}
				for _, node := range snapshot.Nodes {
					ids[node.ID] = node.Dimensions["c_id"]
				}
				peers := 0
				for _, edge := range snapshot.Edges {
					if edge.RelationType != "peer" {
						continue
					}
					peers++
					pair := []string{ids[edge.Source], ids[edge.Target]}
					sort.Strings(pair)
					require.Equal(t, []string{"c1", "c2"}, pair)
				}
				if index == 1 {
					require.Equal(t, wantPeers, peers, "both ends are reachable only at the middle frame")
				} else {
					require.Zero(t, peers, "frame %d", index)
				}
			}

			peerReads := 0
			for _, call := range backend.calls {
				if call.metric != "peer" {
					continue
				}
				peerReads++
				values := []string{}
				for _, field := range call.conditions.FieldList {
					if field.Operator == structured.ConditionEqual && (field.DimensionName == "from_c_id" || field.DimensionName == "to_c_id") {
						values = append(values, field.Value...)
					}
				}
				sort.Strings(values)
				require.Equal(t, []string{"c1", "c2"}, values, "the induced read is bounded by the two last-layer identities")
			}
			require.Positive(t, peerReads)
		})
	}
}

// 静态关系另一端的主键都在锚点主键内（容器到 Pod）时，展开容器不再按容器读回它的 Pod：
// 那条唯一的边已随 Pod 一侧的成对读取写入图。答案与整条读取相同。
func TestTopologyHopFetchSkipsDeterminedParentEdges(t *testing.T) {
	oldPlan, oldReuse := SharedTopologyPlanCandidates, SharedTopologyReuseMatrix
	t.Cleanup(func() { SharedTopologyPlanCandidates, SharedTopologyReuseMatrix = oldPlan, oldReuse })
	frames := hopFetchFrames()
	for seed := int64(0); seed < 4; seed++ {
		data := randomHopFetchFixture(rand.New(rand.NewSource(seed)), frames)
		for _, hops := range []int{2, 3} {
			t.Run(fmt.Sprintf("seed-%d/H%d", seed, hops), func(t *testing.T) {
				request := cmdb.SharedTopologyQuery{
					SpaceUID: "space", SourceType: "pod", SourceInfo: cmdb.Matcher{"cluster": "c0", "pod": "p0"},
					StartTime: frames[0] / 1000, EndTime: frames[len(frames)-1] / 1000, Step: "60s", MaxHops: hops,
				}
				SharedTopologyPlanCandidates, SharedTopologyReuseMatrix = false, true
				_, wantSnapshots, wantCompact := queryHopFetchTopology(t, hopFetchProvider(), &hopFetchBackend{t: t, data: data}, request)
				SharedTopologyPlanCandidates = true
				backend := &hopFetchBackend{t: t, data: data}
				_, gotSnapshots, gotCompact := queryHopFetchTopology(t, hopFetchProvider(), backend, request)
				require.Equal(t, wantSnapshots, gotSnapshots)
				require.Equal(t, wantCompact, gotCompact)
				for _, call := range backend.calls {
					if call.metric != "container_with_pod" {
						continue
					}
					for _, field := range call.conditions.FieldList {
						require.False(t, field.DimensionName == "name" && field.Operator == structured.ConditionEqual,
							"a container's pod edge is not read again by the container: %+v", call.conditions)
					}
				}
			})
		}
	}
}

// 同一关系、同一锚点一侧、同一收窄范围的前沿宽读取超过单次响应边界后，本请求内后续各层
// 直接按身份分批，不再先失败一次；答案与整条读取相同。
func TestTopologyHopFetchRemembersTooLargeScopes(t *testing.T) {
	oldPlan, oldReuse := SharedTopologyPlanCandidates, SharedTopologyReuseMatrix
	t.Cleanup(func() { SharedTopologyPlanCandidates, SharedTopologyReuseMatrix = oldPlan, oldReuse })
	provider := contractSchemaProvider{
		resources: []ResourceType{"r", "c"},
		primary:   map[ResourceType][]string{"r": {"r_id"}, "c": {"c_id"}},
		fields:    map[ResourceType][]string{"r": {"r_id"}, "c": {"c_id"}},
		schemas: []RelationSchema{
			{RelationType: "rc", Category: RelationCategoryStatic, FromType: "r", ToType: "c", IsDirectional: true, MetricName: "rc"},
			{RelationType: "peer", Category: RelationCategoryDynamic, FromType: "c", ToType: "c", IsDirectional: true, MetricName: "peer"},
		},
	}
	frames := hopFetchFrames()[:2]
	fixture := newHopFetchFixture()
	const width = timeGraphWideFrontierThreshold + 2
	for i := 0; i < width; i++ {
		fixture.add("rc", map[string]string{"r_id": "root", "c_id": fmt.Sprintf("a%03d", i)}, frames...)
		fixture.add("peer", map[string]string{"from_c_id": fmt.Sprintf("a%03d", i), "to_c_id": fmt.Sprintf("b%03d", i)}, frames...)
		fixture.add("peer", map[string]string{"from_c_id": fmt.Sprintf("b%03d", i), "to_c_id": fmt.Sprintf("d%03d", i)}, frames...)
	}
	request := cmdb.SharedTopologyQuery{
		SpaceUID: "space", SourceType: "r", SourceInfo: cmdb.Matcher{"r_id": "root"}, DynamicRelationDirection: "outbound",
		StartTime: frames[0] / 1000, EndTime: frames[len(frames)-1] / 1000, Step: "60s", MaxHops: 3,
	}
	SharedTopologyPlanCandidates, SharedTopologyReuseMatrix = false, true
	_, wantSnapshots, wantCompact := queryHopFetchTopology(t, provider, &hopFetchBackend{t: t, data: fixture.data}, request)
	SharedTopologyPlanCandidates = true
	// 整条 peer 关系有 2*width 条序列，超过上限；按 64 个身份分批的读取不超过。
	backend := &hopFetchBackend{t: t, data: fixture.data, limit: width + 10}
	_, gotSnapshots, gotCompact := queryHopFetchTopology(t, provider, backend, request)
	require.Equal(t, wantSnapshots, gotSnapshots)
	require.Equal(t, wantCompact, gotCompact)
	wide := map[string]int{}
	for _, call := range backend.calls {
		if call.metric == "peer" && !hopFetchNarrowed(call.conditions) {
			wide[fmt.Sprintf("%+v", call.conditions)]++
		}
	}
	require.NotEmpty(t, wide, "the fixture must exercise an oversized wide read")
	for scope, count := range wide {
		// 快照和紧凑格式各是一次请求，各自最多尝试一次。
		require.LessOrEqual(t, count, 2, "an oversized scope is tried at most once per request: %s", scope)
	}
}
