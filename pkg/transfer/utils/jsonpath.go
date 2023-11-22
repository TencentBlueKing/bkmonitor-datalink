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
	"github.com/oliveagle/jsonpath"
)

// JSONPath : Json
type JSONPath struct {
	*jsonpath.Compiled
}

// LookupSlice :
func (j *JSONPath) LookupSlice(obj interface{}) ([]interface{}, error) {
	result, err := j.Lookup(obj)
	if err != nil {
		return nil, err
	}
	return result.([]interface{}), nil
}

// ForEach :
func (j *JSONPath) ForEach(obj interface{}, fn func(index int, value interface{})) error {
	result, err := j.LookupSlice(obj)
	if err != nil {
		return err
	}
	for index, value := range result {
		fn(index, value)
	}
	return nil
}

// MustCompileJSONPath :
func MustCompileJSONPath(path string) *JSONPath {
	jpath, err := CompileJSONPath(path)
	if err != nil {
		panic(err)
	}
	return jpath
}

// CompileJSONPath :
func CompileJSONPath(path string) (*JSONPath, error) {
	jpath, err := jsonpath.Compile(path)
	if err != nil {
		return nil, err
	}
	return &JSONPath{
		Compiled: jpath,
	}, nil
}
