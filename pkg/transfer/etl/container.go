// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package etl

import (
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// MapContainer :
type MapContainer map[string]interface{}

// NewMapContainer : create MapContainer
func NewMapContainer() MapContainer {
	return make(map[string]interface{})
}

// NewMapContainerFrom :
func NewMapContainerFrom(data map[string]interface{}) MapContainer {
	return data
}

// ConvertMapContainer :
func ConvertMapContainer(data interface{}) MapContainer {
	switch t := data.(type) {
	case map[string]interface{}:
		return NewMapContainerFrom(t)
	case MapContainer:
		return t
	}
	return nil
}

// AsMapStr : as map[string]interface{}
func (c MapContainer) AsMapStr() map[string]interface{} {
	return c
}

// GetRealValue : get real value
func (c MapContainer) GetRealValue(value interface{}) interface{} {
	v := ConvertMapContainer(value)
	if v == nil {
		return value
	}
	return v
}

// Keys : get all keys
func (c MapContainer) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	return keys
}

// Del : delete by name
func (c MapContainer) Del(name string) error {
	_, ok := c[name]
	if !ok {
		return define.ErrItemNotFound
	}

	delete(c, name)
	return nil
}

// Get : get value by name
func (c MapContainer) Get(name string) (interface{}, error) {
	value, ok := c[name]
	if !ok {
		return nil, define.ErrItemNotFound
	}

	return c.GetRealValue(value), nil
}

// Put : put
func (c MapContainer) Put(name string, value interface{}) error {
	c[name] = value
	return nil
}

// Copy
func (c MapContainer) Copy() Container {
	dst := make(map[string]interface{})
	for key, value := range c {
		dst[key] = value
	}

	return NewMapContainerFrom(dst)
}

// MakeSubContainer
func MakeSubContainer(container Container, name string) (Container, error) {
	value, err := container.Get(name)
	if err == define.ErrItemNotFound {
		err := container.Put(name, make(map[string]interface{}))
		if err != nil {
			return nil, err
		}

		value, err = container.Get(name)
		if err != nil {
			return nil, err
		}
	}

	result, ok := value.(Container)
	if !ok {
		return nil, errors.WithMessagef(define.ErrType, "member %s is not a container of %v", name, container)
	}
	return result, nil
}

// ContainerToMap
func ContainerToMap(container Container) map[string]interface{} {
	switch v := container.(type) {
	case interface{ AsMapStr() map[string]interface{} }:
		return v.AsMapStr()
	default:
		results := make(map[string]interface{})
		for _, key := range container.Keys() {
			value, err := container.Get(key)
			utils.CheckError(err)
			results[key] = value
		}
		return results
	}
}
