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
	// ClusterBasePath 集群route基础路径
	ClusterBasePath = "cluster_info"
)

// :
var (
	ClusterPath string
)

func initClusterPath() {
	ClusterPath = TotalPrefix + "/" + ClusterBasePath
}

// GetClusterInfo 获取集群信息
var GetClusterInfo = func(cluster string) (*ClusterInfo, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Tracef("called,cluster:%s", cluster)
	path := TotalPrefix + "/" + ClusterBasePath + "/" + cluster
	res, err := consulClient.Get(path)
	if err != nil {
		flowLog.Errorf("get failed")
		return nil, err
	}
	if res == nil {
		flowLog.Errorf("res is nil")
		return nil, ErrPathDismatch
	}
	ci := new(ClusterInfo)
	err = json.Unmarshal(res.Value, ci)
	if err != nil {
		flowLog.Errorf("Unmarshal failed,error:%s", err)
		return nil, err
	}
	flowLog.Tracef("done")
	return ci, nil
}

// GetClustersName 获取全部cluster的名称
var GetClustersName = func() ([]string, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Tracef("called")
	paths, err := consulClient.GetChild(TotalPrefix+"/"+ClusterBasePath, "/")
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

// GetAllClustersData 获取全部集群信息
var GetAllClustersData = func() (map[string]*ClusterInfo, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	flowLog.Tracef("called")
	data, err := consulClient.GetPrefix(TotalPrefix+"/"+ClusterBasePath, "/")
	if err != nil {
		return nil, err
	}
	clusterMap := make(map[string]*ClusterInfo)
	for _, kvPair := range data {
		ci, err := kvToClusterInfo(kvPair)
		if err != nil {
			flowLog.Errorf("kvToClusterInfo failed")
			return nil, err
		}

		cluster := formatClusterPath(kvPair.Key)
		clusterMap[cluster] = ci

	}
	flowLog.Tracef("done")
	return clusterMap, nil
}

func formatClusterPath(path string) string {
	cluster := strings.Replace(path, ClusterPath+"/", "", 1)
	cluster = strings.TrimSuffix(cluster, "/")
	return cluster
}
