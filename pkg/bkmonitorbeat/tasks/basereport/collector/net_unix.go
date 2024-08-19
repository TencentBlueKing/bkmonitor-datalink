// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || zos

package collector

import (
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/shirou/gopsutil/v3/net"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	DevPath      = "/proc/net/dev"
	CollsIndex   = 13
	CarrierIndex = 14
	SumLength    = 16

	NetCoutnerMaxSize = math.MaxUint64
)

func ProtoCounters(protocols []string) ([]net.ProtoCountersStat, error) {
	return net.ProtoCounters(protocols)
}

func initVirtualInterfaceSet() error {
	interfaceSet := common.NewSet()
	fileList, err := os.ReadDir("/sys/devices/virtual/net")
	if err != nil {
		return err
	}
	for _, file := range fileList {
		interfaceSet.Insert(file.Name())
	}
	virtualInterfaceSet = interfaceSet
	return nil
}

// cat /proc/net/dev
func GetNetInfoFromDev() (map[string]NetInfo, error) {
	netInfo := make(map[string]NetInfo, 2)
	fileContent, err := os.ReadFile(DevPath)
	if err != nil {
		// freebsd无此文件，忽略报错
		if os.IsNotExist(err) {
			return netInfo, nil
		}
		return nil, err
	}

	strfiles := string(fileContent)
	lines := strings.Split(strfiles, "\n")[2:]
	var lasterr error
	for _, line := range lines {
		var netinfoItem NetInfo
		deviName := strings.Split(line, ":")
		name := strings.TrimSpace(deviName[0])
		if name == "" {
			continue
		}

		logger.Debugf("net name :%s", name)
		results := strings.Fields(deviName[1])
		if len(results) != SumLength {
			continue
		}

		colls := results[CollsIndex]
		netinfoItem.Carrier, err = strconv.ParseUint(strings.TrimSpace(colls), 10, 0)
		if err != nil {
			lasterr = err
		}

		carrier := results[CarrierIndex]
		netinfoItem.Collisions, err = strconv.ParseUint(strings.TrimSpace(carrier), 10, 0)
		if err != nil {
			lasterr = err
		}

		logger.Infof("get interface->[%s] info colls->[%s] carries->[%s]", name, colls, carrier)
		netInfo[name] = netinfoItem
	}
	return netInfo, lasterr
}
