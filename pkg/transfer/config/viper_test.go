// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config_test

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
)

// ViperConfigurationSuite :
type ViperConfigurationSuite struct {
	suite.Suite
}

// TestMarshal :
func (s *ViperConfigurationSuite) TestMarshal() {
	cases := []struct {
		object interface{}
		key    string
		value  interface{}
	}{
		{map[string]interface{}{"a": 1}, "a", 1.0},
		{map[string]interface{}{"a": map[string]interface{}{"b": "c"}}, "a.b", "c"},
		{struct{ A int }{1}, "a", 1.0},
		{struct{ A struct{ B string } }{A: struct{ B string }{"c"}}, "a.b", "c"},
		{struct{ A map[string]interface{} }{A: map[string]interface{}{"b": "c"}}, "a.b", "c"},
	}
	for _, c := range cases {
		conf := config.NewConfiguration()
		s.NoError(conf.Marshal(c.object))
		s.Equal(c.value, conf.Get(c.key))
	}
}

// TestViperConfiguration :
func TestViperConfiguration(t *testing.T) {
	suite.Run(t, new(ViperConfigurationSuite))
}
