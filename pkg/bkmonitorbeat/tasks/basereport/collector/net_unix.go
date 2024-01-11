// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build aix || dragonfly || linux || netbsd || openbsd || solaris || zos

package collector

import (
	"os"

	"github.com/shirou/gopsutil/v3/net"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/common"
)

const (
	pathVirtualNet = "/sys/devices/virtual/net"
)

func ProtoCounters(protocols []string) ([]net.ProtoCountersStat, error) {
	return net.ProtoCounters(protocols)
}

func InitVirtualInterfaceSet() error {
	entities, err := os.ReadDir(pathVirtualNet)
	if err != nil {
		return err
	}

	interfaceSet := common.NewSet()
	for _, entity := range entities {
		interfaceSet.Insert(entity.Name())
	}
	virtualInterfaceSet = interfaceSet
	return nil
}
