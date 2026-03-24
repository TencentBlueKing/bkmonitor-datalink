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
	"bytes"
	"encoding/json"
	"strings"

	"k8s.io/client-go/util/jsonpath"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/utils"
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

// WorkloadRef 是 Pod 与 Workload 的关联关系
type WorkloadRef struct {
	Name      string
	Namespace string
	Ref       OwnerRef
	NodeName  string
}

type WorkloadRefs []WorkloadRef

func (wr WorkloadRefs) AsRelabelConfigs() []RelabelConfig {
	configs := make([]RelabelConfig, 0, len(wr)*2)

	for _, ref := range wr {
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

// PodInfoRef 是 Pod 额外补充维度
type PodInfoRef struct {
	Name       string
	Namespace  string
	Dimensions map[string]string
}

type PodInfoRefs []PodInfoRef

func (pr PodInfoRefs) AsRelabelConfigs() []RelabelConfig {
	configs := make([]RelabelConfig, 0)

	for _, ref := range pr {
		for name, value := range ref.Dimensions {
			configs = append(configs, RelabelConfig{
				SourceLabels: []string{"namespace", "pod_name"},
				Separator:    ";",
				Regex:        ref.Namespace + ";" + ref.Name,
				TargetLabel:  utils.NormalizeName(name),
				Replacement:  value,
				Action:       "replace",
			})
		}
	}
	return configs
}

// ContainerInfoRef 是 Container 额外补充维度
type ContainerInfoRef struct {
	ContainerID     string
	ContainerName   string
	ContainerImage  string
	RefPodName      string
	RefPodNamespace string
}

type ContainerInfoRefs []ContainerInfoRef

func (cr ContainerInfoRefs) AsRelabelConfigs() []RelabelConfig {
	configs := make([]RelabelConfig, 0)
	for _, ref := range cr {
		configs = append(configs, RelabelConfig{
			SourceLabels: []string{"container_id"},
			Regex:        ref.ContainerID,
			TargetLabel:  "pod_name",
			Replacement:  ref.RefPodName,
			Action:       "replace",
		})
		configs = append(configs, RelabelConfig{
			SourceLabels: []string{"container_id"},
			Regex:        ref.ContainerID,
			TargetLabel:  "pod", // 兼容仪表盘和告警策略
			Replacement:  ref.RefPodName,
			Action:       "replace",
		})
		configs = append(configs, RelabelConfig{
			SourceLabels: []string{"container_id"},
			Regex:        ref.ContainerID,
			TargetLabel:  "namespace",
			Replacement:  ref.RefPodNamespace,
			Action:       "replace",
		})
		configs = append(configs, RelabelConfig{
			SourceLabels: []string{"container_id"},
			Regex:        ref.ContainerID,
			TargetLabel:  "container_name",
			Replacement:  ref.ContainerName,
			Action:       "replace",
		})
		configs = append(configs, RelabelConfig{
			SourceLabels: []string{"container_id"},
			Regex:        ref.ContainerID,
			TargetLabel:  "container", // 兼容仪表盘和告警策略
			Replacement:  ref.ContainerName,
			Action:       "replace",
		})
		configs = append(configs, RelabelConfig{
			SourceLabels: []string{"container_id"},
			Regex:        ref.ContainerID,
			TargetLabel:  "image",
			Replacement:  ref.ContainerImage,
			Action:       "replace",
		})
	}

	// 丢弃没有 container_name 的指标
	configs = append(configs, RelabelConfig{
		SourceLabels: []string{"container_name"},
		Action:       "drop",
	})
	// 丢弃 container_id 维度
	configs = append(configs, RelabelConfig{
		Regex:  "container_id",
		Action: "labeldrop",
	})
	return configs
}

// WorkloadsRelabelConfigs 返回所有 workload relabel 配置
func (oc *ObjectsController) WorkloadsRelabelConfigs() []RelabelConfig {
	pods := oc.podObjs.GetAll()
	return oc.getWorkloadRelabelConfigs(pods, "")
}

// WorkloadsRelabelConfigsByPodName 根据节点名称和 pod 名称获取 workload relabel 配置
func (oc *ObjectsController) WorkloadsRelabelConfigsByPodName(nodeName, podName string, annotations, labels []string) []RelabelConfig {
	pods := oc.podObjs.GetByNodeName(nodeName)

	var configs []RelabelConfig
	configs = append(configs, oc.getWorkloadRelabelConfigs(pods, podName)...)
	configs = append(configs, oc.getPodRelabelConfigs(pods, podName, annotations, labels)...)
	return configs
}

func (oc *ObjectsController) getWorkloadRelabelConfigs(pods []PodObject, podName string) []RelabelConfig {
	workloadRefs := make(WorkloadRefs, 0, len(pods))

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
		}
	}
	return workloadRefs.AsRelabelConfigs()
}

