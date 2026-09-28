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

// Walk all visible ancestors and all matching mounts. Hidden ancestors cannot
// be certified from a container namespace; the configured reservation cap
// always applies, including when discovery is incomplete or unavailable.
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
			// procfs exposes mountinfo octal escapes, not decoded paths.
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
	// v2 'max' and v1's near-MaxInt64 sentinel are unlimited, not headroom.
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
