// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 日志平台 (BlueKing - Log) available.
// Copyright (C) 2017-2021 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

package define

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/api/bk.tencent.com/v1alpha1"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/utils"
	corev1 "k8s.io/api/core/v1"
)

// LogConfigType log config type
type LogConfigType interface {
	Config() []byte
	ConfigName() string
}

// Empty is log configs empty
func Empty(configs []LogConfigType) bool {
	if len(configs) == 0 {
		return true
	}
	return false
}

// labelKeyToField label key to field
func labelKeyToField(key string) string {
	metaKey := strings.ReplaceAll(key, "/", "_")
	metaKey = strings.ReplaceAll(metaKey, ".", "_")
	return strings.ReplaceAll(metaKey, "-", "_")
}

// StdOutLogConfig stdout log config
type StdOutLogConfig struct {
	v1alpha1.BkLogConfig
	Container   *Container
	Pod         *corev1.Pod
	RuntimeType RuntimeType
}

// Config stdout log config
func (s *StdOutLogConfig) Config() []byte {
	bkunifylogbeatConfig := &BkunifylogbeatConfig{}
	extMeta := make(map[string]interface{})

	if s.BkLogConfig.Spec.AddPodLabel {
		labels := make(map[string]string)
		for labelKey, labelValue := range utils.GetLabels(s.Pod) {
			labels[labelKeyToField(labelKey)] = labelValue
		}

		if s.BkLogConfig.Spec.IsBcsConfig {
			// 兼容 bcs 老版本字段配置，标签放在最外层
			for k, v := range labels {
				extMeta[k] = v
			}
		} else {
			extMeta["labels"] = labels
		}
	}

	if s.BkLogConfig.Spec.AddPodAnnotation {
		annotations := make(map[string]string)
		for labelKey, labelValue := range utils.GetAnnotations(s.Pod) {
			annotations[labelKeyToField(labelKey)] = labelValue
		}
		extMeta["annotations"] = annotations
	}

	if s.Spec.ExtMeta != nil {
		for k, v := range s.Spec.ExtMeta {
			extMeta[k] = v
		}
	}

	if s.BkLogConfig.Spec.IsBcsConfig {
		// 兼容 bcs 老版本字段配置
		extMeta["io_tencent_bcs_pod_ip"] = s.Pod.Status.PodIP
		extMeta["io_tencent_bcs_pod"] = utils.GetPodName(s.Pod)
		extMeta["io_tencent_bcs_namespace"] = utils.GetPodNamespace(s.Pod)
		extMeta["io_tencent_bcs_type"] = utils.GetPodWorkloadType(s.Pod, s.Spec.WorkloadType)
		extMeta["io_tencent_bcs_server_name"] = utils.GetPodWorkloadName(s.Pod, s.Spec.WorkloadName)
		extMeta["io_tencent_bcs_container_name"] = s.Container.Labels[config.ContainerLabelK8sContainerName]
		extMeta["container_id"] = s.Container.ID
	} else {
		extMeta["io_kubernetes_pod_ip"] = s.Pod.Status.PodIP
		extMeta["io_kubernetes_pod"] = utils.GetPodName(s.Pod)
		extMeta["io_kubernetes_pod_uid"] = utils.GetPodUid(s.Pod)
		extMeta["io_kubernetes_pod_namespace"] = utils.GetPodNamespace(s.Pod)
		extMeta["io_kubernetes_workload_name"] = utils.GetPodWorkloadName(s.Pod, s.Spec.WorkloadName)
		extMeta["io_kubernetes_workload_type"] = utils.GetPodWorkloadType(s.Pod, s.Spec.WorkloadType)
		extMeta["container_name"] = s.Container.Labels[config.ContainerLabelK8sContainerName]
		extMeta["container_id"] = s.Container.ID
		extMeta["container_image"] = s.Container.Image
	}

	local := FromBklogConfig(&s.BkLogConfig)

	if !utils.StringNotEmpty(local.Input) {
		local.Input = config.StdLogConfigInput
	}

	local.Path = []string{s.stdFilePath()}
	local.RemovePathPrefix = strings.TrimRight(config.HostPath, string(filepath.Separator))
	local.ExtMeta = extMeta
	local.TailFiles = s.BkLogConfig.Spec.TailFiles

	if !s.BkLogConfig.Spec.IsBcsConfig {
		// 如果是由 BCS 迁移过来的配置，直接按原始格式采集上来，不进行解析
		local.DockerJSON = &DockerJSON{
			Stream:   "all", // 采集标准输出和标准错误
			Partial:  true,  // 单行日志被截断时，拼接完整行之后再上报
			CRIFlags: true,  // 解析换行标签 P/F，containerd 的日志必须设置为 true
		}
		if s.RuntimeType == RuntimeTypeContainerd {
			local.DockerJSON.ForceCRI = true
		} else {
			local.DockerJSON.ForceCRI = false
		}
	}
	bkunifylogbeatConfig.Local = []Local{local}
	yamlContent, err := bkunifylogbeatConfig.Marshal()
	if utils.NotNil(err) {
		return []byte{}
	}
	return yamlContent
}

