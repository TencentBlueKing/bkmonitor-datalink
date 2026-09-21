// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// PythonValueMD5 applies the existing Python count_md5 protocol to one JSON
// value, including dictionaries used by legacy record identity.
func PythonValueMD5(raw json.RawMessage) (string, error) {
	if !json.Valid(raw) {
		return "", fmt.Errorf("invalid JSON value")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	return pythonCountMD5(value)
}

// PythonScalarText preserves Python str formatting for observed scalar values.
func PythonScalarText(raw json.RawMessage) (string, error) {
	value, err := monitorScalar(raw)
	if err != nil {
		return "", err
	}
	return pythonScalarString(value)
}
