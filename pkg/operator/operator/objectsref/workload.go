// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package objectsref

import (
	"time"
)

// RelabelConfig relabel 配置 遵循 prometheus 规则
type RelabelConfig struct {
	SourceLabels []string `json:"sourceLabels"`
	Separator    string   `json:"separator"`
	Regex        string   `json:"regex"`
	TargetLabel  string   `json:"targetLabel"`
	Replacement  string   `json:"replacement"`
	Action       string   `json:"action"`
	NodeName     string   `json:"nodeName"`
}

// WorkloadsRelabelConfigs 返回所有 workload relabel 配置
func (oc *ObjectsController) WorkloadsRelabelConfigs() []RelabelConfig {
	oc.mm.IncWorkloadRequestCounter()
	pods := oc.podObjs.GetAll()
	return getWorkloadRelabelConfigs(oc.getWorkloadRefs(pods))
}

// WorkloadsRelabelConfigsByNodeName 根据节点名称获取 workload relabel 配置
func (oc *ObjectsController) WorkloadsRelabelConfigsByNodeName(nodeName string) []RelabelConfig {
	oc.mm.IncWorkloadRequestCounter()
	pods := oc.podObjs.GetByNodeName(nodeName)
	return getWorkloadRelabelConfigs(oc.getWorkloadRefs(pods))
}

type WorkloadRef struct {
	Name      string   `json:"name"`
	Namespace string   `json:"namespace"`
	Ref       OwnerRef `json:"ownerRef"`
	NodeName  string   `json:"nodeName"`
}

func (oc *ObjectsController) getWorkloadRefs(pods []Object) []WorkloadRef {
	refs := make([]WorkloadRef, 0, len(pods))
	start := time.Now()

	for _, pod := range pods {
		ownerRef := Lookup(pod.ID, oc.podObjs, oc.objsMap())
		if ownerRef == nil {
			continue
		}
		refs = append(refs, WorkloadRef{
			Name:      pod.ID.Name,
			Namespace: pod.ID.Namespace,
			Ref:       *ownerRef,
			NodeName:  pod.NodeName,
		})
	}
	oc.mm.ObserveWorkloadLookupDuration(start)
	return refs
}

func (oc *ObjectsController) objsMap() map[string]*Objects {
	om := map[string]*Objects{
		kindReplicaSet:  oc.replicaSetObjs,
		kindDeployment:  oc.deploymentObjs,
		kindDaemonSet:   oc.daemonSetObjs,
		kindStatefulSet: oc.statefulSetObjs,
		kindJob:         oc.jobObjs,
		kindCronJob:     oc.cronJobObjs,
	}

	if oc.gameStatefulSetObjs != nil {
		om[kindGameStatefulSet] = oc.gameStatefulSetObjs
	}
	if oc.gameDeploymentsObjs != nil {
		om[kindGameDeployment] = oc.gameDeploymentsObjs
	}
	return om
}

func getWorkloadRelabelConfigs(refs []WorkloadRef) []RelabelConfig {
	configs := make([]RelabelConfig, 0, len(refs)*2)

	for _, ref := range refs {
		configs = append(configs, RelabelConfig{
			SourceLabels: []string{"namespace", "pod_name"},
			Separator:    ";",
			Regex:        ref.Namespace + ";" + ref.Name,
			TargetLabel:  "workload_kind",
			Replacement:  ref.Ref.Kind,
			Action:       "replace",
			NodeName:     ref.NodeName,
		})
		configs = append(configs, RelabelConfig{
			SourceLabels: []string{"namespace", "pod_name"},
			Separator:    ";",
			Regex:        ref.Namespace + ";" + ref.Name,
			TargetLabel:  "workload_name",
			Replacement:  ref.Ref.Name,
			Action:       "replace",
			NodeName:     ref.NodeName,
		})
	}
	return configs
}
