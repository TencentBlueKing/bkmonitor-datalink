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
	"errors"
	"fmt"
	"strconv"
	"strings"

	driver "github.com/go-sql-driver/mysql"
	"linkd/internal/store"
)

const (
	maxIdentityBytes = 255
	versionPrefix    = "mysql:"
)

var _ store.Repository = (*Repository)(nil)

// Repository 使用 database/sql 实现 Linkd 的完整存储契约。
// 连接池和事务重试策略由创建 sql.DB 的进程装配层负责。
type Repository struct {
	db *sql.DB
}

// New 使用调用方持有的 MySQL 连接池创建 Repository。
// Repository 不取得 db 的所有权，调用方仍负责 Ping、连接池参数和 Close。
func New(db *sql.DB) (*Repository, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: mysql db must not be nil", store.ErrInvalidArgument)
	}
	return &Repository{db: db}, nil
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("%w: context must not be nil", store.ErrInvalidArgument)
	}
	return ctx.Err()
}

func validateIdentity(bkTenantID, idName, id string) error {
	if err := validateIdentityPart("bk_tenant_id", bkTenantID); err != nil {
		return err
	}
	return validateIdentityPart(idName, id)
}

func validateIdentityPart(name, value string) error {
	if value == "" {
		return fmt.Errorf("%w: %s must not be empty", store.ErrInvalidArgument, name)
	}
	if len(value) > maxIdentityBytes {
		return fmt.Errorf(
			"%w: %s exceeds mysql VARBINARY(%d)",
			store.ErrInvalidArgument,
			name,
			maxIdentityBytes,
		)
	}
	return nil
}

func normalizeBatchIDs(bkTenantID, idName string, ids []string) ([]string, error) {
	if err := validateIdentityPart("bk_tenant_id", bkTenantID); err != nil {
		return nil, err
	}
	if len(ids) == 0 || len(ids) > store.MaxBatchSize {
		return nil, fmt.Errorf(
			"%w: %s batch size must be between 1 and %d",
			store.ErrInvalidArgument,
			idName,
			store.MaxBatchSize,
		)
	}
	seen := make(map[string]struct{}, len(ids))
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		if err := validateIdentityPart(idName, id); err != nil {
			return nil, err
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result, nil
}

func placeholders(count int) string {
	return strings.TrimSuffix(strings.Repeat("?,", count), ",")
}

func versionToken(version uint64) store.VersionToken {
	return store.NewVersionToken(versionPrefix + strconv.FormatUint(version, 10))
}

func parseVersion(token store.VersionToken) (uint64, bool) {
	value := token.String()
	if !strings.HasPrefix(value, versionPrefix) {
		return 0, false
	}
	version, err := strconv.ParseUint(strings.TrimPrefix(value, versionPrefix), 10, 64)
	if err != nil || version == 0 {
		return 0, false
	}
	return version, true
}

func isDuplicateKey(err error) bool {
	var mysqlError *driver.MySQLError
	return errors.As(err, &mysqlError) && mysqlError.Number == 1062
}

func rollback(tx *sql.Tx) {
	_ = tx.Rollback()
}
