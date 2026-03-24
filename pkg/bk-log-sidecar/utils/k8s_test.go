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
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var realPod = &corev1.Pod{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "real-name",
		UID:       "real-uid",
		Namespace: "real-namespace",
		Labels: map[string]string{
			"aaa": "bbb",
			"ccc": "ddd",
		},
		Annotations: map[string]string{
			"key1": "value1",
			"key2": "value2",
		},
	},
}

var vclusterPodWithoutLabel = &corev1.Pod{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "real-name",
		UID:       "real-uid",
		Namespace: "real-namespace",
		Annotations: map[string]string{
			"vcluster.loft.sh/name":           "v-name",
			"vcluster.loft.sh/namespace":      "v-namespace",
			"vcluster.loft.sh/owner-set-kind": "v-workload-type",
			"vcluster.loft.sh/owner-set-name": "v-workload-name",
			"vcluster.loft.sh/uid":            "v-uid",
		},
	},
}

var vclusterPod = &corev1.Pod{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "real-name",
		UID:       "real-uid",
		Namespace: "real-namespace",
		Annotations: map[string]string{
			"vcluster.loft.sh/name":                "v-name",
			"vcluster.loft.sh/namespace":           "v-namespace",
			"vcluster.loft.sh/owner-set-kind":      "v-workload-type",
			"vcluster.loft.sh/owner-set-name":      "v-workload-name",
			"vcluster.loft.sh/uid":                 "v-uid",
			"vcluster.loft.sh/labels":              "app=\"nginx\"\ncontroller-revision-hash=\"statefulset-test-41xlw9ny-7b746545cd\"\nstatefulset.kubernetes.io/pod-name=\"statefulset-test-41xlw9ny-0\"",
			"vcluster.loft.sh/managed-annotations": "key1\nkey2",
			"key1":                                 "value1",
			"key2":                                 "value2",
			"key3":                                 "value3",
		},
		Labels: map[string]string{
			"vcluster.loft.sh/managed-by": "vcluster-test",
			"aaa":                         "bbb",
			"ccc":                         "ddd",
		},
	},
}

func TestGetPodName(t *testing.T) {
	assert.Equal(t, "real-name", GetPodName(realPod))
	assert.Equal(t, "real-name", GetPodName(vclusterPodWithoutLabel))
	assert.Equal(t, "v-name", GetPodName(vclusterPod))
}

func TestGetPodUid(t *testing.T) {
	assert.Equal(t, "real-uid", GetPodUid(realPod))
	assert.Equal(t, "real-uid", GetPodUid(vclusterPodWithoutLabel))
	assert.Equal(t, "v-uid", GetPodUid(vclusterPod))
}

func TestGetPodNamespace(t *testing.T) {
	assert.Equal(t, "real-namespace", GetPodNamespace(realPod))
	assert.Equal(t, "real-namespace", GetPodNamespace(vclusterPodWithoutLabel))
	assert.Equal(t, "v-namespace", GetPodNamespace(vclusterPod))
}

func TestGetPodWorkloadName(t *testing.T) {
	assert.Equal(t, "real-workload-name", GetPodWorkloadName(realPod, "real-workload-name"))
	assert.Equal(t, "real-workload-name", GetPodWorkloadName(vclusterPodWithoutLabel, "real-workload-name"))
	assert.Equal(t, "v-workload-name", GetPodWorkloadName(vclusterPod, "real-workload-name"))
}

func TestGetPodWorkloadType(t *testing.T) {
	assert.Equal(t, "real-workload-type", GetPodWorkloadType(realPod, "real-workload-type"))
	assert.Equal(t, "real-workload-type", GetPodWorkloadType(vclusterPodWithoutLabel, "real-workload-type"))
	assert.Equal(t, "v-workload-type", GetPodWorkloadType(vclusterPod, "real-workload-type"))
}

func TestGetLabels(t *testing.T) {
	assert.Equal(t, map[string]string{
		"aaa": "bbb",
		"ccc": "ddd",
	}, GetLabels(realPod))

	assert.Equal(t, map[string]string{
		"app":                                "nginx",
		"controller-revision-hash":           "statefulset-test-41xlw9ny-7b746545cd",
		"statefulset.kubernetes.io/pod-name": "statefulset-test-41xlw9ny-0",
	}, GetLabels(vclusterPod))
}

func TestGetAnnotations(t *testing.T) {
	assert.Equal(t, map[string]string{
		"key1": "value1",
		"key2": "value2",
	}, GetAnnotations(realPod))

	assert.Equal(t, map[string]string{
		"key1": "value1",
		"key2": "value2",
	}, GetAnnotations(vclusterPod))
}