// stdFilePath stdout file log path
func (s *StdOutLogConfig) stdFilePath() string {
	return ToHostPath(s.Container.LogPath)
}

// ConfigName stdout log config name
func (s *StdOutLogConfig) ConfigName() string {
	return fmt.Sprintf("%s_%s_%s_%s", s.Container.ID, config.StdLogConfig, s.Namespace, s.Name)
}

// ContainerLogConfig container log config
type ContainerLogConfig struct {
	v1alpha1.BkLogConfig
	Container *Container
	Pod       *corev1.Pod
}

// Config container config
func (s *ContainerLogConfig) Config() []byte {
	bkunifylogbeatConfig := &BkunifylogbeatConfig{}
	extMeta := make(map[string]interface{})

	if s.BkLogConfig.Spec.AddPodLabel {
		labels := make(map[string]string)
		for labelKey, labelValue := range utils.GetLabels(s.Pod) {
			labels[labelKeyToField(labelKey)] = labelValue
		}

		if s.BkLogConfig.Spec.IsBcsConfig {
			// 兼容 bcs 老版本字段配置，标签放在最外层
			for k, v := range labels {
				extMeta[k] = v
			}
		} else {
			extMeta["labels"] = labels
		}
	}

	if s.BkLogConfig.Spec.AddPodAnnotation {
		annotations := make(map[string]string)
		for labelKey, labelValue := range utils.GetAnnotations(s.Pod) {
			annotations[labelKeyToField(labelKey)] = labelValue
		}
		extMeta["annotations"] = annotations
	}

	if s.Spec.ExtMeta != nil {
		for k, v := range s.Spec.ExtMeta {
			extMeta[k] = v
		}
	}

	if s.BkLogConfig.Spec.IsBcsConfig {
		// 兼容 bcs 老版本字段配置
		extMeta["io_tencent_bcs_pod_ip"] = s.Pod.Status.PodIP
		extMeta["io_tencent_bcs_pod"] = utils.GetPodName(s.Pod)
		extMeta["io_tencent_bcs_namespace"] = utils.GetPodNamespace(s.Pod)
		extMeta["io_tencent_bcs_type"] = utils.GetPodWorkloadType(s.Pod, s.Spec.WorkloadType)
		extMeta["io_tencent_bcs_server_name"] = utils.GetPodWorkloadName(s.Pod, s.Spec.WorkloadName)
		extMeta["io_tencent_bcs_container_name"] = s.Container.Labels[config.ContainerLabelK8sContainerName]
		extMeta["container_id"] = s.Container.ID
	} else {
		extMeta["io_kubernetes_pod_ip"] = s.Pod.Status.PodIP
		extMeta["io_kubernetes_pod"] = utils.GetPodName(s.Pod)
		extMeta["io_kubernetes_pod_uid"] = utils.GetPodUid(s.Pod)
		extMeta["io_kubernetes_pod_namespace"] = utils.GetPodNamespace(s.Pod)
		extMeta["io_kubernetes_workload_name"] = utils.GetPodWorkloadName(s.Pod, s.Spec.WorkloadName)
		extMeta["io_kubernetes_workload_type"] = utils.GetPodWorkloadType(s.Pod, s.Spec.WorkloadType)
		extMeta["container_name"] = s.Container.Labels[config.ContainerLabelK8sContainerName]
		extMeta["container_id"] = s.Container.ID
		extMeta["container_image"] = s.Container.Image
	}

	local := FromBklogConfig(&s.BkLogConfig)
	if !utils.StringNotEmpty(local.Input) {
		local.Input = config.ContainerLogConfigInput
	}

	// 容器采集路径补充为挂载根目录前缀
	containerRootPath := strings.TrimRight(s.Container.RootPath, string(filepath.Separator))
	local.RemovePathPrefix = strings.TrimRight(config.HostPath, string(filepath.Separator))
	local.RootFs = filepath.Join(local.RemovePathPrefix, containerRootPath)

	mountMap := make(map[string]string)
	mounts := make([]Mount, 0)
	for _, path := range local.Path {
		newMountMap, err := GetContainerMount(path, s.Container)
		if utils.NotNil(err) {
			continue
		}
		// 更新 mountMap
		for k, v := range newMountMap {
			mountMap[k] = v
		}
	}
	for k, v := range mountMap {
		mounts = append(mounts, Mount{HostPath: ToHostPath(k), ContainerPath: v})
	}
	if len(mountMap) > 0 {
		local.Mounts = mounts
	}

	local.ExtMeta = extMeta
	local.TailFiles = s.BkLogConfig.Spec.TailFiles
	bkunifylogbeatConfig.Local = []Local{local}
	yamlContent, err := bkunifylogbeatConfig.Marshal()
	if utils.NotNil(err) {
		return []byte{}
	}
	return yamlContent
}

