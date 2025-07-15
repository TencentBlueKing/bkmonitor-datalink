// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package define

import (
	"fmt"
	"time"
)

const (
	AppName          = "bkmonitor-operator"
	MonitorNamespace = "bkmonitor_operator"
	UnknownNode      = "unknown"

	ReSyncPeriod = 5 * time.Minute
)

// ConfigFilePath 主配置文件路径
var ConfigFilePath string

// MonitorMeta 描述了监控类型的元数据信息，目前类型有 serviceMonitor, podMonitor, probe
type MonitorMeta struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Index     int    `json:"index"`
}

// ID 即 Discover Name
func (m MonitorMeta) ID() string {
	return fmt.Sprintf("%s/%s/%s/%d", m.Kind, m.Namespace, m.Name, m.Index)
}

// DefObserveDuration 默认的时间桶分布
var DefObserveDuration = []float64{
	0.001, 0.005, 0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 30, 60, 120, 300, 600,
}

type ClusterInfo struct {
	BcsClusterID string `json:"bcs_cluster_id"`
	BizID        string `json:"bizid"`
	BkEnv        string `json:"bk_env"`
}
