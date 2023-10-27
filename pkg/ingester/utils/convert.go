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

	"gopkg.in/yaml.v2"
)

// ConvertByJSON 通过 JSON 将 struct 转换为另外一个 struct
func ConvertByJSON(source interface{}, target interface{}) error {
	data, err := json.Marshal(source)
	if err != nil {
		return err
	}
	err = json.Unmarshal(data, target)
	if err != nil {
		return err
	}
	return nil
}

// ConvertByYaml 通过 Yaml 将 struct 转换为另外一个 struct
func ConvertByYaml(source interface{}, target interface{}) error {
	data, err := yaml.Marshal(source)
	if err != nil {
		return err
	}
	err = yaml.Unmarshal(data, target)
	if err != nil {
		return err
	}
	return nil
}
