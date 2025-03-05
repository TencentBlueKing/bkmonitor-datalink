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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetPodRelabelConfigs(t *testing.T) {
	oc := ObjectsController{}
	pods := []PodObject{
		{
			PodIP: "pod1",
			Labels: map[string]string{
				"biz.cluster": "cluster1",
				"biz.zone":    "gz",
			},
			Annotations: map[string]string{
				"env.service": `[{"id":"test-service1","foo":"bar"}]`,
				"env.target":  "prod",
			},
		},
		{
			PodIP: "pod2",
			Labels: map[string]string{
				"biz.cluster": "cluster2",
				"biz.zone":    "gz",
			},
			Annotations: map[string]string{
				"env.service": `[{"id":"test-service1","foo":"bar"}]`,
				"env.target":  "stag",
			},
		},
	}

	expected := map[string]string{
		"annotation_env_service": "test-service1",
	}

	var hint int
	ret := oc.getPodRelabelConfigs(pods, "", []string{"({[0].id})env.service", "env.target"}, []string{"biz.cluster", "biz.zone"})
	for _, item := range ret {
		v, ok := expected[item.TargetLabel]
		if ok {
			hint++
			assert.Equal(t, v, item.Replacement)
		}
	}
	assert.Equal(t, 2, hint)
}
