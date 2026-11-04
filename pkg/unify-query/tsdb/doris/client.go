// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package doris

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/bksql"
)

type connectionConfig struct{ address, username, password string }

type poolEntry struct {
	config connectionConfig
	db     *sql.DB
}

var pools = struct {
	sync.Mutex
	entries map[string]poolEntry
}{entries: make(map[string]poolEntry)}

// Client 仅适配执行协议；SQL 生成和业务响应继续由 bksql.Instance 负责。
type Client struct {
	db     *sql.DB
	tables map[string]metadata.PhysicalTable
}

func NewClient(storageID, address, username, password string, timeout time.Duration, tables []metadata.PhysicalTable) (*Client, error) {
	if address == "" {
		return nil, fmt.Errorf("Doris storage address is empty")
	}
	config := connectionConfig{address, username, password}
	pools.Lock()
	defer pools.Unlock()
	entry, ok := pools.entries[storageID]
	if !ok || entry.config != config {
		cfg := mysql.NewConfig()
		cfg.Net = "tcp"
		cfg.Addr = address
		cfg.User = username
		cfg.Passwd = password
		cfg.Timeout = timeout
		cfg.ParseTime = true
		cfg.Loc = time.FixedZone("UTC+8", 8*3600)
		cfg.Params = map[string]string{"time_zone": "'+08:00'"}
		connector, err := mysql.NewConnector(cfg)
		if err != nil {
			return nil, err
		}
		db := sql.OpenDB(connector)
		db.SetMaxOpenConns(10)
		db.SetMaxIdleConns(5)
		db.SetConnMaxLifetime(5 * time.Minute)
		if ok {
			entry.db.Close()
		}
		entry = poolEntry{config: config, db: db}
		pools.entries[storageID] = entry
	}
	c := &Client{db: entry.db, tables: make(map[string]metadata.PhysicalTable, len(tables))}
	for _, t := range tables {
		c.tables[metadata.PhysicalTableSQL(t)] = t
	}
	return c, nil
}

func (c *Client) QuerySync(ctx context.Context, req bksql.QuerySyncRequest, span *trace.Span) *bksql.Result {
	data, err := c.query(ctx, req.SQL)
	if err != nil {
		return &bksql.Result{Code: bksql.StatusFailed, Message: err.Error(), Err: err}
	}
	return &bksql.Result{Result: true, Code: bksql.StatusOK, Data: data}
}

func (c *Client) query(ctx context.Context, statement string) (*bksql.QuerySyncResultData, error) {
	conn, err := c.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	tableName := strings.TrimPrefix(statement, "DESC ")
	describing := strings.HasPrefix(statement, "DESC ")
	if describing {
		// SET 与 DESC 必须在同一连接中；失败不视为缺字段。
		if _, err = conn.ExecContext(ctx, "SET describe_extend_variant_column = true"); err != nil {
			return nil, err
		}
	}
	start := time.Now()
	rows, err := conn.QueryContext(ctx, statement)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}
	data := &bksql.QuerySyncResultData{Sql: statement, Device: metadata.DorisStorageType, List: []map[string]any{}}
	for i, col := range columns {
		data.SelectFieldsOrder = append(data.SelectFieldsOrder, col.Name())
		name := fmt.Sprintf("__c%d", i)
		if describing {
			name = col.Name()
		}
		data.ResultSchema = append(data.ResultSchema, map[string]any{"field_name": name, "field_alias": col.Name(), "field_index": i, "field_type": schemaType(col.DatabaseTypeName())})
	}
	for rows.Next() {
		values := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		if err = rows.Scan(dest...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(columns))
		for i, col := range columns {
			value, convertErr := convertValue(values[i], col.DatabaseTypeName())
			if convertErr != nil {
				return nil, fmt.Errorf("column %s: %w", col.Name(), convertErr)
			}
			row[col.Name()] = value
		}
		if describing {
			if typ, ok := row["Type"].(string); ok {
				row["Type"] = strings.ToLower(typ)
			}
			row["Analyzed"] = "false"
			for _, field := range c.tables[tableName].AnalyzedFields {
				if row["Field"] == field {
					row["Analyzed"] = "true"
					break
				}
			}
		}
		data.List = append(data.List, row)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	data.TotalRecords = len(data.List)
	data.Timetaken = time.Since(start).Seconds()
	return data, nil
}

func schemaType(t string) string {
	switch strings.ToUpper(t) {
	case "BIGINT", "UNSIGNED BIGINT", "DATETIME", "TIMESTAMP":
		return "long"
	case "TINYINT", "SMALLINT", "MEDIUMINT", "INT", "INTEGER", "BOOLEAN", "BOOL":
		return "int"
	case "FLOAT":
		return "float"
	case "DOUBLE", "DECIMAL", "NEWDECIMAL":
		return "double"
	default:
		return "string"
	}
}

func convertValue(v any, typ string) (any, error) {
	if v == nil {
		return nil, nil
	}
	typ = strings.ToUpper(typ)
	if tm, ok := v.(time.Time); ok {
		if typ == "DATE" {
			return tm.Format("2006-01-02"), nil
		}
		return tm.UnixMilli(), nil
	}
	if flag, ok := v.(bool); ok {
		if flag {
			return int64(1), nil
		}
		return int64(0), nil
	}
	var value string
	switch x := v.(type) {
	case []byte:
		value = string(x)
	case string:
		value = x
	default:
		return v, nil
	}
	switch typ {
	case "TINYINT", "SMALLINT", "MEDIUMINT", "INT", "INTEGER", "BIGINT", "BOOLEAN", "BOOL":
		return strconv.ParseInt(value, 10, 64)
	case "UNSIGNED TINYINT", "UNSIGNED SMALLINT", "UNSIGNED MEDIUMINT", "UNSIGNED INT", "UNSIGNED BIGINT":
		return strconv.ParseUint(value, 10, 64)
	case "FLOAT", "DOUBLE", "DECIMAL", "NEWDECIMAL":
		return strconv.ParseFloat(value, 64)
	default:
		return value, nil
	}
}
