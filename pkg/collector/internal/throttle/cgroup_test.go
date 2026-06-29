// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package throttle

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCgroupReaderV2(t *testing.T) {
	root := t.TempDir()
	sysfsRoot := filepath.Join(root, "sys", "fs", "cgroup")
	procRoot := filepath.Join(root, "proc")
	leaf := filepath.Join(sysfsRoot, "kubepods", "pod1")

	writeTestFile(t, filepath.Join(procRoot, "self", "cgroup"), "0::/kubepods/pod1\n")
	writeTestFile(t, filepath.Join(procRoot, "self", "mountinfo"), fmt.Sprintf("36 25 0:32 / %s rw - cgroup2 cgroup rw\n", sysfsRoot))
	writeTestFile(t, filepath.Join(leaf, "cpu.max"), "150000 100000\n")
	writeTestFile(t, filepath.Join(leaf, "cpuset.cpus.effective"), "0-3\n")
	writeTestFile(t, filepath.Join(leaf, "cpu.stat"), "usage_usec 123456\nnr_periods 1\n")
	writeTestFile(t, filepath.Join(leaf, "memory.current"), "1000\n")
	writeTestFile(t, filepath.Join(leaf, "memory.stat"), "inactive_file 250\n")
	writeTestFile(t, filepath.Join(leaf, "memory.max"), "2000\n")

	reader := newCgroupReaderWithRoots(sysfsRoot, procRoot)
	cores, ok := reader.EffectiveCores()
	assert.True(t, ok)
	assert.InDelta(t, 1.5, cores, 0.001)

	usage, err := reader.CPUUsageNanos()
	require.NoError(t, err)
	assert.Equal(t, uint64(123456000), usage)

	workingSet, ok := reader.MemWorkingSet()
	assert.True(t, ok)
	assert.Equal(t, uint64(750), workingSet)

	limit, ok := reader.MemLimit()
	assert.True(t, ok)
	assert.Equal(t, uint64(2000), limit)
}

func TestCgroupReaderV2HierarchyLimits(t *testing.T) {
	root := t.TempDir()
	sysfsRoot := filepath.Join(root, "sys", "fs", "cgroup")
	procRoot := filepath.Join(root, "proc")
	leaf := filepath.Join(sysfsRoot, "kubepods", "pod1", "container1")
	pod := filepath.Join(sysfsRoot, "kubepods", "pod1")
	parent := filepath.Join(sysfsRoot, "kubepods")

	writeTestFile(t, filepath.Join(procRoot, "self", "cgroup"), "0::/kubepods/pod1/container1\n")
	writeTestFile(t, filepath.Join(procRoot, "self", "mountinfo"), fmt.Sprintf("36 25 0:32 / %s rw - cgroup2 cgroup rw\n", sysfsRoot))
	writeTestFile(t, filepath.Join(leaf, "cpu.max"), "max 100000\n")
	writeTestFile(t, filepath.Join(leaf, "memory.max"), "max\n")
	writeTestFile(t, filepath.Join(pod, "cpu.max"), "200000\n")
	writeTestFile(t, filepath.Join(pod, "memory.max"), "3000\n")
	writeTestFile(t, filepath.Join(parent, "cpu.max"), "50000 100000\n")
	writeTestFile(t, filepath.Join(parent, "memory.max"), "1000\n")
	writeTestFile(t, filepath.Join(sysfsRoot, "cpu.max"), "100000 100000\n")
	writeTestFile(t, filepath.Join(sysfsRoot, "memory.max"), "2000\n")

	reader := newCgroupReaderWithRoots(sysfsRoot, procRoot)
	cores, ok := reader.EffectiveCores()
	assert.True(t, ok)
	assert.InDelta(t, 0.5, cores, 0.001)

	limit, ok := reader.MemLimit()
	assert.True(t, ok)
	assert.Equal(t, uint64(1000), limit)
}