// ConfigName container log config
func (s *ContainerLogConfig) ConfigName() string {
	return fmt.Sprintf("%s_%s_%s_%s", s.Container.ID, config.ContainerLogConfig, s.Namespace, s.Name)
}

// NodeLogConfig node log config
type NodeLogConfig struct {
	v1alpha1.BkLogConfig
	Node *corev1.Node
}

// Config get node config
func (s *NodeLogConfig) Config() []byte {
	bkunifylogbeatConfig := &BkunifylogbeatConfig{}
	extMeta := make(map[string]interface{})

	if s.BkLogConfig.Spec.AddPodLabel {
		labels := make(map[string]string)
		for labelKey, labelValue := range s.Node.GetLabels() {
			labels[labelKeyToField(labelKey)] = labelValue
		}
		extMeta["labels"] = labels
	}

	if s.BkLogConfig.Spec.AddPodAnnotation {
		annotations := make(map[string]string)
		for labelKey, labelValue := range s.Node.GetAnnotations() {
			annotations[labelKeyToField(labelKey)] = labelValue
		}
		extMeta["annotations"] = annotations
	}

	if s.Spec.ExtMeta != nil {
		for k, v := range s.Spec.ExtMeta {
			extMeta[k] = v
		}
	}

	local := FromBklogConfig(&s.BkLogConfig)

	if !utils.StringNotEmpty(local.Input) {
		local.Input = config.NodeLogConfigInput
	}

	local.ExtMeta = extMeta
	local.TailFiles = true
	local.RemovePathPrefix = strings.TrimRight(config.HostPath, string(filepath.Separator))
	local.RootFs = local.RemovePathPrefix
	for pathIdx, path := range local.Path {
		local.Path[pathIdx] = path
	}
	bkunifylogbeatConfig.Local = []Local{local}
	yamlContent, err := bkunifylogbeatConfig.Marshal()
	if utils.NotNil(err) {
		return []byte{}
	}
	return yamlContent
}

// ConfigName get node config name
func (s *NodeLogConfig) ConfigName() string {
	return fmt.Sprintf("%s_%s_%s", config.NodeLogConfig, s.Namespace, s.Name)
}
