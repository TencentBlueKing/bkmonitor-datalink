// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package utils

// CombineHandler
type CombineHandler func(result map[string]interface{}) error

// CombineHelper
type CombineHelper struct {
	values map[string][]interface{}
}

func (p *CombineHelper) wrapForProduct(name string, values []interface{}, next CombineHandler) CombineHandler {
	return func(result map[string]interface{}) error {
		for _, value := range values {
			result[name] = value
			err := next(result)
			if err != nil {
				return err
			}
		}
		return nil
	}
}

func (p *CombineHelper) getInitValue(initValue map[string]interface{}) map[string]interface{} {
	if initValue == nil {
		initValue = make(map[string]interface{})
	}

	return initValue
}

// Product
func (p *CombineHelper) Product(initValue map[string]interface{}, callback CombineHandler) error {
	for name, values := range p.values {
		callback = p.wrapForProduct(name, values, callback)
	}

	return callback(p.getInitValue(initValue))
}

// Zip
func (p *CombineHelper) Zip(initValue map[string]interface{}, callback CombineHandler) error {
	index := 0

	done := false
	result := p.getInitValue(initValue)
	for !done {
		done = true

		for name, values := range p.values {
			if index >= len(values) {
				result[name] = nil
			} else {
				result[name] = values[index]
				done = false
			}
		}
		if !done {
			index++
			err := callback(result)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// NewCombineHelper
func NewCombineHelper(values map[string][]interface{}) *CombineHelper {
	return &CombineHelper{
		values: values,
	}
}
