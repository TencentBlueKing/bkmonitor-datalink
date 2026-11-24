// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License").

//go:build linux

package executor

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/moby/sys/mountinfo"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const cgroupUnlimitedV1 = uint64(1) << 60

type cgroupMemoryLocation struct {
	version    int
	mountpoint string
	path       string
}

type linuxResourceProbe struct {
	directoryUsageProbe

	meminfoPath    string
	selfCgroupPath string

	cgroupOnce     sync.Once
	cgroupLocation *cgroupMemoryLocation
	cgroupError    error
	cgroupWarnOnce sync.Once
}

func newHostResourceProbe() resourceProbe {
	return &linuxResourceProbe{
		meminfoPath:    "/proc/meminfo",
		selfCgroupPath: "/proc/self/cgroup",
	}
}

func (p *linuxResourceProbe) MemoryAvailableBytes() (uint64, error) {
	hostAvailable, err := readHostMemoryAvailable(p.meminfoPath)
	if err != nil {
		return 0, err
	}

	p.discoverCgroup()
	if p.cgroupError != nil {
		p.cgroupWarnOnce.Do(func() {
			logger.Warnf("[active_profile] resource guard cgroup memory detection degraded to host MemAvailable: %v", p.cgroupError)
		})
		return hostAvailable, nil
	}
	if p.cgroupLocation == nil {
		return hostAvailable, nil
	}
	cgroupAvailable, limited, err := readCgroupMemoryAvailable(*p.cgroupLocation)
	if err != nil {
		p.cgroupWarnOnce.Do(func() {
			logger.Warnf("[active_profile] resource guard cgroup memory read degraded to host MemAvailable: %v", err)
		})
		return hostAvailable, nil
	}
	if limited && cgroupAvailable < hostAvailable {
		return cgroupAvailable, nil
	}
	return hostAvailable, nil
}

// MemoryLimitBytes 与 MemoryAvailableBytes 一样在 cgroup 探测失败时退化为没有上限。
func (p *linuxResourceProbe) MemoryLimitBytes() uint64 {
	p.discoverCgroup()
	if p.cgroupError != nil || p.cgroupLocation == nil {
		return 0
	}
	limit, err := readCgroupMemoryLimit(*p.cgroupLocation)
	if err != nil {
		return 0
	}
	return limit
}

func (p *linuxResourceProbe) discoverCgroup() {
	p.cgroupOnce.Do(func() {
		p.cgroupLocation, p.cgroupError = discoverCgroupMemoryLocation(p.selfCgroupPath)
	})
}

func readHostMemoryAvailable(path string) (uint64, error) {
	values, err := readMeminfo(path)
	if err != nil {
		return 0, err
	}
	if value, ok := values["MemAvailable"]; ok {
		return kibToBytes(value)
	}

	// Linux < 3.14 may not expose MemAvailable. This approximation follows
	// the same reclaimable-memory shape without counting swap.
	memFree, freeOK := values["MemFree"]
	buffers, buffersOK := values["Buffers"]
	cached, cachedOK := values["Cached"]
	if !freeOK || !buffersOK || !cachedOK {
		return 0, errors.New("/proc/meminfo has neither MemAvailable nor fallback fields")
	}
	available := saturatingAdd(memFree, buffers)
	available = saturatingAdd(available, cached)
	available = saturatingAdd(available, values["SReclaimable"])
	shmem := values["Shmem"]
	if shmem >= available {
		available = 0
	} else {
		available -= shmem
	}
	return kibToBytes(available)
}

func readMeminfo(path string) (map[string]uint64, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()

	values := make(map[string]uint64)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		value, parseErr := strconv.ParseUint(fields[1], 10, 64)
		if parseErr != nil {
			continue
		}
		values[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", path, err)
	}
	return values, nil
}

func kibToBytes(value uint64) (uint64, error) {
	if value > math.MaxUint64/1024 {
		return 0, errors.New("memory value overflows bytes")
	}
	return value * 1024, nil
}

func discoverCgroupMemoryLocation(cgroupPath string) (*cgroupMemoryLocation, error) {
	raw, err := os.ReadFile(cgroupPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", cgroupPath, err)
	}
	v2Path, v1Path := parseSelfCgroup(string(raw))
	mounts, err := mountinfo.GetMounts(nil)
	if err != nil {
		return nil, fmt.Errorf("read mountinfo: %w", err)
	}
	if v2Path != "" {
		for _, mount := range mounts {
			if mount.FSType != "cgroup2" {
				continue
			}
			resolved, ok := resolveCgroupMountPath(mount, v2Path)
			if ok {
				return &cgroupMemoryLocation{version: 2, mountpoint: mount.Mountpoint, path: resolved}, nil
			}
		}
	}
	if v1Path != "" {
		for _, mount := range mounts {
			if mount.FSType != "cgroup" || !commaListContains(mount.VFSOptions, "memory") {
				continue
			}
			resolved, ok := resolveCgroupMountPath(mount, v1Path)
			if ok {
				return &cgroupMemoryLocation{version: 1, mountpoint: mount.Mountpoint, path: resolved}, nil
			}
		}
	}
	return nil, nil
}

func parseSelfCgroup(raw string) (v2Path, v1MemoryPath string) {
	for _, line := range strings.Split(raw, "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), ":", 3)
		if len(parts) != 3 {
			continue
		}
		if parts[0] == "0" && parts[1] == "" {
			v2Path = parts[2]
		}
		if commaListContains(parts[1], "memory") {
			v1MemoryPath = parts[2]
		}
	}
	return v2Path, v1MemoryPath
}

func commaListContains(raw, expected string) bool {
	for _, value := range strings.Split(raw, ",") {
		if strings.TrimSpace(value) == expected {
			return true
		}
	}
	return false
}

