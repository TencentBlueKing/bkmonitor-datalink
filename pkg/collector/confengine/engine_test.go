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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

func TestLoadConfigPath(t *testing.T) {
	config, err := LoadConfigPath("../example/example.yml")
	assert.NoError(t, err)

	conf := make(map[string]any)
	assert.NoError(t, config.Unpack(conf))
}

func TestLoadNotExistPath(t *testing.T) {
	config, err := LoadConfigPath("./example/example.yml")
	assert.Error(t, err)
	assert.Nil(t, config)
}

func TestLoadConfigPattern(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		configs, err := LoadConfigPattern("../example/fixtures/report_v2*.yml")
		assert.NoError(t, err)
		assert.Len(t, configs, 2)

		for _, config := range configs {
			m := make(map[string]any)
			assert.NoError(t, config.Unpack(m))
			t.Log("load config pattern:", m)
		}
	})

	t.Run("Failed", func(t *testing.T) {
		configs, err := LoadConfigPattern("../example/fixtures/report_v2*.ymlx")
		assert.NoError(t, err)
		assert.Len(t, configs, 0)
	})
}

func TestLoadContentFailed(t *testing.T) {
	conf, err := LoadConfigContent("|{}")
	assert.Nil(t, conf)
	assert.Error(t, err)
}

func TestMustLoadContentFailed(t *testing.T) {
	assert.Panics(t, func() {
		conf := MustLoadConfigContent("|{}")
		assert.Nil(t, conf)
	})
}

func TestLoadConfigPatterns(t *testing.T) {
	configs := LoadConfigPatterns([]string{"../example/fixtures/report_v2*.yml", "^.!.!"})
	assert.Len(t, configs, 2)

	for _, config := range configs {
		m := make(map[string]any)
		assert.NoError(t, config.Unpack(m))
		t.Log("load config patterns:", m)
	}
}

func TestSelectConfigFromType(t *testing.T) {
	t.Run("Platform", func(t *testing.T) {
		subConfigs := LoadConfigPatterns([]string{"../example/fixtures/platform.yml"})
		config := SelectConfigFromType(subConfigs, define.ConfigTypePlatform)
		assert.NotNil(t, config)

		conf := make(map[string]any)
		assert.NoError(t, config.Unpack(conf))
	})

	t.Run("Privileged", func(t *testing.T) {
		subConfigs := LoadConfigPatterns([]string{"../example/fixtures/privileged.yml"})
		config := SelectConfigFromType(subConfigs, define.ConfigTypePrivileged)
		assert.NotNil(t, config)

		conf := make(map[string]any)
		assert.NoError(t, config.Unpack(conf))
	})
}
