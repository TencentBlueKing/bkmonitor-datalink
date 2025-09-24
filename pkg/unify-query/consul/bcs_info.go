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
	"context"
	"regexp"
	"sync"

	"github.com/hashicorp/consul/api"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

var bcsInfo = &BCSInfo{Mutex: new(sync.Mutex)}

// BCSInfo projectID: clusterID -> []dataIDList
type BCSInfo struct {
	*sync.Mutex
	// {"project_id": [{"cluster_id": [data_id]}]}
	info map[string]map[string][]DataID
	// {"cluster_id": [data_id]}
	clusterInfo map[string][]DataID
}

// GetBcsInfo get bcs info
func GetBcsInfo() *BCSInfo {
	if len(bcsInfo.info) == 0 {
		return &BCSInfo{Mutex: new(sync.Mutex)}
	}
	return bcsInfo
}

// GetRouterByProjectID 通过 projectID 获取 DataID
func (b *BCSInfo) GetRouterByProjectID(ids ...string) []DataID {
	var result []DataID
	for _, id := range ids {
		project, ok := b.info[id]
		if !ok {
			continue
		}
		for _, dataIDs := range project {
			result = append(result, dataIDs...)
		}
	}
	return result
}

// GetRouterByClusterID 通过 clusterID 获取 DataID
func (b *BCSInfo) GetRouterByClusterID(ids ...string) []DataID {
	var result []DataID
	for _, id := range ids {
		if val, ok := b.clusterInfo[id]; ok {
			result = append(result, val...)
		}
	}

	return result
}

// ReloadBCSInfo 从consul获取router信息
func ReloadBCSInfo() error {
	kv, err := GetDataWithPrefix(BCSInfoPath)
	if err != nil {
		return err
	}
	return FormatBCSInfo(kv)
}

// FormatBCSInfo 格式化 consul 信息
func FormatBCSInfo(kvPairs api.KVPairs) error {
	var (
		tmpBCSInfo     = make(map[string]map[string][]DataID, 0)
		tmpClusterInfo = make(map[string][]DataID, 0)
		reg            = regexp.MustCompile("project_id/(.*)/cluster_id/(.*)/?")
	)

	for _, kvPair := range kvPairs {
		s := reg.FindStringSubmatch(kvPair.Key)
		// [project_id/xxxx/cluster_id/bcs-bcs-k8s-xxxx xxxx xxxx]
		// 不符合规范则跳过
		if len(s) != 3 {
			continue
		}

		projectID := s[1]
		clusterID := s[2]

		dataIDList := make([]DataID, 0)
		if err := json.Unmarshal(kvPair.Value, &dataIDList); err != nil {
			return err
		}

		if _, ok := tmpBCSInfo[projectID]; !ok {
			tmpBCSInfo[projectID] = make(map[string][]DataID, 0)
		}

		tmpBCSInfo[projectID][clusterID] = append(tmpBCSInfo[projectID][clusterID], dataIDList...)
		tmpClusterInfo[clusterID] = append(tmpClusterInfo[clusterID], dataIDList...)
	}

	bcsInfo.Lock()
	defer bcsInfo.Unlock()
	bcsInfo.info = tmpBCSInfo
	bcsInfo.clusterInfo = tmpClusterInfo
	log.Debugf(context.TODO(), "set bcs info: %v", bcsInfo.info)
	log.Debugf(context.TODO(), "set bcs cluster info: %v", bcsInfo.clusterInfo)
	return nil
}

// WatchBCSInfo 监听consul路径，拿到es和influxdb等对应的查询信息
var WatchBCSInfo = func(ctx context.Context) (<-chan any, error) {
	path := BCSInfoPath
	// 多个查询服务都需要此监听开启，但只运行一次就可以
	return WatchChangeOnce(ctx, path, "/")
}
