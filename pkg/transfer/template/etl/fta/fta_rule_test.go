// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fta_test

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/fta"
)

type FTARuleSuite struct {
	suite.Suite
}

func (s *FTARuleSuite) TestInit() {
	triggerCfg := `
		{
			"rules": [
				{
					"key": "test1",
					"value": ["1"],
					"method": "eq",
					"condition": ""
				},
				{
					"key": "test2",
					"value": ["2"],
					"method": "neq",
					"condition": "or"
				},
				{
					"key": "test3",
					"value": ["3"],
					"method": "reg",
					"condition": "and"
				}
			]
		}
	`
	alert := &fta.Alert{}
	err := json.Unmarshal([]byte(triggerCfg), alert)
	s.NoError(err)
	err = alert.Init()
	s.NoError(err)
}

func (s *FTARuleSuite) TestMatch() {
	triggerCfg := `
		{
			"rules": [
				{
					"key": "event.name",
					"value": ["CPUX"],
					"method": "eq",
					"condition": ""
				},
				{
					"key": "event.content",
					"value": ["system\\.cpu"],
					"method": "reg",
					"condition": "or"
				},
				{
					"key": "event.level",
					"value": ["3"],
					"method": "eq",
					"condition": "and"
				}
			]
		}
	`
	trigger := &fta.Alert{}
	err := json.Unmarshal([]byte(triggerCfg), trigger)
	s.NoError(err)
	err = trigger.Init()
	s.NoError(err)

	result := trigger.IsMatch(map[string]interface{}{
		"event": map[string]interface{}{
			"name": "CPU",
		},
	})
	s.Equal(false, result)

	result = trigger.IsMatch(map[string]interface{}{
		"event": map[string]interface{}{
			"name": "CPUX",
		},
	})
	s.Equal(true, result)

	result = trigger.IsMatch(map[string]interface{}{
		"event": map[string]interface{}{
			"content": "system.cpu",
		},
	})
	s.Equal(false, result)

	result = trigger.IsMatch(map[string]interface{}{
		"event": map[string]interface{}{
			"content": "system.cpu",
			"level":   3,
		},
	})
	s.Equal(true, result)
}

// TestFTARule :
func TestFTARule(t *testing.T) {
	suite.Run(t, new(FTARuleSuite))
}

// BenchmarkTrigger_IsMatch
func BenchmarkTrigger_IsMatch(b *testing.B) {
	triggerCfg := `
		{
			"rules": [
				{
					"key": "event.name",
					"value": ["CPUX"],
					"method": "eq",
					"condition": ""
				},
				{
					"key": "event.content",
					"value": ["system\\.cpu"],
					"method": "reg",
					"condition": "or"
				},
				{
					"key": "event.level",
					"value": ["3"],
					"method": "eq",
					"condition": "and"
				}
			]
		}
	`
	trigger := &fta.Alert{}
	_ = json.Unmarshal([]byte(triggerCfg), trigger)
	_ = trigger.Init()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		trigger.IsMatch(map[string]interface{}{
			"event": map[string]interface{}{
				"name":    "CPU",
				"content": "system.cpu",
				"level":   3,
			},
		})
	}
}
