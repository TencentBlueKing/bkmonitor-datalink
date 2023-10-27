// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cluster

import (
	"context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/backend"
)

// NewClusterFunc Backend生成方法，生成一个指定类型的Backend
type NewClusterFunc func(ctx context.Context, name string, allBackendList []backend.Backend, unreadableHostMap map[string]bool) (Cluster, error)

// 存储所有生成方法
var clusterFactory map[string]NewClusterFunc

func init() {
	clusterFactory = make(map[string]NewClusterFunc)
}

// RegisterCluster 注册指定类型的cluster
func RegisterCluster(name string, clusterFunc NewClusterFunc) {
	clusterFactory[name] = clusterFunc
}

// GetClusterFunc 获取指定类型的cluster
func GetClusterFunc(name string) NewClusterFunc {
	return clusterFactory[name]
}
