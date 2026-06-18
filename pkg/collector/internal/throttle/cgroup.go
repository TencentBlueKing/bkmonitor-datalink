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
	"bufio"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	defaultProcRoot          = "/proc"
	defaultCgroupRoot        = "/sys/fs/cgroup"
	defaultCPUMaxPeriodUS    = 100000
	unlimitedMemoryThreshold = 1 << 60
)

// 当前实现只读当前进程所在 cgroup 的水位
//
// 经过调研，没有一个库能完全满足需求，用于读取水位信息：
//
// containerd/cgroups
// - 定位：cgroup 管理库并用于运行时创建和加载 cgroup。
// - 局限：cgroup v2 容器继承父级限制，会使获取到高估的 CPU 或内存，而且该库不支持获取 CPU 配额。
//
// automaxprocs / GOMAXPROCS
// - 定位：自动设置 runtime.GOMAXPROCS 并服务 Go 调度器。
// - 局限：quota 向下取整，不太准确。
//
// gopsutil
// - 定位：通用主机和进程指标库
// - 局限：获取宿主机 CPU 和内存情况而非容器配额下的使用率。
//
// 最终参考 VictoriaMetrics 的 lib/cgroup 模块：https://github.com/VictoriaMetrics/VictoriaMetrics/tree/master/lib/cgroup

// Reader 只读当前进程所在 cgroup 的水位。
type Reader interface {
	EffectiveCores() (float64, bool)
	CPUUsageNanos() (uint64, error)
	MemWorkingSet() (uint64, bool)
	MemLimit() (uint64, bool)
}

type CgroupReader struct {
	cgroupRoot string
	procRoot   string
	layout     cgroupLayout
}

type cgroupLayout struct {
	v2Mount          string
	v2Path           string
	controllerMounts map[string]string
	controllerPaths  map[string]string
}

func NewCgroupReader() *CgroupReader {
	return newCgroupReaderWithRoots(defaultCgroupRoot, defaultProcRoot)
}

func newCgroupReaderWithRoots(cgroupRoot, procRoot string) *CgroupReader {
	r := &CgroupReader{
		cgroupRoot: cgroupRoot,
		procRoot:   procRoot,
	}
	r.layout = r.loadLayout()
	return r
}

// EffectiveCores 获取有效的 CPU 核数。
func (r *CgroupReader) EffectiveCores() (float64, bool) {
	// CPU 水位必须按容器配额归一化。没有 quota 时返回 false，让 sampler 使用 fallback_cores。
	quota, quotaOK := r.cpuQuotaCores()
	if !quotaOK {
		return 0, false
	}

	cpuset, cpusetOK := r.cpusetCores()
	if cpusetOK {
		// cpuset 决定进程允许运行在哪几个物理核上(如 0-3)，是整数个核的「并行宽度」上限，硬限制。
		return math.Min(quota, cpuset), true
	}
	return quota, true
}

// CPUUsageNanos 获取当前 CPU 累计耗时（纳秒）。
func (r *CgroupReader) CPUUsageNanos() (uint64, error) {
	if usage, ok := readKeyUintFirst(r.candidatePaths("cpu", "cpu.stat"), "usage_usec"); ok {
		return usage * 1000, nil
	}
	if usage, ok := r.readFirstUint("cpuacct", "cpuacct.usage"); ok {
		return usage, nil
	}
	return 0, errors.New("failed to read cgroup cpu usage")
}

// MemWorkingSet 获取内存当前用量（不含 Cache）。
func (r *CgroupReader) MemWorkingSet() (uint64, bool) {
	// working set 对齐 cadvisor 口径：当前用量扣掉可回收的 inactive file cache。
	current, ok := r.readFirstUint("memory", "memory.current")
	if !ok {
		current, ok = r.readFirstUint("memory", "memory.usage_in_bytes")
	}
	if !ok {
		return 0, false
	}

	inactive, _ := r.readMemoryStatInactiveFile()
	if current <= inactive {
		return 0, true
	}
	return current - inactive, true
}

// MemLimit 获取内存容量
func (r *CgroupReader) MemLimit() (uint64, bool) {
	if limit, ok := r.readFirstUint("memory", "memory.limit_in_bytes"); ok {
		if limit == 0 || limit > unlimitedMemoryThreshold {
			return 0, false
		}
		return limit, true
	}
	return r.memLimitV2()
}

