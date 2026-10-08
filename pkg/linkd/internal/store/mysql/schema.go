// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package mysqlstore

import (
	"context"
	"database/sql"
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
	for _, column := range []string{"policy_work", "merge_work", "projection_work", "action_work"} {
		if err := r.ensureAlertWorkIndex(ctx, column); err != nil {
			return err
		}
	}
	return nil
}

// 已有表只追加未出现过的控制面工作标记与索引，保留原数据；并发初始化重读确认胜出结果。
func (r *Repository) ensureAlertWorkIndex(ctx context.Context, column string) error {
	if column != "policy_work" && column != "merge_work" && column != "projection_work" && column != "action_work" {
		return fmt.Errorf("invalid alert work column")
	}
	indexName := "idx_linkd_alert_" + column
	expectedColumns := column + ",bk_tenant_id,alert_id"
	read := func() (bool, error) {
		var kind, nullable string
		err := r.db.QueryRowContext(ctx, `SELECT DATA_TYPE,IS_NULLABLE FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='linkd_alerts' AND COLUMN_NAME=?`, column).Scan(&kind, &nullable)
		if err == sql.ErrNoRows {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if kind != "tinyint" || nullable != "NO" {
			return false, fmt.Errorf("incompatible %s column", column)
		}
		return true, nil
	}
	exists, err := read()
	if err != nil {
		return err
	}
	if !exists {
		if _, err := r.db.ExecContext(ctx, "ALTER TABLE linkd_alerts ADD COLUMN "+column+" TINYINT NOT NULL DEFAULT 0"); err != nil {
			if ok, checkErr := read(); checkErr != nil || !ok {
				return fmt.Errorf("add %s: %w", column, err)
			}
		}
	}
	index := func() (string, error) {
		var cols sql.NullString
		err := r.db.QueryRowContext(ctx, `SELECT GROUP_CONCAT(COLUMN_NAME ORDER BY SEQ_IN_INDEX) FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='linkd_alerts' AND INDEX_NAME=?`, indexName).Scan(&cols)
		return cols.String, err
	}
	cols, err := index()
	if err != nil {
		return err
	}
	if cols == "" {
		if _, err := r.db.ExecContext(ctx, "CREATE INDEX "+indexName+" ON linkd_alerts("+expectedColumns+")"); err != nil {
			if actual, checkErr := index(); checkErr != nil || actual != expectedColumns {
				return fmt.Errorf("add %s index: %w", column, err)
			}
		}
		cols, err = index()
		if err != nil {
			return err
		}
	}
	if cols != expectedColumns {
		return fmt.Errorf("incompatible %s index", column)
	}
	return nil
}
