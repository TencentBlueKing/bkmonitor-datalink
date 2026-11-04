// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/viper"

	router "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/router/influxdb"
)

const QueryFlagsHeader = "X-Bk-Query-Flags"

const DorisDirectQueryConfigPath = "query.flags.doris_direct_query"

// QueryFlags 是请求偏好；nil 表示使用服务默认配置。
type QueryFlags struct {
	DorisDirectQuery *bool `json:"doris_direct_query,omitempty"`
}

type EffectiveQueryFlags struct {
	DorisDirectQuery bool
}

type queryFlagsContextKey struct{}

func ParseQueryFlags(header string) (QueryFlags, error) {
	var flags QueryFlags
	if strings.TrimSpace(header) == "" {
		return flags, nil
	}
	if !strings.HasPrefix(strings.TrimSpace(header), "{") {
		return flags, fmt.Errorf("%s must be a JSON object", QueryFlagsHeader)
	}
	if err := json.Unmarshal([]byte(header), &flags); err != nil {
		return flags, fmt.Errorf("invalid %s: %w", QueryFlagsHeader, err)
	}
	return flags, nil
}

// WithQueryFlags 在请求入口固定有效值，派生 context 会自动继承。
func WithQueryFlags(ctx context.Context, flags QueryFlags) context.Context {
	effective := EffectiveQueryFlags{DorisDirectQuery: viper.GetBool(DorisDirectQueryConfigPath)}
	if flags.DorisDirectQuery != nil {
		effective.DorisDirectQuery = *flags.DorisDirectQuery
	}
	return context.WithValue(ctx, queryFlagsContextKey{}, effective)
}

func GetQueryFlags(ctx context.Context) EffectiveQueryFlags {
	if flags, ok := ctx.Value(queryFlagsContextKey{}).(EffectiveQueryFlags); ok {
		return flags
	}
	return EffectiveQueryFlags{DorisDirectQuery: viper.GetBool(DorisDirectQueryConfigPath)}
}

// SelectDorisQueryBackend 只依据偏好和目标配置选路，不因执行失败切换后端。
func SelectDorisQueryBackend(flags EffectiveQueryFlags, hasPhysicalTable, hasBKBaseTable bool) (string, error) {
	if hasPhysicalTable && (flags.DorisDirectQuery || !hasBKBaseTable) {
		return DorisStorageType, nil
	}
	if hasBKBaseTable {
		return BkSqlStorageType, nil
	}
	return "", fmt.Errorf("doris query has neither physical table nor BKBase table ID")
}

// PhysicalTable 与 Redis 路由保持同一协议。
type PhysicalTable = router.PhysicalTable

func PhysicalTableSQL(t PhysicalTable) string {
	return "`" + strings.ReplaceAll(t.Database, "`", "``") + "`.`" + strings.ReplaceAll(t.Table, "`", "``") + "`"
}

// ResolveDorisBackend 在路由已完成历史目标覆盖后调用。
func (q *Query) ResolveDorisBackend(ctx context.Context) error {
	if q.QueryBackend != "" || q.Measurement != DorisStorageType || (q.StorageType != BkSqlStorageType && q.StorageType != DorisStorageType) {
		return nil
	}
	t := q.PhysicalTables["main"]
	backend, err := SelectDorisQueryBackend(GetQueryFlags(ctx), t.Database != "" && t.Table != "", q.DB != "" || len(q.DBs) > 0)
	if q.StorageType == DorisStorageType {
		q.QueryBackend = backend
	}
	if backend == DorisStorageType {
		q.QueryBackend = backend
		q.DirectTables = []PhysicalTable{t}
	}
	return err
}

func (q *Query) DorisTables() []PhysicalTable {
	if len(q.DirectTables) > 0 {
		return q.DirectTables
	}
	if t, ok := q.PhysicalTables["main"]; ok {
		return []PhysicalTable{t}
	}
	return nil
}
