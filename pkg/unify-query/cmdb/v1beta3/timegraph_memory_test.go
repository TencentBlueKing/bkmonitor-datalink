// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/prometheus/procfs"
	"github.com/stretchr/testify/require"
)

func TestTopologyReservationAdmissionAndRelease(t *testing.T) {
	oldYolo, oldConcurrent, oldBudget, oldRequest, oldHeadroom, oldRead := yoloMode, MaxSharedTopologyConcurrent, MaxSharedTopologyReservedBytes, SharedTopologyRequestMemoryBytes, SharedTopologyMemoryHeadroom, topologyMemoryHeadroom
	t.Cleanup(func() {
		yoloMode = oldYolo
		MaxSharedTopologyConcurrent = oldConcurrent
		MaxSharedTopologyReservedBytes = oldBudget
		SharedTopologyRequestMemoryBytes = oldRequest
		SharedTopologyMemoryHeadroom = oldHeadroom
		topologyMemoryHeadroom = oldRead
	})
	yoloMode = false
	MaxSharedTopologyConcurrent = 3
	MaxSharedTopologyReservedBytes = 1000
	SharedTopologyRequestMemoryBytes = 100
	SharedTopologyMemoryHeadroom = 50
	topologyMemoryHeadroom = func() (int64, bool) { return 10000, true }
	ctx, release, err := AcquireSharedTopology(context.Background())
	require.NoError(t, err)
	t.Cleanup(release)
	require.Equal(t, int64(100), topologyAdmission.reserved)
	nested, nestedRelease, err := AcquireSharedTopology(ctx)
	require.NoError(t, err)
	nestedRelease()
	require.Equal(t, ctx, nested)
	require.NoError(t, ReserveTopologyStage(ctx, "matrix", 200))
	require.ErrorContains(t, ReserveTopologyStage(ctx, "invalid", -1), "negative")
	require.NoError(t, ReserveTopologyStage(ctx, "graph", 300))
	require.Equal(t, int64(500), topologyAdmission.reserved)
	require.ErrorContains(t, ReserveTopologyStage(ctx, "output", 501), "max_topology_reserved_bytes")
	require.Equal(t, int64(500), topologyAdmission.reserved)
	topologyMemoryHeadroom = func() (int64, bool) { return 650, true }
	require.ErrorContains(t, ReserveTopologyStage(ctx, "output", 101), "topology_memory_headroom")
	require.NoError(t, ReserveTopologyStage(ctx, "output", 100))
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	require.ErrorIs(t, ReserveTopologyStage(canceled, "graph", 1), context.Canceled)
	require.NoError(t, ReserveTopologyStage(canceled, "graph", 0))
	release()
	release()
	require.Zero(t, topologyAdmission.reserved)
	require.Zero(t, topologyAdmission.active)
	_, _, err = AcquireSharedTopology(ctx)
	require.ErrorIs(t, err, context.Canceled)
	topologyMemoryHeadroom = func() (int64, bool) { return 0, false }
	var releases []func()
	for i := 0; i < 3; i++ {
		_, r, e := AcquireSharedTopology(context.Background())
		require.NoError(t, e)
		releases = append(releases, r)
		t.Cleanup(r)
	}
	_, _, err = AcquireSharedTopology(context.Background())
	require.ErrorContains(t, err, "max_topology_concurrent_requests")
	yoloMode = true
	ctx, yoloRelease, err := AcquireSharedTopology(context.Background())
	require.NoError(t, err)
	require.NoError(t, ReserveTopologyStage(ctx, "output", 1<<50))
	yoloRelease()
	for _, r := range releases {
		r()
	}
	require.Zero(t, topologyAdmission.reserved)
	require.Zero(t, topologyAdmission.active)
}

func TestTopologyVisibleAncestorMemoryHeadroom(t *testing.T) {
	oldPaths := topologyMemoryPaths
	t.Cleanup(func() { topologyMemoryPaths = oldPaths })
	dir := t.TempDir()
	paths := []topologyMemoryFiles{{filepath.Join(dir, "child.max"), filepath.Join(dir, "child.current")}, {filepath.Join(dir, "parent.max"), filepath.Join(dir, "parent.current")}}
	for path, value := range map[string]string{paths[0].limit: "1000", paths[0].usage: "100", paths[1].limit: "800", paths[1].usage: "600"} {
		require.NoError(t, os.WriteFile(path, []byte(value), 0600))
	}
	topologyMemoryPaths = func() []topologyMemoryFiles { return paths }
	available, known := readTopologyMemoryHeadroom()
	require.True(t, known)
	require.Equal(t, int64(200), available)
	require.NoError(t, os.WriteFile(paths[1].limit, []byte("max"), 0600))
	available, known = readTopologyMemoryHeadroom()
	require.True(t, known)
	require.Equal(t, int64(900), available)
	require.NoError(t, os.WriteFile(paths[0].usage, []byte("1001"), 0600))
	available, known = readTopologyMemoryHeadroom()
	require.True(t, known)
	require.Zero(t, available)
}

func TestTopologyCgroupPaths(t *testing.T) {
	for _, test := range []struct {
		name   string
		groups []procfs.Cgroup
		mounts []*procfs.MountInfo
		want   []topologyMemoryFiles
	}{
		{"v2 parents", []procfs.Cgroup{{HierarchyID: 0, Path: "/a/b"}}, []*procfs.MountInfo{{FSType: "cgroup2", Root: "/", MountPoint: "/cg"}}, []topologyMemoryFiles{{"/cg/a/b/memory.max", "/cg/a/b/memory.current"}, {"/cg/a/memory.max", "/cg/a/memory.current"}, {"/cg/memory.max", "/cg/memory.current"}}},
		{"v1 private namespace", []procfs.Cgroup{{HierarchyID: 2, Controllers: []string{"memory"}, Path: "/"}}, []*procfs.MountInfo{{FSType: "cgroup", Root: "/pod/container", MountPoint: "/cg/memory", SuperOptions: map[string]string{"memory": ""}}}, []topologyMemoryFiles{{"/cg/memory/memory.limit_in_bytes", "/cg/memory/memory.usage_in_bytes"}}},
		{"wrong subtree", []procfs.Cgroup{{HierarchyID: 0, Path: "/other"}}, []*procfs.MountInfo{{FSType: "cgroup2", Root: "/a", MountPoint: "/cg"}}, nil},
	} {
		t.Run(test.name, func(t *testing.T) { require.Equal(t, test.want, topologyCgroupMemoryPaths(test.groups, test.mounts)) })
	}
	path := filepath.Join(t.TempDir(), "memory.max")
	for _, value := range []string{"max", "9223372036854771712", "invalid", "-1"} {
		require.NoError(t, os.WriteFile(path, []byte(value), 0600))
		_, ok := topologyMemoryValue(path)
		require.False(t, ok)
	}
	require.NoError(t, os.WriteFile(path, []byte("1048576\n"), 0600))
	value, ok := topologyMemoryValue(path)
	require.True(t, ok)
	require.Equal(t, int64(1048576), value)
	_, ok = topologyMemoryValue(path + "missing")
	require.False(t, ok)
}
