// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package confengine

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

type testConfig struct {
	id string
}

func TestTierConfig(t *testing.T) {
	tc := NewTierConfig()
	tc.SetGlobal(testConfig{id: "default"})

	tc.Set("token1", "default", "", testConfig{id: "token1"})
	tc.Set("token1", "service", "service1", testConfig{id: "token1/service1"})
	tc.Set("token1", "service", "service2", testConfig{id: "token1/service2"})
	tc.Set("token1", "instance", "instance1", testConfig{id: "token1/service1/instance1"})
	tc.Set("token1", "instance", "instance2", testConfig{id: "token1/service1/instance2"})

	assert.Len(t, tc.All(), 6)
	assert.Equal(t, "default", tc.GetByToken("not_exists").(testConfig).id)
	assert.Equal(t, "default", tc.GetByToken("").(testConfig).id)
	assert.Equal(t, "token1", tc.GetByToken("token1").(testConfig).id)
	assert.Equal(t, "token1/service1", tc.Get("token1", "service1", "").(testConfig).id)
	assert.Equal(t, "token1/service2", tc.Get("token1", "service2", "").(testConfig).id)
	assert.Equal(t, "token1/service1/instance1", tc.Get("token1", "", "instance1").(testConfig).id)
	assert.Equal(t, "token1/service1/instance2", tc.Get("token1", "", "instance2").(testConfig).id)

	tc.Del("token1", "service", "service1")
	assert.Equal(t, "token1", tc.Get("token1", "service1", "").(testConfig).id)

	tc.DelGlobal()
	assert.Nil(t, tc.GetGlobal())
}

func TestConfig(t *testing.T) {
	content := `
proxy:
  server:
    disabled: true
    num: 10
  logger:
    level: debug
    output: file
    path: /path/to/config
`
	config := MustLoadConfigContent(content)
	assert.NotNil(t, config.RawConfig())
	assert.True(t, config.Has("proxy"))

	cfg := config.MustChild("proxy")
	assert.True(t, cfg.Disabled("server"))

	m1 := make(map[string]any)
	assert.NoError(t, config.UnpackChild("proxy", m1))

	m2 := make(map[string]any)
	assert.NoError(t, config.Unpack(m2))
	assert.Equal(t, 8080, config.UnpackIntWithDefault("proxy.server.port", 8080))
	assert.Equal(t, 10, config.UnpackIntWithDefault("proxy.server.num", 0))
}
