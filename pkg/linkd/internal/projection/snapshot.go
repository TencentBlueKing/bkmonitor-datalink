// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package projection

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
)

// RestoreSnapshot 恢复持久层重新排列 JSON 对象后的规范字节，不修改原摘要。
// ES update 会重新序列化整篇 _source；字段顺序不是业务变化。只有规范化后仍匹配
// 首次冻结摘要的数据才可恢复，数值使用 json.Number，避免精度损失或掩盖内容篡改。
func RestoreSnapshot(raw json.RawMessage, hash string) (json.RawMessage, error) {
	if len(raw) > 0 && raw[0] == '"' {
		var frozen string
		if len(raw) > 2*MaxSnapshotBytes+2 || json.Unmarshal(raw, &frozen) != nil {
			return nil, ErrInvalid
		}
		raw = []byte(frozen)
	}
	if len(raw) == 0 || len(raw) > MaxSnapshotBytes || !validHash(hash) {
		return nil, ErrInvalid
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) == hash {
		return raw, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return nil, ErrInvalid
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, ErrInvalid
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, ErrInvalid
	}
	sum = sha256.Sum256(canonical)
	if hex.EncodeToString(sum[:]) != hash {
		return nil, ErrInvalid
	}
	return canonical, nil
}

// PreserveSnapshot 把内部持久信封中的快照保存为字符串，使 ES update 保留其精确字节。
// path 只指向调用方固定的 request 路径；对外协议仍使用 JSON 对象。
func PreserveSnapshot(raw json.RawMessage, path ...string) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil, ErrInvalid
	}
	if len(path) > 0 {
		child, ok := fields[path[0]]
		if !ok {
			return nil, ErrInvalid
		}
		next, err := PreserveSnapshot(child, path[1:]...)
		if err != nil {
			return nil, err
		}
		fields[path[0]] = next
	} else {
		snapshot, ok := fields["alert"]
		if !ok || len(snapshot) == 0 {
			return nil, ErrInvalid
		}
		if snapshot[0] != '"' {
			encoded, err := json.Marshal(string(snapshot))
			if err != nil {
				return nil, err
			}
			fields["alert"] = encoded
		}
	}
	return json.Marshal(fields)
}
