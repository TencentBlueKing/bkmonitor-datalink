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
	"github.com/jmespath/go-jmespath"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

func ExtractByJMESPathBase(compiled *jmespath.JMESPath, err error) ExtractFn {
	return func(c Container) (interface{}, error) {
		if err != nil {
			return nil, err
		}

		var result interface{}
		var innerErr error

		defer utils.RecoverError(func(e error) {
			innerErr = e
		})

		switch v := c.(type) {
		case MapContainer:
			result, innerErr = compiled.Search(v.AsMapStr())
		case Container:
			values := make(map[string]interface{})
			for _, k := range c.Keys() {
				val, err := c.Get(k)
				if err != nil {
					return nil, err
				}
				values[k] = val
			}
			result, innerErr = compiled.Search(values)
		default:
			result, innerErr = compiled.Search(c)
		}
		return result, innerErr
	}
}

// ExtractByJMESPath : extract container by jmes path
func ExtractByJMESPath(path string) ExtractFn {
	compiled, err := jmespath.Compile(path)
	return ExtractByJMESPathBase(compiled, err)
}

func ExtractByJMESMultiPathBase(compiledList []*jmespath.JMESPath, err error) ExtractFn {
	return func(c Container) (interface{}, error) {
		if err != nil {
			return nil, err
		}

		var result interface{}
		var innerErr error

		defer utils.RecoverError(func(e error) {
			innerErr = e
		})

		for _, compiled := range compiledList {
			switch v := c.(type) {
			case MapContainer:
				result, innerErr = compiled.Search(v.AsMapStr())
			case Container:
				values := make(map[string]interface{})
				for _, k := range c.Keys() {
					val, err := c.Get(k)
					if err != nil {
						return nil, err
					}
					values[k] = val
				}
				result, innerErr = compiled.Search(values)
			default:
				result, innerErr = compiled.Search(c)
			}

			// 找到数据则退出 按顺序查找
			if result != nil {
				return result, innerErr
			}
		}
		// 遍历完 compiled 也没有搜索到
		return nil, innerErr
	}
}

// ExtractByJMESMultiPath : extract container by jmes paths in order
func ExtractByJMESMultiPath(paths ...string) ExtractFn {
	compiledList := make([]*jmespath.JMESPath, 0, len(paths))
	errs := make([]error, 0)
	for _, path := range paths {
		compiled, err := jmespath.Compile(path)
		if err != nil {
			errs = append(errs, err)
		}
		compiledList = append(compiledList, compiled)
	}

	if len(errs) == 0 {
		return ExtractByJMESMultiPathBase(compiledList, nil)
	}

	return ExtractByJMESMultiPathBase(compiledList, errs[0])
}

// ExtractByJMESPathWithCustomFn : extract container by jmes path
func ExtractByJMESPathWithCustomFn(path string) ExtractFn {
	compiled, err := utils.CompileJMESPathCustom(path)
	return ExtractByJMESPathBase(compiled, err)
}
