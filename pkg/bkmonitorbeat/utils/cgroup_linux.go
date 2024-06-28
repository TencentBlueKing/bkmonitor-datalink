// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build linux

package utils

import (
	"errors"
	"os"

	"github.com/containerd/cgroups"
	"github.com/opencontainers/runtime-spec/specs-go"
)

func SetLinuxCGroup(name string, blockIO SpecBlockIO) error {
	resource := &specs.LinuxResources{
		BlockIO: &specs.LinuxBlockIO{},
	}

	if blockIO.Major == 0 && blockIO.Minor == 0 {
		return errors.New("empty block major/minor")
	}
	if blockIO.ReadBytes <= 0 && blockIO.WriteBytes <= 0 && blockIO.ReadIOps <= 0 && blockIO.WriteIOps <= 0 {
		return errors.New("empty block read/write limits")
	}

	// 读大小
	if blockIO.ReadBytes > 0 {
		rio := specs.LinuxThrottleDevice{}
		rio.Major = blockIO.Major
		rio.Minor = blockIO.Minor
		rio.Rate = blockIO.ReadBytes
		resource.BlockIO.ThrottleReadBpsDevice = append(resource.BlockIO.ThrottleReadBpsDevice, rio)
	}
	// 写大小
	if blockIO.WriteBytes > 0 {
		wio := specs.LinuxThrottleDevice{}
		wio.Major = blockIO.Major
		wio.Minor = blockIO.Minor
		wio.Rate = blockIO.WriteBytes
		resource.BlockIO.ThrottleWriteBpsDevice = append(resource.BlockIO.ThrottleWriteBpsDevice, wio)
	}
	// 读频率
	if blockIO.ReadIOps > 0 {
		rio := specs.LinuxThrottleDevice{}
		rio.Major = blockIO.Major
		rio.Minor = blockIO.Minor
		rio.Rate = blockIO.ReadIOps
		resource.BlockIO.ThrottleReadIOPSDevice = append(resource.BlockIO.ThrottleReadIOPSDevice, rio)
	}
	// 写频率
	if blockIO.WriteIOps > 0 {
		wio := specs.LinuxThrottleDevice{}
		wio.Major = blockIO.Major
		wio.Minor = blockIO.Minor
		wio.Rate = blockIO.WriteIOps
		resource.BlockIO.ThrottleWriteIOPSDevice = append(resource.BlockIO.ThrottleWriteIOPSDevice, wio)
	}

	// 静态路径
	staticPath := cgroups.StaticPath("/cgroup-bkmonitorbeat-" + name)

	// 先尝试加载原有的 cgroup
	cgroup, err := cgroups.Load(cgroups.V1, staticPath)
	// 加载成功
	if err == nil {
		// 先尝试更新 cgroup 配置 可能每次启动的时候限制资源数量会不同
		if err = cgroup.Update(resource); err != nil {
			return err
		}

		// 将进程号挂到 cgroup 下
		if err = cgroup.Add(cgroups.Process{Pid: os.Getpid()}); err != nil {
			return err
		}
		return nil
	}

	// 如果有问题 则尝试创建一个新的 cgroup 再挂载 实在还是不行那就木得办法了
	cgroup, err = cgroups.New(cgroups.V1, staticPath, resource)
	if err != nil {
		return err
	}

	// 创建成功 将进程号挂到 cgroup 下
	return cgroup.Add(cgroups.Process{Pid: os.Getpid()})
}
