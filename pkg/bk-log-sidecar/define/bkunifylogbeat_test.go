// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 日志平台 (BlueKing - Log) available.
// Copyright (C) 2017-2021 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

package define

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/api/bk.tencent.com/v1alpha1"
)

func TestBkunifylogbeatConfig(t *testing.T) {
	content := `local:
    - dataid: 11
      delimiter: '|'
      docker-json: null
      input: ""
      paths:
        - test
        - testsdf
      remove_path_prefix: ""
      tail_files: false
`
	local := Local{
		DataId:    11,
		Path:      []string{"test", "testsdf"},
		Delimiter: "|",
	}
	bkunifylogbeatConfig := &BkunifylogbeatConfig{
		Local: []Local{local},
	}
	yamlContent, _ := bkunifylogbeatConfig.Marshal()
	assert.Equal(t, string(yamlContent), content)
}

func TestBkunifylogbeatConfigWithExtOptions(t *testing.T) {
	content := `local:
    - dataid: 11
      delimiter: '|'
      docker-json: null
      ignore_older: 1h
      input: ""
      ludicrous_mode: true
      output.console:
        enabled: true
      paths:
        - test
        - testsdf
      remove_path_prefix: ""
      tail_files: false
`
	local := Local{
		DataId:      11,
		Path:        []string{"test", "testsdf"},
		Delimiter:   "|",
		IgnoreOlder: "1d",
		ExtOptions: map[string]runtime.RawExtension{
			"ludicrous_mode": {Raw: []byte("true")},
			"ignore_older":   {Raw: []byte(`"1h"`)},
			"output.console": {Raw: []byte(`{"enabled": true}`)},
		},
	}
	bkunifylogbeatConfig := &BkunifylogbeatConfig{
		Local: []Local{local},
	}
	yamlContent, _ := bkunifylogbeatConfig.Marshal()
	assert.Equal(t, string(yamlContent), content)

	content2 := []byte(`{"extOptions": {"output.console": {"enabled": true}}}`)
	data := v1alpha1.BkLogConfigSpec{}
	_ = json.Unmarshal(content2, &data)
	assert.Equal(t, data.ExtOptions, map[string]runtime.RawExtension{
		"output.console": {Raw: []byte(`{"enabled": true}`)},
	})

	content3 := []byte(`{"extOptions": {"output.console": [{"enabled": true}]}}`)
	data = v1alpha1.BkLogConfigSpec{}
	_ = json.Unmarshal(content3, &data)
	assert.Equal(t, data.ExtOptions, map[string]runtime.RawExtension{
		"output.console": {Raw: []byte(`[{"enabled": true}]`)},
	})
}
