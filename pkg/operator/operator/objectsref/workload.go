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
	"strings"
	"unicode"
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
	pods := oc.podObjs.GetAll()
	return asWorkloadRelabelConfigs(oc.getRefs(pods, "", nil, nil))
}

// WorkloadsRelabelConfigsByPodName 根据节点名称和 pod 名称获取 workload relabel 配置
func (oc *ObjectsController) WorkloadsRelabelConfigsByPodName(nodeName, podName string, annotations, labels []string) []RelabelConfig {
	pods := oc.podObjs.GetByNodeName(nodeName)
	return asWorkloadRelabelConfigs(oc.getRefs(pods, podName, annotations, labels))
}

// PodsRelabelConfigs 获取 Pods Relabels 规则
func (oc *ObjectsController) PodsRelabelConfigs(annotations, labels []string) []RelabelConfig {
	pods := oc.podObjs.GetAll()
	_, podsRef := oc.getRefs(pods, "", annotations, labels)
	return asPodsRelabelConfigs(podsRef)
}

type WorkloadRef struct {
	Name      string   `json:"name"`
	Namespace string   `json:"namespace"`
	Ref       OwnerRef `json:"ownerRef"`
	NodeName  string   `json:"nodeName"`
}

type PodInfoRef struct {
	Name       string
	Namespace  string
	Dimensions map[string]string
}

func (oc *ObjectsController) getRefs(pods []Object, podName string, annotations, labels []string) ([]WorkloadRef, []PodInfoRef) {
	workloadRefs := make([]WorkloadRef, 0, len(pods))
	podInfoRefs := make([]PodInfoRef, 0)

	for _, pod := range pods {
		ownerRef := Lookup(pod.ID, oc.podObjs, oc.objsMap())
		if ownerRef == nil {
			continue
		}

		// 1) 没有 podname 则命中所有
		// 2) 存在则需要精准匹配
		if podName == "" || podName == pod.ID.Name {
			workloadRefs = append(workloadRefs, WorkloadRef{
				Name:      pod.ID.Name,
				Namespace: pod.ID.Namespace,
				Ref:       *ownerRef,
				NodeName:  pod.NodeName,
			})

			extra := make(map[string]string)
			for _, name := range annotations {
				if v, ok := pod.Annotations[name]; ok {
					extra["annotation_"+name] = v
				}
			}
			for _, name := range labels {
				if v, ok := pod.Labels[name]; ok {
					extra["label_"+name] = v
				}
			}
			// 按需补充维度
			if len(extra) > 0 {
				podInfoRefs = append(podInfoRefs, PodInfoRef{
					Name:       pod.ID.Name,
					Namespace:  pod.ID.Namespace,
					Dimensions: extra,
				})
			}
		}
	}
	return workloadRefs, podInfoRefs
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

func normalizeName(s string) string {
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' }), "_")
}

func asPodsRelabelConfigs(podInfoRefs []PodInfoRef) []RelabelConfig {
	configs := make([]RelabelConfig, 0)

	for _, ref := range podInfoRefs {
		for name, value := range ref.Dimensions {
			configs = append(configs, RelabelConfig{
				SourceLabels: []string{"namespace", "pod_name"},
				Separator:    ";",
				Regex:        ref.Namespace + ";" + ref.Name,
				TargetLabel:  normalizeName(name),
				Replacement:  value,
				Action:       "replace",
			})
		}
	}
	return configs
}

func asWorkloadRelabelConfigs(workloadRefs []WorkloadRef, podInfoRefs []PodInfoRef) []RelabelConfig {
	configs := make([]RelabelConfig, 0)

	for _, ref := range workloadRefs {
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

	configs = append(configs, asPodsRelabelConfigs(podInfoRefs)...)
	return configs
}