func TestCgroupReaderV1(t *testing.T) {
	root := t.TempDir()
	sysfsRoot := filepath.Join(root, "sys", "fs", "cgroup")
	procRoot := filepath.Join(root, "proc")
	cpuMount := filepath.Join(sysfsRoot, "cpu")
	cpuacctMount := filepath.Join(sysfsRoot, "cpuacct")
	cpusetMount := filepath.Join(sysfsRoot, "cpuset")
	memoryMount := filepath.Join(sysfsRoot, "memory")
	rel := filepath.Join("docker", "abc")

	writeTestFile(t, filepath.Join(procRoot, "self", "cgroup"), "2:cpu:/docker/abc\n3:cpuacct:/docker/abc\n4:cpuset:/docker/abc\n5:memory:/docker/abc\n")
	writeTestFile(t, filepath.Join(procRoot, "self", "mountinfo"), fmt.Sprintf(
		"36 25 0:32 / %s rw - cgroup cgroup rw,cpu\n37 25 0:33 / %s rw - cgroup cgroup rw,cpuacct\n38 25 0:34 / %s rw - cgroup cgroup rw,cpuset\n39 25 0:35 / %s rw - cgroup cgroup rw,memory\n",
		cpuMount, cpuacctMount, cpusetMount, memoryMount,
	))
	writeTestFile(t, filepath.Join(cpuMount, rel, "cpu.cfs_quota_us"), "50000\n")
	writeTestFile(t, filepath.Join(cpuMount, rel, "cpu.cfs_period_us"), "100000\n")
	writeTestFile(t, filepath.Join(cpuacctMount, rel, "cpuacct.usage"), "987654321\n")
	writeTestFile(t, filepath.Join(cpusetMount, rel, "cpuset.cpus"), "0-3\n")
	writeTestFile(t, filepath.Join(memoryMount, rel, "memory.usage_in_bytes"), "4096\n")
	writeTestFile(t, filepath.Join(memoryMount, rel, "memory.stat"), "inactive_file 8\ntotal_inactive_file 1024\n")
	writeTestFile(t, filepath.Join(memoryMount, rel, "memory.limit_in_bytes"), "8192\n")

	reader := newCgroupReaderWithRoots(sysfsRoot, procRoot)
	cores, ok := reader.EffectiveCores()
	assert.True(t, ok)
	assert.InDelta(t, 0.5, cores, 0.001)

	usage, err := reader.CPUUsageNanos()
	require.NoError(t, err)
	assert.Equal(t, uint64(987654321), usage)

	workingSet, ok := reader.MemWorkingSet()
	assert.True(t, ok)
	assert.Equal(t, uint64(3072), workingSet)

	limit, ok := reader.MemLimit()
	assert.True(t, ok)
	assert.Equal(t, uint64(8192), limit)
}

func TestCgroupReaderV1ReadsControllerMountRootFirst(t *testing.T) {
	root := t.TempDir()
	sysfsRoot := filepath.Join(root, "sys", "fs", "cgroup")
	procRoot := filepath.Join(root, "proc")
	cpuMount := filepath.Join(sysfsRoot, "cpu")
	memoryMount := filepath.Join(sysfsRoot, "memory")
	rel := filepath.Join("docker", "abc")

	writeTestFile(t, filepath.Join(procRoot, "self", "cgroup"), "2:cpu:/docker/abc\n5:memory:/docker/abc\n")
	writeTestFile(t, filepath.Join(procRoot, "self", "mountinfo"), fmt.Sprintf(
		"36 25 0:32 / %s rw - cgroup cgroup rw,cpu\n39 25 0:35 / %s rw - cgroup cgroup rw,memory\n",
		cpuMount, memoryMount,
	))
	writeTestFile(t, filepath.Join(cpuMount, "cpu.cfs_quota_us"), "100000\n")
	writeTestFile(t, filepath.Join(cpuMount, "cpu.cfs_period_us"), "100000\n")
	writeTestFile(t, filepath.Join(cpuMount, rel, "cpu.cfs_quota_us"), "50000\n")
	writeTestFile(t, filepath.Join(cpuMount, rel, "cpu.cfs_period_us"), "100000\n")
	writeTestFile(t, filepath.Join(memoryMount, "memory.limit_in_bytes"), "2048\n")
	writeTestFile(t, filepath.Join(memoryMount, rel, "memory.limit_in_bytes"), "1024\n")

	reader := newCgroupReaderWithRoots(sysfsRoot, procRoot)
	cores, ok := reader.EffectiveCores()
	assert.True(t, ok)
	assert.InDelta(t, 1.0, cores, 0.001)

	limit, ok := reader.MemLimit()
	assert.True(t, ok)
	assert.Equal(t, uint64(2048), limit)
}

