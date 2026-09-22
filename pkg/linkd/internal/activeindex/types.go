// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package activeindex 由控制面把持久化 Active Alert 投影为 Redis 策略集合。
// Hook 只提交可合并的刷新提示；完整读取和单写入者发布共同保证最终一致性。
package activeindex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"linkd/internal/domain"
)

// Scope 是集合的租户和策略身份，沿用对外通知协议。
type Scope struct {
	BKTenantID string `json:"bk_tenant_id"`
	StrategyID string `json:"strategy_id"`
}

// ValidatePrefix 保留内部元数据命名空间，避免正式集合扫描与元数据互相覆盖。
func ValidatePrefix(prefix string) error {
	const reserved = "linkd:active-index:"
	if prefix == "" || strings.TrimSpace(prefix) != prefix || len(prefix) > 256 {
		return fmt.Errorf("key_prefix must be 1 to 256 bytes without surrounding whitespace")
	}
	if strings.HasPrefix(reserved, prefix+":") || strings.HasPrefix(prefix+":", reserved) {
		return fmt.Errorf("key_prefix overlaps reserved active index metadata namespace")
	}
	return nil
}

// Validate 拒绝不满足集合身份及资源边界的作用域。
func (s Scope) Validate() error {
	if err := domain.ValidateIdentityPart("bk_tenant_id", s.BKTenantID, 64); err != nil {
		return err
	}
	if s.StrategyID == "" || len(s.StrategyID) > 1024 {
		return fmt.Errorf("invalid strategy_id")
	}
	return nil
}

// Key 返回既有对外集合键，不改变 fingerprint 消费协议。
func (s Scope) Key(prefix string) string { return prefix + ":" + s.BKTenantID + ":" + s.StrategyID }

// ParseKey 只解析指定前缀的正式集合，租户身份不允许冒号。
func ParseKey(prefix, key string) (Scope, error) {
	v, ok := strings.CutPrefix(key, prefix+":")
	tenant, strategy, split := strings.Cut(v, ":")
	s := Scope{tenant, strategy}
	if !ok || !split {
		return s, fmt.Errorf("invalid strategy key")
	}
	return s, s.Validate()
}

// MetadataPrefix 隔离内部队列、租约及状态，不与正式集合扫描混用。
func MetadataPrefix(prefix string) string {
	sum := sha256.Sum256([]byte(prefix))
	return "linkd:active-index:" + hex.EncodeToString(sum[:])
}

// StrategyID 与 Hook 及查询适配器共用标签转换规则；缺省或空字符串跳过。
func StrategyID(labels domain.DimensionMap) (string, bool, error) {
	scalar, exists := labels["strategy_id"]
	if !exists {
		return "", true, nil
	}
	if v, ok := scalar.StringValue(); ok {
		return v, v == "", nil
	}
	if v, ok := scalar.NumberValue(); ok && scalar.Valid() {
		return strconv.FormatFloat(v, 'f', -1, 64), false, nil
	}
	return "", false, fmt.Errorf("strategy_id must be a string or finite number")
}

// Row 是仅供缓存投影使用的窄告警快照，不读取告警正文。
type Row struct {
	BKTenantID    string              `json:"bk_tenant_id"`
	EventSourceID string              `json:"event_source_id"`
	Fingerprint   string              `json:"fingerprint"`
	Labels        domain.DimensionMap `json:"labels"`
}

// Query 限定完整读取范围；Scope 为空用于周期发现所有租户和策略。
type Query struct {
	Sources  []string
	Scope    *Scope
	MaxRows  int
	MaxBytes int
}

// Validate 校验扫描硬上限，禁止空来源变成全库扫描。
func (q Query) Validate() error {
	if len(q.Sources) == 0 || len(q.Sources) > 64 || q.MaxRows < 1 || q.MaxRows > 1000000 || q.MaxBytes < 1 || q.MaxBytes > 64<<20 {
		return fmt.Errorf("invalid active index query limits")
	}
	for _, s := range q.Sources {
		if err := domain.ValidateIdentityPart("event_source_id", s, 32); err != nil {
			return err
		}
	}
	if q.Scope != nil {
		return q.Scope.Validate()
	}
	return nil
}

// Group 再次核对存储作用域并按租户、策略去重。任意非法行都拒绝整个快照。
func Group(q Query, rows []Row) (map[Scope][]string, error) {
	groups := make(map[Scope][]string)
	for _, row := range rows {
		if !slices.Contains(q.Sources, row.EventSourceID) || row.Fingerprint == "" || len(row.Fingerprint) > 128 {
			return nil, fmt.Errorf("invalid active index row")
		}
		strategy, skip, err := StrategyID(row.Labels)
		if err != nil {
			return nil, err
		}
		if skip {
			continue
		}
		scope := Scope{row.BKTenantID, strategy}
		if err := scope.Validate(); err != nil {
			return nil, err
		}
		if q.Scope != nil {
			if scope.BKTenantID != q.Scope.BKTenantID {
				return nil, fmt.Errorf("active index tenant mismatch")
			}
			if scope.StrategyID != q.Scope.StrategyID {
				continue
			}
		}
		groups[scope] = append(groups[scope], row.Fingerprint)
	}
	for scope, members := range groups {
		slices.Sort(members)
		groups[scope] = slices.Compact(members)
	}
	return groups, nil
}

// Reader 成功时必须返回整个查询范围的一致读取快照；超限、缺索引、部分分片失败均返回错误。
// Elasticsearch 快照只覆盖已经 refresh 的数据，不承诺读到最近一次写入。
type Reader interface {
	ReadActiveIndex(context.Context, Query) ([]Row, error)
}

// NumericStrategy 仅对规范数字文本允许数值匹配，避免 "00123" 与 123 混淆。
func NumericStrategy(strategy string) (float64, bool) {
	n, err := strconv.ParseFloat(strategy, 64)
	return n, err == nil && !math.IsInf(n, 0) && !math.IsNaN(n) && strconv.FormatFloat(n, 'f', -1, 64) == strategy
}
