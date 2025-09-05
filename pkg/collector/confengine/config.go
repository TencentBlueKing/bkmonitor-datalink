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
	"fmt"

	"github.com/elastic/beats/libbeat/common"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/beat"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// Config 是对 beat.Config 的封装 并提供一些简便的操作函数
type Config struct {
	conf *beat.Config
}

func New(conf *beat.Config) *Config {
	return &Config{conf: conf}
}

func (c *Config) Has(s string) bool {
	ok, err := c.conf.Has(s, -1)
	if err != nil {
		return false
	}
	return ok
}

func (c *Config) Child(s string) (*Config, error) {
	content, err := c.conf.Child(s, -1)
	if err != nil {
		return nil, err
	}
	return &Config{conf: (*beat.Config)(content)}, nil
}

func (c *Config) MustChild(s string) *Config {
	child, err := c.Child(s)
	if err != nil {
		panic(err)
	}
	return child
}

func (c *Config) Unpack(to any) error {
	return c.conf.Unpack(to)
}

func (c *Config) Disabled(s string) bool {
	ok, err := c.conf.Bool(fmt.Sprintf("%s.disabled", s), -1)
	if err != nil {
		return false
	}
	return ok
}

func (c *Config) UnpackChild(s string, to any) error {
	content, err := c.conf.Child(s, -1)
	if err != nil {
		return err
	}
	return content.Unpack(to)
}

func (c *Config) UnpackIntWithDefault(s string, val int) int {
	n, err := c.conf.Int(s, -1)
	if err != nil {
		return val
	}
	return int(n)
}

func (c *Config) RawConfig() *common.Config {
	return c.conf
}

const (
	keyGlobal = "__global__"
)

// TierConfig 实现了层级 Config 管理和查找的能力
// 配置总共有四个搜索路径，搜索顺序为 1) -> 2) -> 3) -> 4)
//
// 4) global.config			全局主配置（keyGlobal）
// 3) subconfigs.default	子配置默认配置（SubConfigFieldDefault）
// 2) subconfigs.service	子配置服务级别配置（SubConfigFieldService）
// 1) subconfigs.instance	子配置实例级别配置（SubConfigFieldInstance）
//
// 一个子配置文件描述了某个唯一标识的应用的自定义配置
type TierConfig struct {
	m map[tierKey]any
}

type tierKey struct {
	Token string
	Type  string
	ID    string
}

func NewTierConfig() *TierConfig {
	return &TierConfig{m: map[tierKey]any{}}
}

func (tc *TierConfig) All() []any {
	objs := make([]any, 0)
	for _, v := range tc.m {
		objs = append(objs, v)
	}
	return objs
}

func (tc *TierConfig) Set(token, typ, id string, val any) {
	tc.m[tierKey{Token: token, Type: typ, ID: id}] = val
}

func (tc *TierConfig) SetGlobal(val any) {
	tc.m[tierKey{Type: keyGlobal}] = val
}

func (tc *TierConfig) Del(token, typ, id string) {
	delete(tc.m, tierKey{Token: token, Type: typ, ID: id})
}

func (tc *TierConfig) DelGlobal() {
	delete(tc.m, tierKey{Type: keyGlobal})
}

func (tc *TierConfig) GetGlobal() any {
	return tc.m[tierKey{Type: keyGlobal}]
}

func (tc *TierConfig) GetByToken(token string) any {
	return tc.Get(token, "", "")
}

func (tc *TierConfig) Get(token, serviceID, instanceID string) any {
	val, typ := tc.get(token, serviceID, instanceID)
	logger.Debugf("tier config(token=%s, serviceID=%s, instanceID=%s), type: %s", token, serviceID, instanceID, typ)
	return val
}

func (tc *TierConfig) get(token, serviceID, instanceID string) (any, string) {
	// 1) subconfigs.instance
	if instanceID != "" {
		v, ok := tc.m[tierKey{Token: token, Type: define.SubConfigFieldInstance, ID: instanceID}]
		if ok {
			return v, define.SubConfigFieldInstance
		}
	}

	// 2) subconfigs.service
	if serviceID != "" {
		v, ok := tc.m[tierKey{Token: token, Type: define.SubConfigFieldService, ID: serviceID}]
		if ok {
			return v, define.SubConfigFieldService
		}
	}

	// 3) subconfigs.default
	v, ok := tc.m[tierKey{Token: token, Type: define.SubConfigFieldDefault}]
	if ok {
		return v, define.SubConfigFieldDefault
	}

	// 4) global.config
	return tc.m[tierKey{Type: keyGlobal}], keyGlobal
}
