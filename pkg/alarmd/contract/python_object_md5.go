// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
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
	"io"
)

// PythonObjectMD5 is count_md5 over one object, for the identities Python
// builds from a dimension dict rather than from a list of selected values.
//
// The no-data anomaly_id is the case this exists for: Python hashes the whole
// reduced dimension dict, the __NO_DATA_DIMENSION__ tag included, where dedupe
// hashes a list of the values its keys selected. Both are the same count_md5;
// only the shape handed to it differs, and one implementation answers both -
// which is the point of this being an entry rather than a second port.
//
// Values are raw JSON so that Python's distinction between an integer and a
// float survives the way in. Two encodings that Python's str() flattens
// together arrive here already flattened and hash alike: true and "True" agree
// because count_md5 hashes str(True), and 5 and "5" agree for the same reason.
// A caller that has a dimension value as text may therefore pass it as text.
func PythonObjectMD5(fields map[string]json.RawMessage) (string, error) {
	decoded := make(map[string]any, len(fields))
	for key, raw := range fields {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return "", fmt.Errorf("python object field %q: %w", key, err)
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return "", fmt.Errorf("python object field %q: expected one JSON value", key)
		}
		decoded[key] = value
	}
	return pythonCountMD5(decoded)
}
