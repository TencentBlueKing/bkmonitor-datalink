// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License").

//go:build linux

package executor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/moby/sys/mountinfo"
)

func TestReadHostMemoryAvailable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meminfo")
	if err := os.WriteFile(path, []byte("MemTotal: 4096 kB\nMemAvailable: 1536 kB\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	available, err := readHostMemoryAvailable(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := uint64(1536 * 1024); available != want {
		t.Fatalf("available = %d, want %d", available, want)
	}
}

func TestReadHostMemoryAvailableFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meminfo")
	raw := "MemFree: 100 kB\nBuffers: 20 kB\nCached: 80 kB\nSReclaimable: 10 kB\nShmem: 30 kB\n"
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	available, err := readHostMemoryAvailable(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := uint64(180 * 1024); available != want {
		t.Fatalf("available = %d, want %d", available, want)
	}
}

func TestResolveCgroupMountPathHonorsMountRoot(t *testing.T) {
	mount := &mountinfo.Info{Root: "/kubepods.slice", Mountpoint: "/sys/fs/cgroup"}
	got, ok := resolveCgroupMountPath(mount, "/kubepods.slice/pod-1/container-2")
	if !ok || got != "/sys/fs/cgroup/pod-1/container-2" {
		t.Fatalf("resolveCgroupMountPath() = %q, %t", got, ok)
	}
	if _, ok := resolveCgroupMountPath(mount, "/other.slice/process"); ok {
		t.Fatal("path outside mount root must be rejected")
	}
}

func TestReadCgroupV2MemoryUsesTightestAncestor(t *testing.T) {
	mountpoint := t.TempDir()
	parent := filepath.Join(mountpoint, "parent")
	leaf := filepath.Join(parent, "leaf")
	if err := os.MkdirAll(leaf, 0o700); err != nil {
		t.Fatal(err)
	}
	writeCgroupV2Memory(t, mountpoint, "max", "0", "0")
	writeCgroupV2Memory(t, parent, "1000", "600", "100")
	writeCgroupV2Memory(t, leaf, "800", "200", "0")

	available, limited, err := readCgroupMemoryAvailable(cgroupMemoryLocation{
		version:    2,
		mountpoint: mountpoint,
		path:       leaf,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !limited || available != 500 {
		t.Fatalf("available = %d, limited=%t, want 500,true", available, limited)
	}

	limit, err := readCgroupMemoryLimit(cgroupMemoryLocation{version: 2, mountpoint: mountpoint, path: leaf})
	if err != nil || limit != 800 {
		t.Fatalf("limit = %d, %v, want the tightest finite limit 800", limit, err)
	}
	limit, err = readCgroupMemoryLimit(cgroupMemoryLocation{version: 2, mountpoint: mountpoint, path: mountpoint})
	if err != nil || limit != 0 {
		t.Fatalf("unlimited root limit = %d, %v, want 0", limit, err)
	}
}

func TestLinuxResourceProbeUsesMinimumOfHostAndCgroup(t *testing.T) {
	root := t.TempDir()
	meminfo := filepath.Join(root, "meminfo")
	if err := os.WriteFile(meminfo, []byte("MemAvailable: 700 kB\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cgroup := filepath.Join(root, "cgroup")
	if err := os.Mkdir(cgroup, 0o700); err != nil {
		t.Fatal(err)
	}
	writeCgroupV2Memory(t, cgroup, "716800", "307200", "102400") // 500 KiB available

	probe := &linuxResourceProbe{
		meminfoPath: meminfo,
		cgroupLocation: &cgroupMemoryLocation{
			version:    2,
			mountpoint: cgroup,
			path:       cgroup,
		},
	}
	probe.cgroupOnce.Do(func() {})
	available, err := probe.MemoryAvailableBytes()
	if err != nil {
		t.Fatal(err)
	}
	if want := uint64(500 * 1024); available != want {
		t.Fatalf("available = %d, want %d", available, want)
	}
}

// cgroup v2 的根节点没有 memory.max，未启用 memory 控制器的子树同样没有。这表示
// 该层不设限制，必须降级为宿主 MemAvailable 且不报错，否则会丢掉其他层级的真实限制。
func TestCgroupLevelWithoutLimitFileIsUnlimited(t *testing.T) {
	t.Run("v2", func(t *testing.T) {
		_, limited, err := readCgroupLevelMemory(2, t.TempDir())
		if err != nil || limited {
			t.Fatalf("readCgroupLevelMemory(v2) limited=%t err=%v, want false/nil", limited, err)
		}
	})
	t.Run("v1", func(t *testing.T) {
		_, limited, err := readCgroupLevelMemory(1, t.TempDir())
		if err != nil || limited {
			t.Fatalf("readCgroupLevelMemory(v1) limited=%t err=%v, want false/nil", limited, err)
		}
	})
}

// 进程位于根 cgroup 时（普通云主机的常见情况），内存保护应当安静地按宿主
// MemAvailable 判定，不应产生探测失败。
func TestRootCgroupFallsBackToHostMemoryWithoutError(t *testing.T) {
	mountpoint := t.TempDir()
	available, limited, err := readCgroupMemoryAvailable(cgroupMemoryLocation{
		version:    2,
		mountpoint: mountpoint,
		path:       mountpoint,
	})
	if err != nil {
		t.Fatalf("root cgroup must not be reported as a probe failure: %v", err)
	}
	if limited {
		t.Fatalf("root cgroup must be unlimited, got available=%d", available)
	}
}

// 运行目录中的临时文件随时可能被采集器删除，目录不存在时按 0 占用处理，
// 不能让磁盘配额因为探测抖动误判为超限。
func TestRunDirUsageTreatsMissingDirectoryAsEmpty(t *testing.T) {
	probe := &linuxResourceProbe{}
	used, err := probe.DirectoryUsageBytes(filepath.Join(t.TempDir(), "not-created", "leaf"))
	if err != nil {
		t.Fatal(err)
	}
	if used != 0 {
		t.Fatalf("missing run directory usage = %d, want 0", used)
	}
}

func writeCgroupV2Memory(t *testing.T, dir, maximum, current, inactive string) {
	t.Helper()
	files := map[string]string{
		"memory.max":     maximum + "\n",
		"memory.current": current + "\n",
		"memory.stat":    "inactive_file " + inactive + "\n",
	}
	for name, value := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
