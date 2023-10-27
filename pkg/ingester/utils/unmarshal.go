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
	"encoding/json"
	"strings"

	xml "github.com/clbanning/mxj/v2"
	"gopkg.in/yaml.v2"
)

type UnmarshalFn func(data []byte, v *interface{}) error

var unmarshalHandlers = map[string]UnmarshalFn{
	"json": func(data []byte, v *interface{}) error {
		return json.Unmarshal(data, v)
	},
	"yaml": func(data []byte, v *interface{}) error {
		return yaml.Unmarshal(data, v)
	},
	"xml": func(data []byte, v *interface{}) error {
		result, err := xml.NewMapXml(data)
		if err != nil {
			return err
		}
		*v = result.Old()
		return nil
	},
	"text": func(data []byte, v *interface{}) error {
		*v = map[string]interface{}{
			"text": string(data),
		}
		return nil
	},
}

func GetUnmarshalFn(format string) UnmarshalFn {
	switch strings.ToLower(format) {
	case "json", "":
		return unmarshalHandlers["json"]
	case "yaml", "yml":
		return unmarshalHandlers["yaml"]
	case "xml":
		return unmarshalHandlers["xml"]
	case "txt", "text":
		return unmarshalHandlers["text"]
	default:
		return unmarshalHandlers["json"]
	}
}
