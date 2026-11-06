// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/prometheus/procfs"
)

type topologyMemoryFiles struct{ limit, usage string }

var topologyMemoryHeadroom = readTopologyMemoryHeadroom
var topologyMemoryPaths = sync.OnceValue(func() []topologyMemoryFiles {
	self, err := procfs.Self()
	if err != nil {
		return nil
	}
	groups, err := self.Cgroups()
	if err != nil {
		return nil
	}
	mounts, err := self.MountInfo()
	if err != nil {
		return nil
	}
	return topologyCgroupMemoryPaths(groups, mounts)
})

// 检查所有可见祖先 cgroup 和匹配挂载。容器内无法核实隐藏祖先的限制；
// 即使发现不完整或不可用，配置的预留上限仍始终生效。
func topologyCgroupMemoryPaths(groups []procfs.Cgroup, mounts []*procfs.MountInfo) []topologyMemoryFiles {
	var result []topologyMemoryFiles
	seen := make(map[string]bool)
	for _, group := range groups {
		for _, mount := range mounts {
			v2 := mount.FSType == "cgroup2" && group.HierarchyID == 0
			_, memoryMount := mount.SuperOptions["memory"]
			v1 := mount.FSType == "cgroup" && memoryMount && slices.Contains(group.Controllers, "memory")
			if !v1 && !v2 {
				continue
			}
			// procfs 的 mountinfo 使用八进制转义，路径尚未解码。
			unescape := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
			root, point := unescape.Replace(mount.Root), unescape.Replace(mount.MountPoint)
			rel, err := filepath.Rel(root, group.Path)
			if group.Path == "/" {
				rel = "."
				err = nil
			} // private cgroup namespace
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				continue
			}
			for dir := filepath.Join(point, rel); ; dir = filepath.Dir(dir) {
				if !seen[dir] {
					seen[dir] = true
					limit, usage := "memory.max", "memory.current"
					if v1 {
						limit, usage = "memory.limit_in_bytes", "memory.usage_in_bytes"
					}
					result = append(result, topologyMemoryFiles{filepath.Join(dir, limit), filepath.Join(dir, usage)})
				}
				if dir == filepath.Clean(point) || filepath.Dir(dir) == dir {
					break
				}
			}
		}
	}
	return result
}

func topologyMemoryValue(path string) (int64, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	value, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	// v2 的 max 和 v1 接近 MaxInt64 的哨兵表示无限制，不能当作剩余内存。
	return value, err == nil && value >= 0 && value < 1<<60
}

func readTopologyMemoryHeadroom() (int64, bool) {
	var available int64
	known := false
	apply := func(value int64) {
		if !known || value < available {
			available = value
			known = true
		}
	}
	if fs, err := procfs.NewDefaultFS(); err == nil {
		if info, err := fs.Meminfo(); err == nil && info.MemAvailable != nil {
			apply(int64(*info.MemAvailable) * 1024)
		}
	}
	for _, files := range topologyMemoryPaths() {
		limit, ok := topologyMemoryValue(files.limit)
		if !ok {
			continue
		}
		usage, ok := topologyMemoryValue(files.usage)
		if !ok {
			continue
		}
		apply(max(int64(0), limit-usage))
	}
	return available, known
}