// PodsRelabelConfigs 获取 Pods Relabels 规则
func (oc *ObjectsController) PodsRelabelConfigs(annotations, labels []string) []RelabelConfig {
	pods := oc.podObjs.GetAll()
	// TODO(mando): 暂不支持指定 podname
	return oc.getPodRelabelConfigs(pods, "", annotations, labels)
}

func (oc *ObjectsController) getPodRelabelConfigs(pods []PodObject, podName string, annotations, labels []string) []RelabelConfig {
	podInfoRefs := make(PodInfoRefs, 0)

	parseFunc := func(s string) func(string) string {
		left := strings.Index(s, "(")
		right := strings.Index(s, ")")

		if left < 0 || right < 0 || right < left || right-left == 1 {
			return func(s string) string { return s }
		}
		template := s[left+1 : right]

		// 出错原路返回
		return func(input string) string {
			var obj any
			err := json.Unmarshal([]byte(input), &obj)
			if err != nil {
				return input
			}
			j := jsonpath.New("jsonpath")
			j.AllowMissingKeys(false)
			if err := j.Parse(template); err != nil {
				return input
			}
			buf := new(bytes.Buffer)
			if err := j.Execute(buf, obj); err != nil {
				return input
			}
			return buf.String()
		}
	}

	parseKey := func(s string) string {
		idx := strings.Index(s, ")")
		if idx > 0 {
			return s[idx+1:]
		}
		return s
	}

	var annotationsFunc []func(string) string
	var labelsFunc []func(string) string

	var decodedAnnotations []string
	var decodedLabels []string

	for _, annotation := range annotations {
		annotationsFunc = append(annotationsFunc, parseFunc(annotation))
		decodedAnnotations = append(decodedAnnotations, parseKey(annotation))
	}

	for _, label := range labels {
		labelsFunc = append(labelsFunc, parseFunc(label))
		decodedLabels = append(decodedLabels, parseKey(label))
	}

	for _, pod := range pods {
		// 1) 没有 podname 则命中所有
		// 2) 存在则需要精准匹配
		if podName == "" || podName == pod.ID.Name {
			extra := make(map[string]string)
			for i, name := range decodedAnnotations {
				if v, ok := pod.Annotations[name]; ok {
					extra["annotation_"+name] = annotationsFunc[i](v)
				}
			}
			for i, name := range decodedLabels {
				if v, ok := pod.Labels[name]; ok {
					extra["label_"+name] = labelsFunc[i](v)
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
	return podInfoRefs.AsRelabelConfigs()
}

// ContainersRelabelConfigs 获取 Containers Relabels 规则
func (oc *ObjectsController) ContainersRelabelConfigs(nodeName string) []RelabelConfig {
	return oc.getContainerRelabelConfigs(nodeName)
}

func (oc *ObjectsController) getContainerRelabelConfigs(nodeName string) []RelabelConfig {
	containerRefs := make(ContainerInfoRefs, 0)
	pods := oc.podObjs.GetByNodeName(nodeName)
	for _, pod := range pods {
		for _, container := range pod.Containers {
			if container.ID == "" {
				continue
			}
			containerRefs = append(containerRefs, ContainerInfoRef{
				ContainerID:     container.ID,
				ContainerName:   container.Name,
				ContainerImage:  container.ImageID,
				RefPodName:      pod.ID.Name,
				RefPodNamespace: pod.ID.Namespace,
			})
		}
	}
	return containerRefs.AsRelabelConfigs()
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
