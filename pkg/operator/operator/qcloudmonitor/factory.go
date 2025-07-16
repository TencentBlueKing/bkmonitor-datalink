// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package qcloudmonitor

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
)

type Owner interface {
	metav1.ObjectMetaAccessor
	schema.ObjectKind
}

func InjectManagingOwner(o metav1.Object, owner Owner) {
	o.SetOwnerReferences(
		append(
			o.GetOwnerReferences(),
			metav1.OwnerReference{
				//APIVersion:         owner.GroupVersionKind().GroupVersion().String(),
				APIVersion:         "monitoring.bk.tencent.com/v1beta1",
				BlockOwnerDeletion: ptr.To(true),
				Controller:         ptr.To(true),
				//Kind:               owner.GroupVersionKind().Kind,
				Kind: "QCloudMonitor",
				Name: owner.GetObjectMeta().GetName(),
				UID:  owner.GetObjectMeta().GetUID(),
			},
		),
	)
}

const InputHashAnnotationName = "bkmonitor-operator-input-hash"

func InjectInputHashAnnotation(o metav1.Object, h string) {
	a := o.GetAnnotations()
	if a == nil {
		a = map[string]string{}
	}
	a[InputHashAnnotationName] = h
	o.SetAnnotations(a)
}
