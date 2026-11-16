// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"fmt"
	"testing"

	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
)

// defaultSchemaHopFixture 是内置默认 Schema 下的容器根场景。容器的非主键属性 version 和主机的
// 非主键属性 version 都由 app_version 关系投影（app_version 的主键含 version），同一节点在
// 同一帧可有多个取值。pod-1/c1 另有一条 app_version 不会被到达的序列，主机 11 另有一条
// 未到达 app_version 的序列：这两个属性只在读取以到达节点为端点的全部序列后才与宽读取相同。
func defaultSchemaHopFixture(frames []int64) map[string]pl.Matrix {
	fixture := newHopFetchFixture()
	const cluster, namespace = "cluster-a", "ns-a"
	for p := 0; p < 6; p++ {
		pod := fmt.Sprintf("pod-%d", p)
		fixture.add("node_with_pod_relation", map[string]string{"bcs_cluster_id": cluster, "namespace": namespace, "node": fmt.Sprintf("node-%d", p%2), "pod": pod}, frames...)
		fixture.add("pod_with_replicaset_relation", map[string]string{"bcs_cluster_id": cluster, "namespace": namespace, "pod": pod, "replicaset": fmt.Sprintf("rs-%d", p%3)}, frames...)
		for c := 0; c < 2; c++ {
			container := map[string]string{"bcs_cluster_id": cluster, "namespace": namespace, "pod": pod, "container": fmt.Sprintf("c%d", c)}
			fixture.add("container_with_pod_relation", container, frames...)
			version := func(app, value string) map[string]string {
				labels := map[string]string{"app_name": app, "version": value}
				for key, label := range container {
					labels[key] = label
				}
				return labels
			}
			fixture.add("app_version_with_container_relation", version("app", "v1"), frames...)
			if (p+c)%2 == 0 {
				fixture.add("app_version_with_container_relation", version("app", "v2"), frames[1:]...)
			}
			if p == 1 && c == 1 {
				fixture.add("app_version_with_container_relation", version("beta", "v7"), frames[:2]...)
			}
		}
		if p > 0 {
			fixture.add("pod_to_pod_flow", map[string]string{
				"from_bcs_cluster_id": cluster, "from_namespace": namespace, "from_pod": fmt.Sprintf("pod-%d", p-1),
				"to_bcs_cluster_id": cluster, "to_namespace": namespace, "to_pod": pod,
			}, frames[p%len(frames):]...)
		}
	}
	fixture.add("deployment_with_replicaset_relation", map[string]string{"bcs_cluster_id": cluster, "namespace": namespace, "deployment": "web", "replicaset": "rs-0"}, frames...)
	fixture.add("node_with_system_relation", map[string]string{"bcs_cluster_id": cluster, "node": "node-0", "bk_target_ip": "192.0.2.10"}, frames...)
	fixture.add("app_version_with_host_relation", map[string]string{"app_name": "app", "version": "v1", "bk_host_id": "11"}, frames...)
	fixture.add("app_version_with_host_relation", map[string]string{"app_name": "other", "version": "v9", "bk_host_id": "11"}, frames[:2]...)
	fixture.add("host_with_system_relation", map[string]string{"bk_host_id": "11", "bk_target_ip": "192.0.2.10"}, frames...)
	return fixture.data
}