func TestCgroupReaderUnlimitedFallbacks(t *testing.T) {
	root := t.TempDir()
	sysfsRoot := filepath.Join(root, "sys", "fs", "cgroup")
	procRoot := filepath.Join(root, "proc")
	leaf := filepath.Join(sysfsRoot, "slice")

	writeTestFile(t, filepath.Join(procRoot, "self", "cgroup"), "0::/slice\n")
	writeTestFile(t, filepath.Join(procRoot, "self", "mountinfo"), fmt.Sprintf("36 25 0:32 / %s rw - cgroup2 cgroup rw\n", sysfsRoot))
	writeTestFile(t, filepath.Join(leaf, "cpu.max"), "max 100000\n")
	writeTestFile(t, filepath.Join(leaf, "cpuset.cpus.effective"), "0-15\n")
	writeTestFile(t, filepath.Join(leaf, "memory.max"), "max\n")

	reader := newCgroupReaderWithRoots(sysfsRoot, procRoot)
	_, ok := reader.cpuQuotaCores()
	assert.False(t, ok)
	_, ok = reader.EffectiveCores()
	assert.False(t, ok)
	_, ok = reader.MemLimit()
	assert.False(t, ok)
}

func TestCgroupReaderV1UnlimitedFallbacks(t *testing.T) {
	root := t.TempDir()
	sysfsRoot := filepath.Join(root, "sys", "fs", "cgroup")
	procRoot := filepath.Join(root, "proc")
	cpuMount := filepath.Join(sysfsRoot, "cpu")
	cpusetMount := filepath.Join(sysfsRoot, "cpuset")
	memoryMount := filepath.Join(sysfsRoot, "memory")
	rel := filepath.Join("docker", "abc")

	writeTestFile(t, filepath.Join(procRoot, "self", "cgroup"), "2:cpu:/docker/abc\n4:cpuset:/docker/abc\n5:memory:/docker/abc\n")
	writeTestFile(t, filepath.Join(procRoot, "self", "mountinfo"), fmt.Sprintf(
		"36 25 0:32 / %s rw - cgroup cgroup rw,cpu\n38 25 0:34 / %s rw - cgroup cgroup rw,cpuset\n39 25 0:35 / %s rw - cgroup cgroup rw,memory\n",
		cpuMount, cpusetMount, memoryMount,
	))
	writeTestFile(t, filepath.Join(cpuMount, rel, "cpu.cfs_quota_us"), "-1\n")
	writeTestFile(t, filepath.Join(cpuMount, rel, "cpu.cfs_period_us"), "100000\n")
	writeTestFile(t, filepath.Join(cpusetMount, rel, "cpuset.cpus"), "0-15\n")
	writeTestFile(t, filepath.Join(memoryMount, rel, "memory.limit_in_bytes"), "9223372036854771712\n")

	reader := newCgroupReaderWithRoots(sysfsRoot, procRoot)
	_, ok := reader.cpuQuotaCores()
	assert.False(t, ok)
	_, ok = reader.EffectiveCores()
	assert.False(t, ok)
	_, ok = reader.MemLimit()
	assert.False(t, ok)
}

func TestCountCPUSet(t *testing.T) {
	assert.Equal(t, 5, countCPUSet("0-2,4,6"))
	assert.Equal(t, 0, countCPUSet(""))
	assert.Equal(t, 0, countCPUSet("foo"))
}

func TestParseCPUMax(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  float64
		ok    bool
	}{
		{name: "default period", value: "200000", want: 2, ok: true},
		{name: "explicit period", value: "50000 100000", want: 0.5, ok: true},
		{name: "max", value: "max 100000", ok: false},
		{name: "zero period", value: "100000 0", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseCPUMax(tt.value)
			assert.Equal(t, tt.ok, ok)
			assert.InDelta(t, tt.want, got, 0.001)
		})
	}
}

func writeTestFile(t *testing.T, filename, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(filename), 0o755))
	require.NoError(t, os.WriteFile(filename, []byte(content), 0o644))
}
