// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package mysqlstore

import (
	"context"
	"embed"
	"fmt"
	"strings"
)

const migrationSeparator = "-- linkd:statement"

//go:embed migrations/*.sql
var migrationFiles embed.FS

// EnsureSchema 幂等创建当前 MySQL Repository 所需的三张实体表。
// 当前项目尚未发布稳定 schema，字段调整直接收敛到首版 001_init.sql，不维护空的历史迁移链。
func (r *Repository) EnsureSchema(ctx context.Context) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	data, err := migrationFiles.ReadFile("migrations/001_init.sql")
	if err != nil {
		return fmt.Errorf("read embedded mysql migration: %w", err)
	}
	for _, statement := range strings.Split(string(data), migrationSeparator) {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			continue
		}
		if _, err := r.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("execute mysql schema statement: %w", err)
		}
	}
	return nil
}
