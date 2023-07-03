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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// SingleFission
func SingleFission(from Container, callback func(fission Container) error) error {
	return callback(from)
}

// NewCombineHelperByContainer
func NewCombineHelperByContainer(from Container, fields []SimpleField) (*utils.CombineHelper, error) {
	values := make(map[string][]interface{}, len(fields))
	for _, field := range fields {
		value, err := field.GetValue(from)
		if err != nil {
			return nil, err
		}

		name := field.Name()
		result, ok := value.([]interface{})
		if !ok {
			return nil, errors.WithMessagef(define.ErrType, "expect type []interface{} but got %T", result)
		} else if len(result) == 0 {
			logging.Debugf("%v skipped because field %s always empty", from, name)
			return nil, nil
		}

		values[name] = result
	}

	return utils.NewCombineHelper(values), nil
}

// ArrayProductFission
func ArrayProductFission(fields ...SimpleField) FissionFn {
	return func(from Container, callback func(fission Container) error) error {
		helper, err := NewCombineHelperByContainer(from, fields)
		if err != nil {
			return err
		} else if helper == nil {
			return nil
		}
		return helper.Product(nil, func(result map[string]interface{}) error {
			for key, value := range result {
				err := from.Put(key, value)
				if err != nil {
					return err
				}
			}
			return callback(from)
		})
	}
}

// ArrayZipFission
func ArrayZipFission(fields ...SimpleField) FissionFn {
	return func(from Container, callback func(fission Container) error) error {
		helper, err := NewCombineHelperByContainer(from, fields)
		if err != nil {
			return err
		} else if helper == nil {
			return nil
		}
		return helper.Zip(nil, func(result map[string]interface{}) error {
			for key, value := range result {
				err := from.Put(key, value)
				if err != nil {
					return err
				}
			}
			return callback(from)
		})
	}
}
