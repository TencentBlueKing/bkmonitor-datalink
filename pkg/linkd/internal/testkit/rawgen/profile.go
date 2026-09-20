// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package rawgen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// LoadConfig 从一个严格 JSON profile 读取生成配置。
func LoadConfig(path string) (Config, error) {
	// path 是开发者显式指定的测试 profile，读取该文件正是此工具的职责。
	//nolint:gosec // G304: 仅用于本地测试数据生成。
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read raw generator profile %q: %w", path, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var config Config
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("decode raw generator profile %q: %w", path, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Config{}, fmt.Errorf("decode raw generator profile %q: trailing JSON data", path)
		}
		return Config{}, fmt.Errorf("decode raw generator profile %q: %w", path, err)
	}
	config = config.withDefaults()
	if err := config.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate raw generator profile %q: %w", path, err)
	}
	return config, nil
}