func (r *CgroupReader) cpuQuotaCores() (float64, bool) {
	quota, quotaOK := r.readFirstInt("cpu", "cpu.cfs_quota_us")
	period, periodOK := r.readFirstInt("cpu", "cpu.cfs_period_us")
	if quotaOK && periodOK && quota > 0 && period > 0 {
		return float64(quota) / float64(period), true
	}
	return r.cpuQuotaCoresV2()
}

func (r *CgroupReader) cpuQuotaCoresV2() (float64, bool) {
	// cgroup v2 的限制可能挂在父级 slice 上，沿层级向上取最小值才是真正上限。
	var minQuota float64
	for _, candidate := range r.cgroup2HierarchyPaths("cpu.max") {
		data, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		quota, ok := parseCPUMax(string(data))
		if ok && (minQuota == 0 || quota < minQuota) {
			minQuota = quota
		}
	}
	return minQuota, minQuota > 0
}

func parseCPUMax(value string) (float64, bool) {
	// cpu.max 允许只写 quota；period 缺省时内核按 100000us 处理。
	fields := strings.Fields(value)
	if len(fields) == 0 || len(fields) > 2 || fields[0] == "max" {
		return 0, false
	}

	quota, quotaErr := strconv.ParseFloat(fields[0], 64)
	period := float64(defaultCPUMaxPeriodUS)
	if len(fields) == 2 {
		var periodErr error
		period, periodErr = strconv.ParseFloat(fields[1], 64)
		if periodErr != nil {
			return 0, false
		}
	}
	if quotaErr != nil || quota <= 0 || period <= 0 {
		return 0, false
	}
	return quota / period, true
}

func (r *CgroupReader) memLimitV2() (uint64, bool) {
	var minLimit uint64
	for _, candidate := range r.cgroup2HierarchyPaths("memory.max") {
		data, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		value := strings.TrimSpace(string(data))
		if value == "max" {
			continue
		}
		limit, err := strconv.ParseUint(value, 10, 64)
		if err == nil && limit > 0 && (minLimit == 0 || limit < minLimit) {
			minLimit = limit
		}
	}
	return minLimit, minLimit > 0
}

func (r *CgroupReader) cpusetCores() (float64, bool) {
	for _, name := range []string{"cpuset.cpus.effective", "cpuset.cpus"} {
		if value, ok := r.readFirstString("cpuset", name); ok {
			n := countCPUSet(value)
			if n > 0 {
				return float64(n), true
			}
		}
	}
	return 0, false
}

func countCPUSet(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	total := 0
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		bounds := strings.Split(part, "-")
		if len(bounds) == 1 {
			if _, err := strconv.Atoi(bounds[0]); err == nil {
				total++
			}
			continue
		}
		if len(bounds) != 2 {
			continue
		}
		start, startErr := strconv.Atoi(bounds[0])
		end, endErr := strconv.Atoi(bounds[1])
		if startErr != nil || endErr != nil || end < start {
			continue
		}
		total += end - start + 1
	}
	return total
}

func (r *CgroupReader) readMemoryStatInactiveFile() (uint64, bool) {
	// v2
	if value, ok := readKeyUintFirst(r.cgroup2CandidatePaths("memory.stat"), "inactive_file"); ok {
		return value, true
	}
	// v1
	if value, ok := readKeyUintFirst(r.controllerCandidatePaths("memory", "memory.stat"), "total_inactive_file"); ok {
		return value, true
	}
	for _, candidate := range r.fallbackCandidatePaths("memory", "memory.stat") {
		if value, ok := readKeyUint(candidate, "inactive_file"); ok {
			return value, true
		}
		if value, ok := readKeyUint(candidate, "total_inactive_file"); ok {
			return value, true
		}
	}
	return 0, false
}

func (r *CgroupReader) readFirstUint(controller, file string) (uint64, bool) {
	for _, candidate := range r.candidatePaths(controller, file) {
		data, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		value, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
		if err == nil {
			return value, true
		}
	}
	return 0, false
}

func (r *CgroupReader) readFirstInt(controller, file string) (int64, bool) {
	for _, candidate := range r.candidatePaths(controller, file) {
		data, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		value, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
		if err == nil {
			return value, true
		}
	}
	return 0, false
}

func (r *CgroupReader) readFirstString(controller, file string) (string, bool) {
	for _, candidate := range r.candidatePaths(controller, file) {
		data, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		return string(data), true
	}
	return "", false
}

