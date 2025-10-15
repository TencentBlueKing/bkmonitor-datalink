// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 日志平台 (BlueKing - Log) available.
// Copyright (C) 2017-2021 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

// Package v1alpha1 contains API Schema definitions for the blueking v1alpha1 API group
// +k8s:openapi-gen=true
// +groupGoName=Bk
// +groupName=bk.tencent.com
// +kubebuilder:object:generate=true
// +kubebuilder:validation:Optional
// +module=github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	// GroupVersion is group version used to register these objects
	SchemeGroupVersion = schema.GroupVersion{Group: "bk.tencent.com", Version: "v1alpha1"}

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme
	SchemeBuilder = &scheme.Builder{GroupVersion: SchemeGroupVersion}

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

// 必须实现此方法注册具体类型
func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(SchemeGroupVersion,
		&BkLogConfig{},
		&BkLogConfigList{},
	)
	metav1.AddToGroupVersion(scheme, SchemeGroupVersion)
	return nil
}
