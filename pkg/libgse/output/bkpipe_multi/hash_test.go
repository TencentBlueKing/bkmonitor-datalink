// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bkpipe_multi

import (
	"testing"

	"github.com/elastic/beats/libbeat/common"
	"github.com/stretchr/testify/assert"
)

func makeConfigNamespace(t *testing.T, yaml string) common.ConfigNamespace {
	c, err := common.NewConfigWithYAML([]byte(yaml), "")

	if err != nil {
		t.Fatal(err)
	}

	config := common.ConfigNamespace{}
	err = c.Unpack(&config)

	if err != nil {
		t.Fatal(err)
	}
	return config
}

func Test_HashRawConfig(t *testing.T) {
	config1 := makeConfigNamespace(t, `kafka: {"hosts": ["127.0.0.1:9092"], "topic": "0bkmonitor_%{[dataid]}0", "version": "1.0.0"}`)
	hash1, _ := HashRawConfig(config1)

	config2 := makeConfigNamespace(t, `kafka: {"topic": "0bkmonitor_%{[dataid]}0", "version": "1.0.0", "hosts": ["127.0.0.1:9092"]}`)
	hash2, _ := HashRawConfig(config2)

	config3 := makeConfigNamespace(t, `my_kafka: {"topic": "0bkmonitor_%{[dataid]}0", "version": "1.0.0", "hosts": ["127.0.0.1:9092"]}`)
	hash3, _ := HashRawConfig(config3)

	config4 := makeConfigNamespace(t, `kafka: {"topic": "0bkmonitor_%{[dataid]}0", "hosts": ["127.0.0.1:9092"]}`)
	hash4, _ := HashRawConfig(config4)

	assert.Equal(t, hash1, hash2)
	assert.NotEqual(t, hash1, hash3)
	assert.NotEqual(t, hash1, hash4)
}
