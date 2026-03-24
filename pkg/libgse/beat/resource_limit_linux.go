// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package beat

import (
	"errors"
	"math"
	"os"
	"runtime"

	"github.com/containerd/cgroups"
	"github.com/containerd/cgroups/v3/cgroup2"
	"github.com/opencontainers/runtime-spec/specs-go"
)

// SetResourceLimit 设置进程 CPU 和内存资源限制
// Name: cgroup 名称; CPU: core; MEM: MB
func SetResourceLimit(name string, cpu float64, mem int) {
	if err := setLinuxCgroups(name, cpu, mem); err != nil {
		// CPU 核数向上取整 确保有核可用
		// 0.1 -> 1 core
		runtime.GOMAXPROCS(int(math.Ceil(cpu)))
		return
	}

	// 如果 cgroup 限制设置成功 则允许进程在所有核心上进行调度
	runtime.GOMAXPROCS(0)
}

func setLinuxCgroups(name string, cpu float64, mem int) error {
	mode := cgroups.Mode()
	switch mode {
	case cgroups.Legacy, cgroups.Hybrid:
		// v1 和 混合模式下选择 cgroup v1
		return setLinuxCgroupsV1(name, cpu, mem)
	case cgroups.Unified:
		// 仅支持 v2 模式下才选择 cgroup v2
		return setLinuxCgroupsV2(name, cpu, mem)
	default:
		return errors.New("no support cgroup mode")
	}
}

func setLinuxCgroupsV2(name string, cpu float64, mem int) error {
	var cpuResource *cgroup2.CPU
	var memResource *cgroup2.Memory

	// cpu * Core: 小于等于 0 表示 cgroup 无 CPU 限制
	var cpuPeriod uint64 = 100000
	cpuQuota := int64(cpu * float64(cpuPeriod))
	if cpuQuota > 0 {
		cpuResource = &cgroup2.CPU{Max: cgroup2.NewCPUMax(&cpuQuota, &cpuPeriod)}
	}
	if cpuQuota < 0 {
		cpuResource = &cgroup2.CPU{Max: "max"}
	}

	// mem * MB: 小于等于 0 表示 cgroup 无内存限制
	memLimit := int64(mem) * 1024 * 1024
	if memLimit > 0 {
		memResource = &cgroup2.Memory{Max: &memLimit}
	}

	// 无任何限制 直接返回
	if cpuResource == nil && memResource == nil {
		return nil
	}

	rs := &cgroup2.Resources{
		CPU:    cpuResource,
		Memory: memResource,
	}

	// 分组路径
	group := "/collector-" + name

	// cgroup2 Load 场景下不会失败
	// 统一使用 New 来管理
	mgr, err := cgroup2.NewManager("/sys/fs/cgroup", group, rs)
	if err != nil {
		return err
	}
	return mgr.AddProc(uint64(os.Getpid()))
}

func setLinuxCgroupsV1(name string, cpu float64, mem int) error {
	resource := &specs.LinuxResources{}
	var unlimited int64 = -1

	// cpu * Core: 小于等于 0 表示 cgroup 无 CPU 限制
	cpuQuota := int64(cpu * 100000)
	if cpuQuota > 0 {
		resource.CPU = &specs.LinuxCPU{Quota: &cpuQuota}
	}
	if cpuQuota < 0 {
		resource.CPU = &specs.LinuxCPU{Quota: &unlimited}
	}

	// mem * MB: 小于等于 0 表示 cgroup 无内存限制
	memLimit := int64(mem) * 1024 * 1024
	if memLimit > 0 {
		resource.Memory = &specs.LinuxMemory{Limit: &memLimit}
	}
	if memLimit < 0 {
		resource.Memory = &specs.LinuxMemory{Limit: &unlimited}
	}

	// 无任何限制 直接返回
	if resource.CPU == nil && resource.Memory == nil {
		return nil
	}

	// 静态路径
	staticPath := cgroups.StaticPath("/collector-" + name)

	// 先尝试加载原有的 cgroup
	cgroup, err := cgroups.Load(cgroups.V1, staticPath)
	// 加载成功
	if err == nil {
		// 先尝试更新 cgroup 配置 可能每次启动的时候限制资源数量会不同
		if err = cgroup.Update(resource); err != nil {
			return err
		}
		// 将进程号挂到 cgroup 下
		return cgroup.Add(cgroups.Process{Pid: os.Getpid()})
	}

	// 如果有问题 则尝试创建一个新的 cgroup 再挂载 实在还是不行那就木得办法了
	cgroup, err = cgroups.New(cgroups.V1, staticPath, resource)
	if err != nil {
		return err
	}

	// 创建成功 将进程号挂到 cgroup 下
	return cgroup.Add(cgroups.Process{Pid: os.Getpid()})
}
