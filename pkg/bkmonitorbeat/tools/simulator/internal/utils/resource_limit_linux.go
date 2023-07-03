// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package utils

import (
	"fmt"
	"os"

	"github.com/containerd/cgroups"
	"github.com/containerd/cgroups/v3/cgroup2"
	"github.com/opencontainers/runtime-spec/specs-go"
)

func setResourceLimit(name string, cpu float64, mem int64) error {
	if cgroups.Mode() == cgroups.Unified {
		deleteFunc = deleteFuncV1
		return setResourceLimitV2(name, cpu, mem)
	} else {
		deleteFunc = deleteFuncV2
		return setResourceLimitV1(name, cpu, mem)
	}
}

func deleteFuncV1(r interface{}) error {
	if c, ok := r.(cgroups.Cgroup); ok {
		return c.Delete()
	}
	return nil
}

func setResourceLimitV1(name string, cpu float64, mem int64) error {
	res := &specs.LinuxResources{}
	if cpu > 0 {
		var period uint64 = 100000
		var quota = int64(cpu * float64(period))
		res.CPU = &specs.LinuxCPU{
			Quota:  &quota,
			Period: &period,
		}
	}
	if mem > 0 {
		res.Memory = &specs.LinuxMemory{
			Limit: &mem,
		}
	}
	c, err := cgroups.Load(cgroups.V1, cgroups.StaticPath(fmt.Sprintf("/%s", name)))
	if err != nil {
		c, err = cgroups.New(cgroups.V1, cgroups.StaticPath(fmt.Sprintf("/%s", name)), res)
		if err != nil {
			return err
		}
		storeRMap(name, c)
	} else {
		storeRMap(name, c)
		err = c.Update(res)
		if err != nil {
			return err
		}
	}

	return c.Add(cgroups.Process{Pid: os.Getpid()})
}

func deleteFuncV2(r interface{}) error {
	if m, ok := r.(*cgroup2.Manager); ok {
		return m.DeleteSystemd()
	}
	return nil
}

func newSystemd(sliceName string, res *cgroup2.Resources) (*cgroup2.Manager, error) {
	m, err := cgroup2.NewSystemd("/", sliceName, -1, res)
	if err != nil {
		return nil, err
	}

	err = m.Update(res)
	return m, err
}

func setResourceLimitV2(name string, cpu float64, mem int64) error {
	res := &cgroup2.Resources{}
	if cpu > 0 {
		var period uint64 = 100000
		var quota = int64(cpu * float64(period))
		res.CPU = &cgroup2.CPU{
			Max: cgroup2.NewCPUMax(&quota, &period),
		}
	}
	if mem > 0 {
		res.Memory = &cgroup2.Memory{
			Max: &mem,
		}
	}
	sliceName := name + ".slice"
	//存在则使用，不存在则新建
	m, err := cgroup2.LoadSystemd("/", sliceName)
	if err != nil {
		m, err = newSystemd(sliceName, res)
		if err != nil {
			return err
		}
	} else {
		err = m.Update(res)
		if err != nil {
			m, err = newSystemd(sliceName, res)
			if err != nil {
				return err
			}
		}
	}
	storeRMap(name, m)

	return m.AddProc(uint64(os.Getpid()))
}
