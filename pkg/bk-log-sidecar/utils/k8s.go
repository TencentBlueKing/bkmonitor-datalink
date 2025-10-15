// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 日志平台 (BlueKing - Log) available.
// Copyright (C) 2017-2021 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

package utils

import (
	"encoding/json"
	"strings"

	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/config"
)

// IsNetworkPod is network pod
func IsNetworkPod(name string) bool {
	return name == "POD" || name == ""
}

// IsVclusterPod 检查Pod是否属于虚拟集群
func IsVclusterPod(pod *corev1.Pod) bool {
	labels := pod.GetLabels()
	_, exists := labels[config.VclusterLabelKey]
	return exists
}

// GetValueFromAnnotations get value from annotations
func GetValueFromAnnotations(pod *corev1.Pod, key string, defaultValue string) string {
	annotations := pod.GetAnnotations()
	value, exists := annotations[key]
	if exists {
		return value
	}
	return defaultValue
}

// GetPodName get pod name
func GetPodName(pod *corev1.Pod) string {
	if IsVclusterPod(pod) {
		return GetValueFromAnnotations(pod, config.VclusterPodNameAnnotationKey, pod.Name)
	}
	return pod.Name
}

// GetPodUid get pod uid
func GetPodUid(pod *corev1.Pod) string {
	uid := string(pod.UID)
	if IsVclusterPod(pod) {
		return GetValueFromAnnotations(pod, config.VclusterPodUidAnnotationKey, uid)
	}
	return uid
}

// GetPodNamespace get pod namespace
func GetPodNamespace(pod *corev1.Pod) string {
	if IsVclusterPod(pod) {
		return GetValueFromAnnotations(pod, config.VclusterPodNamespaceAnnotationKey, pod.Namespace)
	}
	return pod.Namespace
}

// GetPodWorkloadName get pod workload name
func GetPodWorkloadName(pod *corev1.Pod, defaultValue string) string {
	workloadName := defaultValue
	if IsVclusterPod(pod) {
		return GetValueFromAnnotations(pod, config.VclusterWorkloadNameAnnotationKey, workloadName)
	}
	for _, reference := range pod.GetOwnerReferences() {
		workloadName = reference.Name
	}
	return workloadName
}

// GetPodWorkloadType get pod workload type
func GetPodWorkloadType(pod *corev1.Pod, defaultValue string) string {
	workloadType := defaultValue
	if IsVclusterPod(pod) {
		return GetValueFromAnnotations(pod, config.VclusterWorkloadTypeAnnotationKey, workloadType)
	}
	for _, reference := range pod.GetOwnerReferences() {
		workloadType = reference.Kind
	}
	return workloadType
}

// GetWorkloadName get workload name
func GetWorkloadName(name string, kind string) string {
	if ToLowerEq(kind, "ReplicaSet") {
		index := strings.LastIndex(name, "-")
		return name[:index]
	}
	return name
}

// GetLabels get labels
func GetLabels(pod *corev1.Pod) map[string]string {
	log := ctrl.Log.WithName("utils")

	if !IsVclusterPod(pod) {
		return pod.GetLabels()
	}
	labelsText := GetValueFromAnnotations(pod, config.VclusterLabelsAnnotationKey, "")

	labels := make(map[string]string)

	// 数据格式
	// keya="value"\nkeyb="value"
	// 解析方式参考 https://github.com/loft-sh/vcluster/blob/de1552f073c4b9600f6c13b89fcbee29fc9a8bf8/pkg/controllers/resources/pods/translate/translator.go#L337

	for _, labelText := range strings.Split(labelsText, "\n") {
		labelText = strings.TrimSpace(labelText)
		index := strings.Index(labelText, "=")
		if index == -1 {
			// 没找到等号，则该行不合法
			log.Error(nil, "parse label failed, `=` not found: %s", labelsText)
			continue
		}
		key := labelText[:index]
		value := labelText[index+1:]
		if key == "" || value == "" {
			// key 或者 value 为空，则该行不合法
			log.Error(nil, "parse label failed, empty content: key=%s, value=%s", key, value)
			continue
		}

		var decodedValue string

		err := json.Unmarshal([]byte(value), &decodedValue)
		if err != nil {
			// 反序列化失败，则该行不合法
			log.Error(err, "parse label failed, invalid json value: key=%s, value=%s", key, value)
			continue
		}
		labels[key] = decodedValue
	}

	return labels
}

// GetAnnotations get annotations
func GetAnnotations(pod *corev1.Pod) map[string]string {
	if !IsVclusterPod(pod) {
		return pod.GetAnnotations()
	}
	annotationKeyText := GetValueFromAnnotations(pod, config.VclusterManagedAnnotationKey, "")

	annotations := make(map[string]string)
	allAnnotations := pod.GetAnnotations()

	// 数据格式
	// key1\nkey2

	for _, annotationKey := range strings.Split(annotationKeyText, "\n") {
		annotationKey = strings.TrimSpace(annotationKey)
		if value, ok := allAnnotations[annotationKey]; ok {
			annotations[annotationKey] = value
		} else {
			continue
		}
	}
	return annotations
}
