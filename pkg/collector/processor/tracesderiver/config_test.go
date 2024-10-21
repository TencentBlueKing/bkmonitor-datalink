// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tracesderiver

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pkg/mapstructure"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
)

func TestConfigHandler(t *testing.T) {
	config, err := confengine.LoadConfigPath("../../example/fixtures/main.yml")
	assert.NoError(t, err)

	var processorConfigs processor.Configs
	err = config.UnpackChild("bk-collector.processor", &processorConfigs)
	assert.NoError(t, err)

	var tracesDerivedConfig *processor.Config
	for _, pc := range processorConfigs {
		if pc.Name == "traces_deriver/duration" {
			tracesDerivedConfig = &pc
		}
	}
	assert.NotNil(t, tracesDerivedConfig)

	var c Config
	err = mapstructure.Decode(tracesDerivedConfig.Config, &c)
	assert.NoError(t, err)

	const Type = "duration"

	handler := NewConfigHandler(c)
	types := []TypeWithName{{Type: Type, MetricName: "bk_apm_duration"}}
	assert.Equal(t, types, handler.GetTypes())

	predicateKeys := []string{"attributes.http.method"}
	assert.Equal(t, predicateKeys, handler.GetPredicateKeys(Type, "SPAN_KIND_CLIENT"))
	assert.Len(t, handler.GetPredicateKeys(Type, "SPAN_KIND_UNKNOWN"), 1)

	attributes := []string{
		"net.peer.name",
		"net.peer.ip",
		"net.peer.port",
	}
	assert.Equal(t, attributes, handler.GetAttributes(Type, "SPAN_KIND_RPC", "attributes.rpc.method"))
	assert.Len(t, handler.GetAttributes(Type, "SPAN_KIND_RPC", "attributes.rpc.method.noexsit"), 0)

	assert.Equal(t, []string{"span_name"}, handler.GetMethods(Type, "SPAN_KIND_RPC", "attributes.rpc.method"))
	assert.Len(t, handler.GetMethods(Type, "SPAN_KIND_RPC", "attributes.rpc.method.noexsit"), 0)
}
