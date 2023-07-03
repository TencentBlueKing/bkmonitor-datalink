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
	"regexp"
	"strings"
	"sync"

	"github.com/jmespath/go-jmespath"
)

var CustomJPFunctions = []*jmespath.FunctionEntry{
	jpfSplit,
	jpfRegexExtract,
	jpfGetField,
}

func CompileJMESPathCustom(expression string) (*jmespath.JMESPath, error) {
	compiled, err := jmespath.Compile(expression)
	if err != nil {
		return nil, err
	}

	for _, fn := range CustomJPFunctions {
		compiled.Register(fn)
	}
	return compiled, nil
}

// jpfSplit 将字符串拆分为列表
var jpfSplit = &jmespath.FunctionEntry{
	Name: "split",
	Arguments: []jmespath.ArgSpec{
		{Types: []jmespath.JpType{jmespath.JpString}},
		{Types: []jmespath.JpType{jmespath.JpString}},
	},
	Handler: func(arguments []interface{}) (interface{}, error) {
		search := arguments[0].(string)
		sep := arguments[1].(string)
		slice := strings.Split(search, sep)
		return slice, nil
	},
}

// jpfRegexExtract 字符串正则匹配
var (
	jpfRegexExtractCompiledCache = make(map[string]*regexp.Regexp)
	jpfRegexExtractCacheLock     = sync.Mutex{}
	jpfRegexExtract              = &jmespath.FunctionEntry{
		Name: "regex_extract",
		Arguments: []jmespath.ArgSpec{
			{Types: []jmespath.JpType{jmespath.JpString}},
			{Types: []jmespath.JpType{jmespath.JpString}},
		},
		Handler: func(arguments []interface{}) (interface{}, error) {
			search := arguments[0].(string)
			regex := arguments[1].(string)

			// 尝试从缓存中获取编译结果
			jpfRegexExtractCacheLock.Lock()
			defer jpfRegexExtractCacheLock.Unlock()
			re, ok := jpfRegexExtractCompiledCache[regex]
			var err error
			if !ok {
				// 没有拿到，则重新编译
				re, err = regexp.Compile(regex)
				if err != nil {
					return nil, err
				}
				// 将结果回写缓存
				jpfRegexExtractCompiledCache[regex] = re
			}
			result := re.FindStringSubmatch(search)
			return result, nil
		},
	}
)

// jpfGetField 从 Object 中获取值
var jpfGetField = &jmespath.FunctionEntry{
	Name: "get_field",
	Arguments: []jmespath.ArgSpec{
		{Types: []jmespath.JpType{jmespath.JpObject}},
		{Types: []jmespath.JpType{jmespath.JpString}},
	},
	Handler: func(arguments []interface{}) (interface{}, error) {
		search := arguments[0].(map[string]interface{})
		key := arguments[1].(string)

		value, ok := search[key]
		if !ok {
			return nil, nil
		}
		return value, nil
	},
}
