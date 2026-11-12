// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
)

// 逐帧接口交出的快照与一次物化全部帧的结果完全相同（含节点 ID、排序、属性和 partial），
// 包括 target_types 过滤；snapshots 格式的 header 不带 Snapshots，compact 格式不交出帧。
func TestTopologyFramesMatchCollectedSnapshots(t *testing.T) {
	frames := hopFetchFrames()
	for seed := int64(0); seed < 4; seed++ {
		data := randomHopFetchFixture(rand.New(rand.NewSource(seed)), frames)
		for _, targetTypes := range [][]cmdb.Resource{nil, {"node"}, {"pod", "service"}} {
			t.Run(fmt.Sprintf("seed-%d/targets-%v", seed, targetTypes), func(t *testing.T) {
				request := cmdb.SharedTopologyQuery{
					SpaceUID: "space", SourceType: "container", SourceInfo: cmdb.Matcher{"cluster": "c0", "pod": "p0"},
					StartTime: frames[0] / 1000, EndTime: frames[len(frames)-1] / 1000, Step: "60s", MaxHops: 3, TargetTypes: targetTypes,
				}
				want, err := hopFetchModel(hopFetchProvider(), &hopFetchBackend{t: t, data: data}).QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
				require.NoError(t, err)
				require.Len(t, want.Snapshots, len(frames))

				var got []cmdb.SharedTopologySnapshot
				err = hopFetchModel(hopFetchProvider(), &hopFetchBackend{t: t, data: data}).QuerySharedTopologyFrames(initTimeGraphQueryTestEnvironment(), request, func(header cmdb.SharedTopologyResult, source TopologyFrames) error {
					require.Nil(t, header.Snapshots, "snapshots are produced by the frame source")
					require.Equal(t, want.PointCount, header.PointCount)
					require.Equal(t, want.Step, header.Step)
					require.NotNil(t, source)
					return source(func(snapshot cmdb.SharedTopologySnapshot) error {
						got = append(got, snapshot)
						return nil
					})
				})
				require.NoError(t, err)
				require.Equal(t, want.Snapshots, got)

				request.ResponseFormat = cmdb.CompactTopologyFormat
				err = hopFetchModel(hopFetchProvider(), &hopFetchBackend{t: t, data: data}).QuerySharedTopologyFrames(initTimeGraphQueryTestEnvironment(), request, func(header cmdb.SharedTopologyResult, source TopologyFrames) error {
					require.Nil(t, source)
					require.NotNil(t, header.Compact)
					return nil
				})
				require.NoError(t, err)
			})
		}
	}
}

// compact 结果是独立副本，原图在交给 consume 之前释放，编码期间不再同时持有原图；
// 释放后的结果仍与快照一致。snapshots 逐帧读图，原图活到 consume 返回之后。两种格式都只释放一次。
func TestTopologyCompactReleasesGraphBeforeConsume(t *testing.T) {
	frames := hopFetchFrames()
	data := randomHopFetchFixture(rand.New(rand.NewSource(0)), frames)
	request := cmdb.SharedTopologyQuery{
		SpaceUID: "space", SourceType: "container", SourceInfo: cmdb.Matcher{"cluster": "c0", "pod": "p0"},
		StartTime: frames[0] / 1000, EndTime: frames[len(frames)-1] / 1000, Step: "60s", MaxHops: 3,
	}
	want, err := hopFetchModel(hopFetchProvider(), &hopFetchBackend{t: t, data: data}).QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
	require.NoError(t, err)
	wantSnapshots := canonicalTopologyForTest(t, want.Snapshots)
	require.NotEmpty(t, want.Snapshots[0].Nodes)
	cleanups := func() float64 {
		return readTimeGraphMetric(t, "cmdb_timegraph_stage_seconds", map[string]string{"stage": "cleanup", "result": "success"}, "count")
	}
	for _, format := range []string{"", cmdb.CompactTopologyFormat} {
		t.Run("format="+format, func(t *testing.T) {
			request.ResponseFormat = format
			before := cleanups()
			err := hopFetchModel(hopFetchProvider(), &hopFetchBackend{t: t, data: data}).QuerySharedTopologyFrames(initTimeGraphQueryTestEnvironment(), request, func(header cmdb.SharedTopologyResult, source TopologyFrames) error {
				if format == cmdb.CompactTopologyFormat {
					require.Equal(t, before+1, cleanups(), "the graph is released before the compact result is encoded")
					require.Equal(t, wantSnapshots, canonicalTopologyForTest(t, decodeCompactForTest(t, header.Compact)))
					return nil
				}
				return source(func(cmdb.SharedTopologySnapshot) error {
					require.Equal(t, before, cleanups(), "snapshot frames read the graph while they are encoded")
					return nil
				})
			})
			require.NoError(t, err)
			require.Equal(t, before+1, cleanups(), "the graph is released exactly once")
		})
	}
}
