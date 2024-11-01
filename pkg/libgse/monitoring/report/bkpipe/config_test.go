// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bkpipe

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtraLabels(t *testing.T) {
	f, err := os.CreateTemp("", "info.file")
	assert.NoError(t, err)

	content := []byte(`
cluster_id=test_cluster1
`)

	f.Write(content)
	defer os.Remove(f.Name())

	os.Setenv("PODIP", "127.0.0.1")

	cfg := config{
		ExtraLabels: []ExtraLabel{
			{
				Type:     "file",
				Source:   f.Name(),
				Name:     "bcs_cluster_id",
				ValueRef: "cluster_id",
			},
			{
				Type:   "env",
				Source: "PODIP",
				Name:   "pod_ip",
			},
		},
	}

	extraLabels := make(map[string]string)
	for _, el := range cfg.ExtraLabels {
		for k, v := range el.Load() {
			extraLabels[k] = v
		}
	}

	assert.Equal(t, map[string]string{
		"bcs_cluster_id": "test_cluster1",
		"pod_ip":         "127.0.0.1",
	}, extraLabels)
}
