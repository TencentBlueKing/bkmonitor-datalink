// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package utils

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

// MapHelperSuite :
type MapHelperSuite struct {
	suite.Suite
}

type cases struct {
	conf                            *MapHelper
	key                             string
	fn                              func(interface{}) (interface{}, error)
	defaults, excepted, errExcepted interface{}
}

// TestOption :
func (s *MapHelperSuite) TestConsulConfig() {
	casesGetValue := []cases{
		{&MapHelper{}, "t", nil, nil, nil, false},
		{&MapHelper{Data: map[string]interface{}{}}, "t", nil, nil, nil, false},
		{&MapHelper{Data: map[string]interface{}{"key": "value"}}, "errKey", nil, nil, nil, false},
		{&MapHelper{Data: map[string]interface{}{"key": "value"}}, "key", nil, nil, "value", true},
		{&MapHelper{Data: map[string]interface{}{"key": ""}}, "key", nil, nil, "", true},
		{&MapHelper{Data: map[string]interface{}{"key": map[string]interface{}{"x": "v"}}}, "key", nil, nil, map[string]interface{}{"x": "v"}, true},
	}
	casesGetValueWithDefault := []cases{
		// 无配置, 无key , 返回default
		{&MapHelper{}, "t", nil, "default", "default", nil},
		// 无配置, 有key  有default ===> default
		{&MapHelper{Data: map[string]interface{}{}}, "t", nil, "1", "1", nil},
		// 有配置, 有key  错误key 有default === > default
		{&MapHelper{Data: map[string]interface{}{"key": "value"}}, "errKey", nil, "3", "3", nil},
		// 有配置, 有key  正确key 无default === > value
		{&MapHelper{Data: map[string]interface{}{"key": "value"}}, "key", nil, nil, "value", nil},
		// 有配置, 有key  正确key 有default === > value
		{&MapHelper{Data: map[string]interface{}{"key": "value"}}, "key", nil, "123", "value", nil},
		// 有配置, 有key  正确key value为空字符串 有default === > ""
		{&MapHelper{Data: map[string]interface{}{"key": ""}}, "key", nil, "x", "", nil},
		// 有配置, 有key  正确key value为空 有default === > nil
		{&MapHelper{Data: map[string]interface{}{"key": nil}}, "key", nil, "x", nil, nil},
	}
	casesSet := []cases{
		{&MapHelper{Data: map[string]interface{}{"key": nil}}, "key", nil, "x", nil, nil},
	}
	casesSetDefault := []cases{
		// 存在key 返回false
		{&MapHelper{Data: map[string]interface{}{"test": 1}}, "test", nil, "x", 1, false},
		// 存在key 设置default 返回true
		{&MapHelper{Data: map[string]interface{}{"test2": 1}}, "test", nil, "x", 123, true},
	}

	for _, value := range casesGetValue {
		res, ok := value.conf.Get(value.key)
		s.Equal(value.excepted, res)
		s.Equal(value.errExcepted, ok)
	}

	for _, value := range casesGetValueWithDefault {
		res := value.conf.GetOrDefault(value.key, value.defaults)
		s.Equal(value.excepted, res)
	}

	for _, value := range casesSet {
		value.conf.SetDefault("test", 123)
		res := value.conf.GetOrDefault(value.key, value.defaults)
		s.Equal(value.excepted, res)
	}

	for _, value := range casesSetDefault {
		ok := value.conf.SetDefault(value.key, 123)
		res := value.conf.GetOrDefault(value.key, value.defaults)
		s.Equal(value.excepted, res)
		s.Equal(value.errExcepted, ok)
	}
}

// TestConsulConfigSuite :
func TestConsulConfigSuite(t *testing.T) {
	suite.Run(t, new(MapHelperSuite))
}
