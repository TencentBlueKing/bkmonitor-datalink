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
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
)

// MapHelper :
type MapHelper struct {
	Data map[string]interface{}
}

type MapStringHelper struct {
	Data map[string]string
}

// Get :
func (c *MapHelper) Get(key string) (result interface{}, ok bool) {
	result, ok = c.Data[key]
	return result, ok
}

// Get :
func (c *MapStringHelper) Get(key string) (result string, ok bool) {
	result, ok = c.Data[key]
	return result, ok
}

// MustGet :
func (c *MapHelper) MustGet(key string) interface{} {
	if value, ok := c.Get(key); ok {
		return value
	}
	panic(errors.WithMessagef(define.ErrKey, key))
}

// MustGet :
func (c *MapStringHelper) MustGet(key string) string {
	if value, ok := c.Get(key); ok {
		return value
	}
	panic(errors.WithMessagef(define.ErrKey, key))
}

// GetOrDefault :
func (c *MapHelper) GetOrDefault(key string, defaults interface{}) interface{} {
	if val, ok := c.Get(key); ok {
		return val
	}
	return defaults
}

// GetOrDefault :
func (c *MapStringHelper) GetOrDefault(key string, defaults string) string {
	if val, ok := c.Get(key); ok {
		return val
	}
	return defaults
}

// Exists :
func (c *MapHelper) Exists(key string) bool {
	_, ok := c.Get(key)
	return ok
}

// Exists :
func (c *MapStringHelper) Exists(key string) bool {
	_, ok := c.Get(key)
	return ok
}

// Set :
func (c *MapHelper) Set(key string, value interface{}) {
	if c.Data == nil {
		c.Data = map[string]interface{}{
			key: value,
		}
	} else {
		c.Data[key] = value
	}
}

// Set :
func (c *MapStringHelper) Set(key string, value string) {
	if c.Data == nil {
		c.Data = map[string]string{
			key: value,
		}
	} else {
		c.Data[key] = value
	}
}

// SetDefault :
func (c *MapHelper) SetDefault(key string, value interface{}) bool {
	if c.Exists(key) {
		return false
	}
	c.Set(key, value)
	return true
}

// SetDefault :
func (c *MapStringHelper) SetDefault(key string, value string) bool {
	if c.Exists(key) {
		return false
	}
	c.Set(key, value)
	return true
}

// GetArray : get value as []interface{}
func (c *MapHelper) GetArray(key string) ([]interface{}, bool) {
	result, ok := c.Data[key]
	if !ok {
		return nil, ok
	}
	value, ok := result.([]interface{})
	return value, ok
}

// MustGetArray : get []interface{} or panic
func (c *MapHelper) MustGetArray(key string) []interface{} {
	result, ok := c.Data[key]
	if !ok {
		panic(errors.WithMessagef(define.ErrKey, key))
	}
	value, ok := result.([]interface{})
	if !ok {
		panic(errors.WithMessagef(define.ErrType, key))
	}
	return value
}

// NewMapHelper :
func NewMapHelper(data map[string]interface{}) *MapHelper {
	if data == nil {
		data = make(map[string]interface{})
	}
	return &MapHelper{
		Data: data,
	}
}

// NewMapHelper :
func NewMapStringHelper(data map[string]string) *MapStringHelper {
	if data == nil {
		data = make(map[string]string)
	}
	return &MapStringHelper{
		Data: data,
	}
}

// NewFormatMapHelper :
func NewFormatMapHelper(data interface{}) (*MapHelper, bool) {
	val, ok := data.(map[string]interface{})
	return NewMapHelper(val), ok
}

//go:generate genny -in maphelper_types.tpl -pkg ${GOPACKAGE} -out maphelper_string.go gen "SYMBOL=String TYPE=string"
//go:generate genny -in maphelper_types.tpl -pkg ${GOPACKAGE} -out maphelper_int.go gen "SYMBOL=Int TYPE=int"
//go:generate genny -in maphelper_types.tpl -pkg ${GOPACKAGE} -out maphelper_bool.go gen "SYMBOL=Bool TYPE=bool"
