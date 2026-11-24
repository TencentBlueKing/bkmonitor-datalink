// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// MergeDecisionID 只由租户和固定窗口确定，以便 Redis 丢失后直接查找已经持久化的裁决。
// 一窗只允许一个裁决；成员集合属于该裁决的不可变事实，不参与操作身份，也不替代父告警指纹。
func MergeDecisionID(tenant, window string) (string, error) {
	if err := ValidateIdentityPart("tenant", tenant, 64); err != nil {
		return "", err
	}
	raw, err := hex.DecodeString(window)
	if err != nil || len(raw) != 32 || window != hex.EncodeToString(raw) {
		return "", fmt.Errorf("invalid merge window identity")
	}
	encoded, _ := json.Marshal([]string{"merge-decision", tenant, window})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}
