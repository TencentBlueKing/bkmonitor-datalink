// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package collector

import (
	"testing"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/model/relabel"
	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"
)

func TestRelabel(t *testing.T) {
	data := `- action: replace
  source_labels:
  - pod 
  target_label: pod_name
- action: replace
  source_labels:
  - container
  target_label: container_name
`
	var relabelConfigs []*relabel.Config
	err := yaml.Unmarshal([]byte(data), &relabelConfigs)
	assert.NoError(t, err)

	lsets := labels.Labels{
		{
			Name:  "pod",
			Value: "a",
		},
		{
			Name:  "container",
			Value: "b",
		},
	}
	result, _ := relabel.Process(lsets, relabelConfigs...)
	t.Logf("result: %+v", result)
}
