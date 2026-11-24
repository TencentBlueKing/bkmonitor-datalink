// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package store

import "maps"

// ActiveAlertLimitDecision 保存拦截当时的近似数量，不承诺与当前活跃数量一致。
type ActiveAlertLimitDecision struct {
	Rule      string            `json:"rule"`
	Group     map[string]string `json:"group"`
	Count     int64             `json:"count"`
	MaxActive int64             `json:"max_active"`
}

// Clone 隔离诊断中的分组字段。
func (d *ActiveAlertLimitDecision) Clone() *ActiveAlertLimitDecision {
	if d == nil {
		return nil
	}
	v := *d
	v.Group = maps.Clone(d.Group)
	return &v
}
