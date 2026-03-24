// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 日志平台 (BlueKing - Log) available.
// Copyright (C) 2017-2021 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/config"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// BkLogConfigSpec defines the desired state of BkLogConfig
type BkLogConfigSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Foo is an example field of BkLogConfig. Edit bklogconfig_types.go to remove/update
	DataId        int64             `json:"dataId,omitempty"`
	Input         string            `json:"input,omitempty"`
	TailFiles     bool              `json:"-"`
	Path          []string          `json:"path,omitempty"`
	ExcludeFiles  []string          `json:"exclude_files,omitempty"`
	Encoding      string            `json:"encoding,omitempty"`
	Package       bool              `json:"package,omitempty"`
	PackageCount  int               `json:"packageCount,omitempty"`
	ScanFrequency string            `json:"scanFrequency,omitempty"`
	CloseInactive string            `json:"closeInactive,omitempty"`
	IgnoreOlder   string            `json:"ignoreOlder,omitempty"`
	CleanInactive string            `json:"cleanInactive,omitempty"`
	Multiline     MultilineConfig   `json:"multiline,omitempty"`
	ExtMeta       map[string]string `json:"extMeta,omitempty"`
	// match rule
	// std_log_config,container_log_config,node_log_config
	LogConfigType string `json:"logConfigType,omitempty"`

	// if set all_container is true will match all container
	AllContainer bool `json:"allContainer,omitempty"`

	// not recommended, can use NamespaceSelector
	Namespace            string               `json:"namespace,omitempty"`
	NamespaceSelector    NamespaceSelector    `json:"namespaceSelector,omitempty"`
	WorkloadType         string               `json:"workloadType,omitempty"`
	WorkloadName         string               `json:"workloadName,omitempty"`
	ContainerNameMatch   []string             `json:"containerNameMatch,omitempty"`
	ContainerNameExclude []string             `json:"containerNameExclude,omitempty"`
	LabelSelector        metav1.LabelSelector `json:"labelSelector,omitempty"`
	AnnotationSelector   metav1.LabelSelector `json:"annotationSelector,omitempty"`
	//+nullable
	Delimiter string `json:"delimiter,omitempty"`
	// bkunifylogbeat filter rule
	Filters          []Filter `json:"filters,omitempty"`
	AddPodLabel      bool     `json:"addPodLabel,omitempty"`
	AddPodAnnotation bool     `json:"addPodAnnotation,omitempty"`
	// If config is migrated from BCS, set it true
	IsBcsConfig bool `json:"isBcsConfig,omitempty"`

	// extra config options, will be rendered into sub config file directly
	// +kubebuilder:validation:Type=object
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:pruning:PreserveUnknownFields
	ExtOptions map[string]runtime.RawExtension `json:"extOptions,omitempty"`
}

// Filter is bkunifylogbeat filter rule
type Filter struct {
	Conditions []Condition `json:"conditions,omitempty"`
}

// Condition is bkunifylogbeat filter rule
type Condition struct {
	Index string `json:"index,omitempty"`
	Key   string `json:"key,omitempty"`
	Op    string `json:"op,omitempty"`
}

// NamespaceSelector multi namespace match
type NamespaceSelector struct {
	Any          bool     `json:"any,omitempty"`
	MatchNames   []string `json:"matchNames,omitempty"`
	ExcludeNames []string `json:"excludeNames,omitempty"`
}

// MultilineConfig is bkunifylogbeat multiline options
type MultilineConfig struct {
	//+nullable
	Pattern string `json:"pattern,omitempty"`
	//+nullable
	MaxLines int `json:"maxLines,omitempty"`
	//+nullable
	Timeout string `json:"timeout,omitempty"`
}

// BkLogConfigStatus defines the observed state of BkLogConfig
type BkLogConfigStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// BkLogConfig is the Schema for the bklogconfigs API
// +kubebuilder:object:root=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=bklogconfigs,singular=bklogconfig,scope=Namespaced
// +genclient
type BkLogConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BkLogConfigSpec   `json:"spec,omitempty"`
	Status BkLogConfigStatus `json:"status,omitempty"`
}

// BkLogConfigList contains a list of BkLogConfig
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
type BkLogConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BkLogConfig `json:"items"`
}

// IsNeedMatchType check LogConfigType
func (b *BkLogConfig) IsNeedMatchType() bool {
	return b.Spec.LogConfigType == config.ContainerLogConfig || b.Spec.LogConfigType == config.StdLogConfig
}

// IsContainerType is ContainerLogConfig
func (b *BkLogConfig) IsContainerType() bool {
	return b.Spec.LogConfigType == config.ContainerLogConfig
}

// IsNodeType is NodeLogConfig
func (b *BkLogConfig) IsNodeType() bool {
	return b.Spec.LogConfigType == config.NodeLogConfig
}

// IsMatchBkEnv 是否匹配对应的环境
func (b *BkLogConfig) IsMatchBkEnv() bool {
	// 两种情况
	// 1. 没有设置 bk-env 参数，则只匹配未设置 bkEnv 标签或 标签值为空的CR
	// 2. 如果设置了 bk-env 参数，则匹配 bkEnv 标签值相同的CR
	return b.Labels[config.BkEnvLabelName] == config.BkEnv
}

func init() {
	SchemeBuilder.Register(&BkLogConfig{}, &BkLogConfigList{})
}
