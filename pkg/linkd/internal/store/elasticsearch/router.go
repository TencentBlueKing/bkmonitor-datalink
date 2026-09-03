// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearchstore

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"linkd/internal/store"
)

var targetPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._*-]{0,254}$`)

// Route 描述一个实体 ID 对应的写目标与一个或多个读目标。
// WriteTarget 通常是只指向一个物理索引的 write alias；ReadTargets 可以是物理索引或 read alias，
// 且在索引 refresh 后必须覆盖 WriteTarget 的具体索引，才能读取刚写入的实体。
type Route struct {
	// WriteTarget 是 create/CAS 操作使用的单一写目标。
	WriteTarget string
	// ReadTargets 是读取和冲突核对使用的有界目标集合。
	ReadTargets []string
	// RequireAlias 要求 Elasticsearch 拒绝把缺失的 write alias 自动创建为同名物理索引。
	RequireAlias bool
}

// Router 隔离时间桶周期、schema 版本和 alias 命名。
// 同一实体 ID 的 Route 必须稳定；AlertLog 始终按可解析的父 AlertID 共置，
// EventScanTargets 必须返回有界且完整的集合。
type Router interface {
	EventRoute(ctx context.Context, eventID string) (Route, error)
	EventScanTargets(ctx context.Context, receivedBefore time.Time) ([]string, error)
	EventRangeTargets(ctx context.Context, receivedFrom, receivedTo time.Time) ([]string, error)
	AlertRoute(ctx context.Context, alertID string) (Route, error)
	AlertHistoryRoute(ctx context.Context, alertID string) (Route, error)
	ActiveAlertTargets(ctx context.Context) ([]string, error)
	TerminalAlertTargets(ctx context.Context) ([]string, error)
	AlertLogWriteRoute(ctx context.Context, alertID, logID string) (Route, error)
	AlertLogReadRoute(ctx context.Context, alertID string) (Route, error)
}

func normalizeRoute(route Route, maxReadTargets int) (Route, error) {
	if err := validateTarget("write target", route.WriteTarget); err != nil {
		return Route{}, err
	}
	if strings.Contains(route.WriteTarget, "*") {
		return Route{}, fmt.Errorf("%w: elasticsearch write target must not contain wildcard", store.ErrInvalidArgument)
	}
	readTargets, err := normalizeTargets(route.ReadTargets, maxReadTargets)
	if err != nil {
		return Route{}, err
	}
	route.ReadTargets = readTargets
	return route, nil
}

func normalizeTargets(targets []string, maximum int) ([]string, error) {
	if len(targets) == 0 || len(targets) > maximum {
		return nil, fmt.Errorf(
			"%w: elasticsearch target count must be between 1 and %d",
			store.ErrInvalidArgument,
			maximum,
		)
	}
	seen := make(map[string]struct{}, len(targets))
	normalized := make([]string, 0, len(targets))
	for _, target := range targets {
		if err := validateTarget("read target", target); err != nil {
			return nil, err
		}
		if _, exists := seen[target]; exists {
			continue
		}
		seen[target] = struct{}{}
		normalized = append(normalized, target)
	}
	if len(normalized) == 0 {
		return nil, fmt.Errorf("%w: elasticsearch read targets must not be empty", store.ErrInvalidArgument)
	}
	return normalized, nil
}

func validateTarget(name, target string) error {
	if !targetPattern.MatchString(target) || strings.Contains(target, "..") {
		return fmt.Errorf("%w: elasticsearch %s is invalid: %q", store.ErrInvalidArgument, name, target)
	}
	return nil
}
