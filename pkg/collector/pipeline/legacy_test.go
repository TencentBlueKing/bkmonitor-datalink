// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pipeline

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
)

func TestReportV1ToV2(t *testing.T) {
	configs, err := confengine.LoadConfigPattern("../example/fixtures/report_v1*.yml")
	assert.NoError(t, err)

	for _, conf := range configs {
		var v1Conf reportV1Config
		assert.NoError(t, conf.Unpack(&v1Conf))

		v2, err := convertReportV1ToV2(v1Conf)
		assert.NoError(t, err)
		processors := parseReportV2Configs(v2)

		ps := map[string]struct{}{
			"token_checker/proxy":       {},
			"rate_limiter/token_bucket": {},
			"proxy_validator/common":    {},
		}

		for k, processor := range processors {
			_, ok := ps[k]
			assert.True(t, ok)
			assert.Len(t, processor, 2)
		}
	}
}

func TestStealConfigs(t *testing.T) {
	input := []string{
		"/path/to/bk-collector/bk-collector-*.conf",
	}
	expected := []string{
		"/path/to/bk-collector/bk-collector-*.conf",
		"/path/to/bkmonitorproxy/bkmonitorproxy_report*.conf",
	}
	assert.Equal(t, expected, stealConfigs(input))
}
