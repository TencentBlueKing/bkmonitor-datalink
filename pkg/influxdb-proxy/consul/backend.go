// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consul

import (
	"encoding/json"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/logging"
)

const (
	// HostBasePath 集群主机基础路径
	HostBasePath = "host_info"
)

// HostPath 主机配置完整路径
var HostPath string

func initHostPath() {
	HostPath = TotalPrefix + "/" + HostBasePath
}

// GetHostInfo 获取主机信息
var GetHostInfo = func(host string) (*HostInfo, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Tracef("called,host:%s", host)
	path := TotalPrefix + "/" + HostBasePath + "/" + host
	res, err := consulClient.Get(path)
	if err != nil {
		flowLog.Errorf("get failed")
		return nil, err
	}
	if res == nil {
		flowLog.Errorf("res is nil")
		return nil, ErrPathDismatch
	}
	hi := new(HostInfo)
	err = json.Unmarshal(res.Value, hi)
	if err != nil {
		flowLog.Errorf("Unmarshal failed,error:%s", err)
		return nil, err
	}
	flowLog.Tracef("done")
	return hi, nil
}

// GetHostsName 获取全部主机的名称
var GetHostsName = func() ([]string, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Tracef("called")
	paths, err := consulClient.GetChild(TotalPrefix+"/"+HostBasePath, "/")
	if err != nil {
		flowLog.Errorf("get child failed")
		return nil, err
	}
	list := make([]string, len(paths))
	for index, path := range paths {
		list[index] = strings.Split(path, "/")[2]
	}
	flowLog.Tracef("done")
	return list, nil
}

// GetAllHostsData 获取全部主机信息
var GetAllHostsData = func() (map[string]*HostInfo, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Tracef("called")
	data, err := consulClient.GetPrefix(TotalPrefix+"/"+HostBasePath, "/")
	if err != nil {
		return nil, err
	}
	hostMap := make(map[string]*HostInfo)
	for _, kvPair := range data {
		hi, err := kvToHostInfo(kvPair)
		if err != nil {
			flowLog.Errorf("kvToHostInfo failed")
			return nil, err
		}
		host, err := formatHostPath(kvPair.Key)
		if err != nil {
			flowLog.Errorf("formatHostPath failed,error:%s", err)
			return nil, err
		}
		hostMap[host] = hi

	}
	flowLog.Tracef("done")
	return hostMap, nil
}

func formatHostPath(path string) (string, error) {
	host := strings.Replace(path, HostPath+"/", "", 1)
	host = strings.TrimSuffix(host, "/")
	return host, nil
}
