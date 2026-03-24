// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 日志平台 (BlueKing - Log) available.
// Copyright (C) 2017-2021 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

package config

import (
	"os"
)

const (
	ContainerLabelK8sContainerName = "io.kubernetes.container.name"
	ContainerLabelK8sPodName       = "io.kubernetes.pod.name"
	ContainerLabelK8sPodNamespace  = "io.kubernetes.pod.namespace"
)

const (
	StdLogConfig       = "std_log_config"
	ContainerLogConfig = "container_log_config"
	NodeLogConfig      = "node_log_config"
)

const (
	StdLogConfigInput       = "tail"
	ContainerLogConfigInput = "tail"
	NodeLogConfigInput      = "tail"
)

const (
	BkEnvLabelName = "bk_env"
)

const (
	CurrentNodeNameKey = "MY_NODE_NAME"
)

var (
	VclusterPodNameAnnotationKey      = GetEnvDefault("VCLUSTER_POD_NAME_ANNOTATION_KEY", "vcluster.loft.sh/name")
	VclusterPodUidAnnotationKey       = GetEnvDefault("VCLUSTER_POD_UID_ANNOTATION_KEY", "vcluster.loft.sh/uid")
	VclusterPodNamespaceAnnotationKey = GetEnvDefault("VCLUSTER_POD_NAMESPACE_ANNOTATION_KEY", "vcluster.loft.sh/namespace")
	VclusterWorkloadNameAnnotationKey = GetEnvDefault("VCLUSTER_WORKLOAD_NAME_ANNOTATION_KEY", "vcluster.loft.sh/owner-set-name")
	VclusterWorkloadTypeAnnotationKey = GetEnvDefault("VCLUSTER_WORKLOAD_TYPE_ANNOTATION_KEY", "vcluster.loft.sh/owner-set-kind")
	VclusterLabelsAnnotationKey       = GetEnvDefault("VCLUSTER_LABELS_ANNOTATION_KEY", "vcluster.loft.sh/labels")
	VclusterLabelKey                  = GetEnvDefault("VCLUSTER_LABEL_KEY", "vcluster.loft.sh/managed-by")
	VclusterManagedAnnotationKey      = GetEnvDefault("VCLUSTER_MANAGED_ANNOTATIONS_KEY", "vcluster.loft.sh/managed-annotations")
)

// GetEnvDefault 获取特定环境变量，若值为空，则使用默认值
func GetEnvDefault(envVar string, defaultValue string) string {
	if v, ok := os.LookupEnv(envVar); ok && len(v) > 0 {
		return v
	}
	return defaultValue
}
