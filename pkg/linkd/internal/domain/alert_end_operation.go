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
	"fmt"
	"strings"
)

// AlertEndOperation 固定主动关闭命令身份；与终态原因、时间共同约束重复请求。
// 来源恢复和级别升级不伪造关闭命令，保持 nil。
type AlertEndOperation struct {
	ID           string       `json:"id"`
	Source       string       `json:"source"`
	OperatorKind OperatorKind `json:"operator_kind"`
	OperatorID   string       `json:"operator_id"`
	ConfigDigest string       `json:"config_digest,omitempty"`
}

// Validate 限制已定义的操作来源，区分业务入口与操作者类别。
func (o AlertEndOperation) Validate() error {
	if strings.TrimSpace(o.ID) == "" || len(o.ID) > 128 || strings.TrimSpace(o.OperatorID) == "" || len(o.OperatorID) > 256 || len(o.ConfigDigest) > 256 {
		return fmt.Errorf("invalid end operation identity")
	}
	if o.OperatorKind != OperatorKindUser && o.OperatorKind != OperatorKindSystem {
		return fmt.Errorf("invalid end operator kind")
	}
	switch o.Source {
	case "manual":
		if o.OperatorKind != OperatorKindUser {
			return fmt.Errorf("manual operation requires user")
		}
	case "auto_policy", "self_heal", "strategy_change", "system":
		if o.OperatorKind != OperatorKindSystem {
			return fmt.Errorf("automatic operation requires system")
		}
	case "work_order":
	default:
		return fmt.Errorf("invalid end operation source")
	}
	return nil
}

func (o *AlertEndOperation) clone() *AlertEndOperation {
	if o == nil {
		return nil
	}
	v := *o
	return &v
}