func resolveCgroupMountPath(mount *mountinfo.Info, cgroupPath string) (string, bool) {
	cgroupPath = filepath.Clean("/" + strings.TrimPrefix(cgroupPath, "/"))
	root := filepath.Clean("/" + strings.TrimPrefix(mount.Root, "/"))
	var relative string
	switch {
	case root == "/":
		relative = strings.TrimPrefix(cgroupPath, "/")
	case cgroupPath == root:
		relative = ""
	case strings.HasPrefix(cgroupPath, root+"/"):
		relative = strings.TrimPrefix(cgroupPath, root+"/")
	default:
		return "", false
	}
	resolved := filepath.Join(mount.Mountpoint, relative)
	if resolved != filepath.Clean(mount.Mountpoint) &&
		!strings.HasPrefix(resolved, filepath.Clean(mount.Mountpoint)+string(os.PathSeparator)) {
		return "", false
	}
	return resolved, true
}

func readCgroupMemoryAvailable(location cgroupMemoryLocation) (uint64, bool, error) {
	limited := false
	available := uint64(math.MaxUint64)
	err := walkCgroupLevels(location, func(path string) error {
		value, finite, err := readCgroupLevelMemory(location.version, path)
		if err != nil {
			return err
		}
		if finite {
			limited = true
			if value < available {
				available = value
			}
		}
		return nil
	})
	if err != nil {
		return 0, false, err
	}
	return available, limited, nil
}

// readCgroupMemoryLimit 返回 cgroup 链路上最小的有限内存上限，没有上限时为 0。
func readCgroupMemoryLimit(location cgroupMemoryLocation) (uint64, error) {
	var limit uint64
	err := walkCgroupLevels(location, func(path string) error {
		value, finite, err := readCgroupLevelLimit(location.version, path)
		if err != nil {
			return err
		}
		if finite && (limit == 0 || value < limit) {
			limit = value
		}
		return nil
	})
	return limit, err
}

// walkCgroupLevels 从 location 所在层逐级向上访问到挂载点。
func walkCgroupLevels(location cgroupMemoryLocation, visit func(path string) error) error {
	current := filepath.Clean(location.path)
	mountpoint := filepath.Clean(location.mountpoint)
	for {
		if err := visit(current); err != nil {
			return err
		}
		if current == mountpoint {
			return nil
		}
		parent := filepath.Dir(current)
		if parent == current || (parent != mountpoint && !strings.HasPrefix(parent, mountpoint+string(os.PathSeparator))) {
			return fmt.Errorf("cgroup path %s escapes mountpoint %s", current, mountpoint)
		}
		current = parent
	}
}

// readCgroupLevelLimit 读取单层 cgroup 的内存上限。限制文件不存在表示该层不设限制，
// 而不是探测失败：cgroup v2 的根节点本身就没有 memory.max，未启用 memory 控制器的
// 子树同样没有。把这种情况当成错误会让整棵 cgroup 链路被放弃，从而丢掉真实存在于
// 其他层级的限制。
func readCgroupLevelLimit(version int, path string) (uint64, bool, error) {
	if version == 2 {
		maximumRaw, err := readTrimmedFile(filepath.Join(path, "memory.max"))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return 0, false, nil
			}
			return 0, false, err
		}
		if maximumRaw == "max" {
			return 0, false, nil
		}
		maximum, err := strconv.ParseUint(maximumRaw, 10, 64)
		if err != nil {
			return 0, false, fmt.Errorf("parse %s/memory.max: %w", path, err)
		}
		return maximum, true, nil
	}

	maximum, err := readUintFile(filepath.Join(path, "memory.limit_in_bytes"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, false, nil
		}
		return 0, false, err
	}
	if maximum >= cgroupUnlimitedV1 {
		return 0, false, nil
	}
	return maximum, true, nil
}

// readCgroupLevelMemory 读取单层 cgroup 在限制内的可用内存，该层不设限制时 finite 为 false。
func readCgroupLevelMemory(version int, path string) (uint64, bool, error) {
	maximum, finite, err := readCgroupLevelLimit(version, path)
	if err != nil || !finite {
		return 0, false, err
	}
	if version == 2 {
		current, err := readUintFile(filepath.Join(path, "memory.current"))
		if err != nil {
			return 0, false, err
		}
		inactive := readMemoryStatValue(filepath.Join(path, "memory.stat"), "inactive_file")
		return availableFromLimit(maximum, current, inactive), true, nil
	}

	current, err := readUintFile(filepath.Join(path, "memory.usage_in_bytes"))
	if err != nil {
		return 0, false, err
	}
	inactive := readMemoryStatValue(filepath.Join(path, "memory.stat"), "total_inactive_file")
	if inactive == 0 {
		inactive = readMemoryStatValue(filepath.Join(path, "memory.stat"), "inactive_file")
	}
	return availableFromLimit(maximum, current, inactive), true, nil
}

func availableFromLimit(limit, usage, inactiveFile uint64) uint64 {
	workingSet := usage
	if inactiveFile < workingSet {
		workingSet -= inactiveFile
	} else {
		workingSet = 0
	}
	if workingSet >= limit {
		return 0
	}
	return limit - workingSet
}

func readMemoryStatValue(path, key string) uint64 {
	file, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 || fields[0] != key {
			continue
		}
		value, _ := strconv.ParseUint(fields[1], 10, 64)
		return value
	}
	return 0
}

func readUintFile(path string) (uint64, error) {
	raw, err := readTrimmedFile(path)
	if err != nil {
		return 0, err
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", path, err)
	}
	return value, nil
}

func readTrimmedFile(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return strings.TrimSpace(string(raw)), nil
}
