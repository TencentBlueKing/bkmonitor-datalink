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
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/api/bk.tencent.com/v1alpha1"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/utils"
)

// LogConfigType log config type
type LogConfigType interface {
	Config() ([]byte, error)
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
func (s *StdOutLogConfig) Config() ([]byte, error) {
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
		return nil, fmt.Errorf("marshal stdout log config %s: %w", s.ConfigName(), err)
	}
	return yamlContent, nil
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
func (s *ContainerLogConfig) Config() ([]byte, error) {
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

	// 下发容器的全量卷挂载信息(host_path/container_path)，由采集器(bkunifylogbeat)在遍历
	// 采集路径时按需做 container_path->host_path 切换并解析软链。sidecar 不再预先按字面前缀
	// 匹配筛选 mounts，避免采集路径为软链(穿越卷边界,如 rootfs 软链->PVC)时漏配 mounts、
	// 导致卷内日志采集不到。采集器侧 selectFileSystem 按最长 container_path 前缀命中才切换，
	// 全量下发对未命中卷的路径无副作用。
	if len(s.Container.Mounts) > 0 {
		mounts := make([]Mount, 0, len(s.Container.Mounts))
		seen := make(map[Mount]struct{}, len(s.Container.Mounts))
		for _, mount := range s.Container.Mounts {
			// 跳过 host_path/container_path 为空的挂载：Docker tmpfs 的 MountPoint.Source 允许为空，
			// ToHostPath("") 会得到 sidecar 的 host 根路径，最终下发 {host_path: "/", container_path: ...}，
			// 采集器会错误切换到宿主机根目录、绕过 rootFs。
			if mount.HostPath == "" || mount.ContainerPath == "" {
				continue
			}
			m := Mount{HostPath: ToHostPath(mount.HostPath), ContainerPath: mount.ContainerPath}
			// 去重，避免容器上报重复挂载导致下发冗余。
			if _, ok := seen[m]; ok {
				continue
			}
			seen[m] = struct{}{}
			mounts = append(mounts, m)
		}
		// 稳定输出：按 container_path、host_path 排序，避免 CRI 多次 inspect 返回顺序不稳定时
		// 生成的子配置产生无意义 diff、触发 sidecar 无谓 reload。
		sort.Slice(mounts, func(i, j int) bool {
			if mounts[i].ContainerPath != mounts[j].ContainerPath {
				return mounts[i].ContainerPath < mounts[j].ContainerPath
			}
			return mounts[i].HostPath < mounts[j].HostPath
		})
		// 过滤后可能全为空，仅在存在有效挂载时才下发 mounts，保持无挂载时的原行为。
		if len(mounts) > 0 {
			local.Mounts = mounts
		}
	}

	local.ExtMeta = extMeta
	local.TailFiles = s.BkLogConfig.Spec.TailFiles
	bkunifylogbeatConfig.Local = []Local{local}
	yamlContent, err := bkunifylogbeatConfig.Marshal()
	if utils.NotNil(err) {
		return nil, fmt.Errorf("marshal container log config %s: %w", s.ConfigName(), err)
	}
	return yamlContent, nil
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
func (s *NodeLogConfig) Config() ([]byte, error) {
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
		return nil, fmt.Errorf("marshal node log config %s: %w", s.ConfigName(), err)
	}
	return yamlContent, nil
}

// ConfigName get node config name
func (s *NodeLogConfig) ConfigName() string {
	return fmt.Sprintf("%s_%s_%s", config.NodeLogConfig, s.Namespace, s.Name)
}