func (r *CgroupReader) candidatePaths(controller, file string) []string {
	// 顺序对齐 VM lib/cgroup：先看挂载根，兜住 leaf 被 bind-mount 成根的容器布局。
	var candidates []string
	candidates = append(candidates, r.cgroup2CandidatePaths(file)...)
	candidates = append(candidates, r.controllerCandidatePaths(controller, file)...)
	candidates = append(candidates, r.fallbackCandidatePaths(controller, file)...)
	return uniquePaths(candidates)
}

func (r *CgroupReader) cgroup2CandidatePaths(file string) []string {
	if r.layout.v2Mount == "" {
		return nil
	}
	return uniquePaths([]string{
		filepath.Join(r.layout.v2Mount, r.layout.v2Path, file),
		filepath.Join(r.layout.v2Mount, file),
	})
}

func (r *CgroupReader) cgroup2HierarchyPaths(file string) []string {
	if r.layout.v2Mount == "" {
		return nil
	}

	subPath := r.layout.v2Path
	if subPath == "" {
		subPath = "/"
	}

	var candidates []string
	for {
		candidates = append(candidates, filepath.Join(r.layout.v2Mount, subPath, file))
		if subPath == "/" || subPath == "." {
			break
		}
		subPath = filepath.Dir(subPath)
	}
	return uniquePaths(candidates)
}

func (r *CgroupReader) controllerCandidatePaths(controller, file string) []string {
	mount := r.layout.controllerMounts[controller]
	if mount == "" {
		return nil
	}
	return uniquePaths([]string{
		filepath.Join(mount, file),
		filepath.Join(mount, r.layout.controllerPaths[controller], file),
	})
}

func (r *CgroupReader) fallbackCandidatePaths(controller, file string) []string {
	return uniquePaths([]string{
		filepath.Join(r.cgroupRoot, file),
		filepath.Join(r.cgroupRoot, controller, file),
	})
}

func uniquePaths(input []string) []string {
	seen := make(map[string]struct{})
	var candidates []string
	for _, p := range input {
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		candidates = append(candidates, p)
	}
	return candidates
}

func (r *CgroupReader) loadLayout() cgroupLayout {
	layout := cgroupLayout{
		controllerMounts: make(map[string]string),
		controllerPaths:  make(map[string]string),
	}
	for controller, p := range parseProcCgroup(filepath.Join(r.procRoot, "self", "cgroup")) {
		layout.controllerPaths[controller] = p
	}
	layout.v2Path = layout.controllerPaths[""]

	for _, mount := range parseMountInfo(filepath.Join(r.procRoot, "self", "mountinfo")) {
		switch mount.fsType {
		case "cgroup2":
			layout.v2Mount = mount.mountPoint
		case "cgroup":
			for _, controller := range mount.controllers {
				layout.controllerMounts[controller] = mount.mountPoint
			}
		}
	}
	return layout
}

type cgroupMount struct {
	mountPoint  string
	fsType      string
	controllers []string
}

func parseMountInfo(filename string) []cgroupMount {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil
	}
	var mounts []cgroupMount
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		sep := -1
		for i, field := range fields {
			if field == "-" {
				sep = i
				break
			}
		}
		if sep < 0 || sep+3 >= len(fields) || len(fields) < 5 {
			continue
		}
		fsType := fields[sep+1]
		if fsType != "cgroup" && fsType != "cgroup2" {
			continue
		}
		mountPoint := fields[4]
		mount := cgroupMount{
			mountPoint: mountPoint,
			fsType:     fsType,
		}
		if fsType == "cgroup" {
			mount.controllers = strings.Split(fields[sep+3], ",")
		}
		mounts = append(mounts, mount)
	}
	return mounts
}

func parseProcCgroup(filename string) map[string]string {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil
	}
	paths := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		for _, controller := range strings.Split(parts[1], ",") {
			paths[controller] = parts[2]
		}
	}
	return paths
}

func readKeyUintFirst(filenames []string, key string) (uint64, bool) {
	for _, filename := range filenames {
		if value, ok := readKeyUint(filename, key); ok {
			return value, true
		}
	}
	return 0, false
}

func readKeyUint(filename, key string) (uint64, bool) {
	file, err := os.Open(filename)
	if err != nil {
		return 0, false
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 || fields[0] != key {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err == nil {
			return value, true
		}
	}
	return 0, false
}
