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
	MonitorNamespace = "bkmonitor_operator"
	UnknownNode      = "unknown"

	ReSyncPeriod = 5 * time.Minute

	ActionAdd            = "add"
	ActionDelete         = "delete"
	ActionUpdate         = "update"
	ActionCreateOrUpdate = "createOrUpdate"
	ActionSkip           = "skip"

	EnvNodeName  = "NODE_NAME"
	EnvPodName   = "POD_NAME"
	EnvNamespace = "NAMESPACE"
)

type CheckFunc func(string) (string, bool)

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
