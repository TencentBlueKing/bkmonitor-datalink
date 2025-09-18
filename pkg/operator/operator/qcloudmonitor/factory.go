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
	"k8s.io/utils/ptr"

	bkv1beta1 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/apis/monitoring/v1beta1"
)

// OwnerRef 返回 qcm 作为 OwnerReference 的对象
func OwnerRef(qcm *bkv1beta1.QCloudMonitor) metav1.OwnerReference {
	return metav1.OwnerReference{
		APIVersion:         bkv1beta1.SchemeGroupVersion.String(),
		BlockOwnerDeletion: ptr.To(true),
		Controller:         ptr.To(true),
		Kind:               "QCloudMonitor",
		Name:               qcm.Name,
		UID:                qcm.UID,
	}
}