// 故障定位的容器根在内置默认 Schema 与默认关系范围下：容器和主机的 version 属性由
// app_version 关系投影，原先因投影冲突整条读取关系分量。逐跳取数后按到达节点补读这些
// 关系，H2、H6 的快照与紧凑格式都与整条读取相同，每次读取都按根条件或到达的身份收窄，
// 取数 span 报告 fetch-mode=hop。
func TestTopologyHopFetchDefaultSchemaContainerRoot(t *testing.T) {
	oldPlan, oldReuse := SharedTopologyPlanCandidates, SharedTopologyReuseMatrix
	t.Cleanup(func() { SharedTopologyPlanCandidates, SharedTopologyReuseMatrix = oldPlan, oldReuse })
	frames := hopFetchFrames()
	data := defaultSchemaHopFixture(frames)
	query := func(t *testing.T, backend *hopFetchBackend, request cmdb.SharedTopologyQuery) ([]string, []string, cmdb.SharedTopologyResult) {
		t.Helper()
		model := &Model{schemaProvider: NewStaticSchemaProvider(), timeGraphQueryReference: selectorQueryReference, timeGraphVMQueryWithPartial: backend.query}
		request.ResponseFormat = ""
		result, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
		require.NoError(t, err)
		request.ResponseFormat = cmdb.CompactTopologyFormat
		compact, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
		require.NoError(t, err)
		return canonicalTopologyForTest(t, result.Snapshots), canonicalTopologyForTest(t, decodeCompactForTest(t, compact.Compact)), result
	}
	for _, hops := range []int{2, 6} {
		for _, reuse := range []bool{true, false} {
			t.Run(fmt.Sprintf("H%d/reuse-%v", hops, reuse), func(t *testing.T) {
				SharedTopologyReuseMatrix = reuse
				request := cmdb.SharedTopologyQuery{
					SpaceUID: "space", Sources: []cmdb.TopologySource{{SourceType: "container", SourceInfo: cmdb.Matcher{"bcs_cluster_id": "cluster-a", "namespace": "ns-a", "pod": "pod-0"}}},

					StartTime: frames[0] / 1000, EndTime: frames[len(frames)-1] / 1000, Step: "60s", MaxHops: hops,
				}
				SharedTopologyPlanCandidates = false
				wantSnapshots, wantCompact, _ := query(t, &hopFetchBackend{t: t, data: data}, request)

				recorder := tracetest.NewSpanRecorder()
				provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
				previous := otel.GetTracerProvider()
				otel.SetTracerProvider(provider)
				t.Cleanup(func() { otel.SetTracerProvider(previous); require.NoError(t, provider.Shutdown(context.Background())) })
				SharedTopologyPlanCandidates = true
				backend := &hopFetchBackend{t: t, data: data}
				gotSnapshots, gotCompact, result := query(t, backend, request)
				require.Equal(t, wantSnapshots, gotSnapshots)
				require.Equal(t, wantCompact, gotCompact)
				require.NotEmpty(t, backend.calls)
				for _, call := range backend.calls {
					require.True(t, hopFetchNarrowed(call.conditions), "%s must be read by reached identities or root conditions", call.metric)
				}

				modes, attributeReads, localReported := map[string]bool{}, int64(0), false
				for _, span := range recorder.Ended() {
					for _, kv := range span.Attributes() {
						switch {
						case span.Name() == "timegraph-select-fetch" && kv.Key == attribute.Key("fetch-mode"):
							modes[kv.Value.AsString()] = true
						case span.Name() == "timegraph-hop-fetch" && kv.Key == attribute.Key("hop-attribute-read-count"):
							attributeReads += kv.Value.AsInt64()
						case span.Name() == "timegraph-hop-fetch" && kv.Key == attribute.Key("hop-local-duration-seconds"):
							localReported = localReported || kv.Value.AsFloat64() >= 0
						}
					}
				}
				require.Equal(t, map[string]bool{"hop": true}, modes)
				require.True(t, localReported, "hop fetch reports the time spent outside backend queries")
				if hops == 2 {
					// H6 时这些节点都已展开，以它们为端点的序列已经读入，不需要补读。
					require.Positive(t, attributeReads, "attributes projected by app_version relations are read again by reached identities")
				}

				// 只有补读才能得到的属性：pod-1/c1 的 v7 来自未到达的 app_version，主机的 v9 同理。
				versions := map[string]bool{}
				for _, snapshot := range result.Snapshots[:2] {
					for _, node := range snapshot.Nodes {
						if (node.ResourceType == "container" && node.Dimensions["pod"] == "pod-1" && node.Dimensions["container"] == "c1") || node.ResourceType == "host" {
							versions[string(node.ResourceType)+"="+node.Dimensions["version"]] = true
						}
					}
				}
				require.Equal(t, map[string]bool{"container=v7": true, "host=v9": true}, versions)
			})
		}
	}
}
