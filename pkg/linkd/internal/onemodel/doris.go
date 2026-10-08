// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package onemodel

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"
)

// SQLReader 是 Doris 只读适配器使用的最小查询端口；连接生命周期由装配方管理。
type SQLReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// DorisConfig 对齐 KAC instance_storage 的实例表和投影边表；主线拓扑仍需独立 ES 数据。
type DorisConfig struct {
	Reader            SQLReader
	InstanceTable     string
	EdgeTable         string
	Timeout           time.Duration
	TopologyTransport ElasticsearchTransport
	IndexPrefix       string
}

type dorisReader struct {
	db        SQLReader
	instances string
	edges     string
	timeout   time.Duration
}

// ErrDataSourceUnavailable 表示查询依赖不可用，不能被当作合法空结果。
var ErrDataSourceUnavailable = errors.New("onemodel datasource unavailable")

type dorisFailure struct{ cause error }

func (e dorisFailure) Error() string { return "onemodel Doris query failed" }

func (e dorisFailure) Unwrap() error { return errors.Join(ErrDataSourceUnavailable, e.cause) }

// NewDorisClient 不连接服务，不回退旧实例 ES；Doris 不提供的主线拓扑仅使用显式 ES 连接。
func NewDorisClient(cfg DorisConfig) (*Client, error) {
	if cfg.Reader == nil || cfg.Timeout <= 0 || cfg.Timeout > time.Minute {
		return nil, fmt.Errorf("invalid Doris reader budget")
	}
	if cfg.InstanceTable == "" {
		cfg.InstanceTable = "kingeye_instance"
	}
	if cfg.EdgeTable == "" {
		cfg.EdgeTable = "kingeye_projection_edge"
	}
	quote := func(s string) (string, error) {
		if len(s) > 256 || !regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*(\.[a-zA-Z_][a-zA-Z0-9_]*)?$`).MatchString(s) {
			return "", fmt.Errorf("invalid Doris table identifier")
		}
		return "`" + strings.ReplaceAll(s, ".", "`.`") + "`", nil
	}
	instances, err := quote(cfg.InstanceTable)
	if err != nil {
		return nil, err
	}
	edges, err := quote(cfg.EdgeTable)
	if err != nil {
		return nil, err
	}
	if cfg.IndexPrefix == "" {
		cfg.IndexPrefix = "bk_monitor_base_"
	}
	if err = validateIndexPrefix(cfg.IndexPrefix); err != nil {
		return nil, err
	}
	return &Client{doris: &dorisReader{db: cfg.Reader, instances: instances, edges: edges, timeout: cfg.Timeout}, transport: cfg.TopologyTransport, topologyNodeIndex: cfg.IndexPrefix + "cmdb_biz_topo_node", topologyMembershipIndex: cfg.IndexPrefix + "cmdb_biz_topo_host_membership"}, nil
}

type sqlFragment struct {
	text string
	args []any
}

// dorisFilter 按 KAC Doris 实现使用类型化 JSON_EXTRACT_*、ARRAY_CONTAINS 和参数绑定。
// 不扩展字段和运算符；否定条件包含缺失值，与 KAC 的 include_missing 一致。
func dorisFilter(f Filter) (sqlFragment, error) {
	if f.Empty() {
		return sqlFragment{text: "TRUE"}, nil
	}
	if _, err := f.Compile(); err != nil {
		return sqlFragment{}, fmt.Errorf("%w: invalid filter", ErrInvalidQuery)
	}
	if f.Not != nil {
		x, err := dorisFilter(*f.Not)
		x.text = "NOT COALESCE((" + x.text + "), FALSE)"
		return x, err
	}
	if len(f.All) > 0 || len(f.Any) > 0 {
		list, join := f.All, " AND "
		if len(f.Any) > 0 {
			list, join = f.Any, " OR "
		}
		parts := []string{}
		args := []any{}
		for _, child := range list {
			x, err := dorisFilter(child)
			if err != nil {
				return sqlFragment{}, err
			}
			parts = append(parts, "("+x.text+")")
			args = append(args, x.args...)
		}
		return sqlFragment{strings.Join(parts, join), args}, nil
	}
	op := f.Operator
	switch op {
	case "ne", "not_in", "not_contains", "not_regex":
		f.Operator = map[string]string{"ne": "eq", "not_in": "in", "not_contains": "contains", "not_regex": "regex"}[op]
		x, err := dorisFilter(f)
		x.text = "NOT COALESCE((" + x.text + "), FALSE)"
		return x, err
	}
	expr := "`" + f.Field + "`"
	args := []any{}
	if strings.HasPrefix(f.Field, "attributes.") {
		fn := map[InstanceAttributeType]string{InstanceAttributeKeyword: "JSON_EXTRACT_STRING", InstanceAttributeIP: "JSON_EXTRACT_STRING", InstanceAttributeDatetime: "JSON_EXTRACT_STRING", InstanceAttributeLong: "JSON_EXTRACT_BIGINT", InstanceAttributeDouble: "JSON_EXTRACT_DOUBLE", InstanceAttributeBoolean: "JSON_EXTRACT_BOOL"}[f.Type]
		key, _ := json.Marshal(strings.TrimPrefix(f.Field, "attributes."))
		expr = fn + "(`attributes`, ?)"
		args = append(args, "$."+string(key))
	}
	if op == "exists" {
		if f.Field == "bk_biz_ids" {
			return sqlFragment{"ARRAY_SIZE(" + expr + ") > 0", args}, nil
		}
		return sqlFragment{expr + " IS NOT NULL", args}, nil
	}
	values := []any{f.Value}
	if op == "in" {
		raw, _ := json.Marshal(f.Value)
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		if err := d.Decode(&values); err != nil {
			return sqlFragment{}, ErrInvalidQuery
		}
	}
	for i, v := range values {
		typed, err := typedFilterValue(f.Type, v)
		if err != nil {
			return sqlFragment{}, ErrInvalidQuery
		}
		values[i] = typed
	}
	if f.Field == "bk_biz_ids" && (op == "eq" || op == "in") {
		clauses := []string{}
		params := []any{}
		for _, v := range values {
			clauses = append(clauses, "ARRAY_CONTAINS("+expr+", ?)")
			params = append(params, args...)
			params = append(params, v)
		}
		return sqlFragment{"(" + strings.Join(clauses, " OR ") + ")", params}, nil
	}
	switch op {
	case "in":
		return sqlFragment{expr + " IN (" + strings.TrimSuffix(strings.Repeat("?,", len(values)), ",") + ")", append(args, values...)}, nil
	case "contains":
		v, ok := values[0].(string)
		if !ok {
			return sqlFragment{}, ErrInvalidQuery
		}
		escaped := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(v)
		return sqlFragment{expr + " LIKE ? ESCAPE '\\\\'", append(args, "%"+escaped+"%")}, nil
	case "regex":
		return sqlFragment{expr + " REGEXP ?", append(args, values[0])}, nil
	default:
		sqlOp := map[string]string{"eq": "=", "gt": ">", "gte": ">=", "lt": "<", "lte": "<="}[op]
		if sqlOp == "" {
			return sqlFragment{}, ErrInvalidQuery
		}
		return sqlFragment{expr + " " + sqlOp + " ?", append(args, values[0])}, nil
	}
}

var instanceColumns = []string{"bk_tenant_id", "model_id", "model_inst_id", "entity_uid", "source", "display_name", "bk_biz_ids", "attributes", "attribute_values", "source_revision", "sync_time"}

var edgeColumns = []string{"bk_tenant_id", "producer", "source_model_id", "source_entity_uid", "target_model_id", "target_entity_uid", "relation_code", "relation_identity", "edge_level", "attributes", "source_revision", "sync_time"}

func (d *dorisReader) instancesPage(ctx context.Context, tenant, model string, where Filter, limit int, after string) ([]map[string]any, error) {
	x, err := dorisFilter(where)
	if err != nil {
		return nil, err
	}
	args := []any{tenant, model}
	args = append(args, x.args...)
	text := "`bk_tenant_id` = ? AND `model_id` = ? AND (" + x.text + ")"
	if after != "" {
		text += " AND `model_inst_id` > ?"
		args = append(args, after)
	}
	return d.rows(ctx, d.instances, instanceColumns, sqlFragment{text, args}, "`model_inst_id` ASC", limit)
}

// edgeRows 仅接受本包生成的精确条件，不暴露任意 SQL 或 ES DSL 查询面。
func (d *dorisReader) edgeRows(ctx context.Context, filters []any, limit int) ([]map[string]any, error) {
	clauses := []string{}
	args := []any{}
	tenant := ""
	for _, raw := range filters {
		m, ok := raw.(map[string]any)
		if !ok || len(m) != 1 {
			return nil, ErrInvalidQuery
		}
		for op, body := range m {
			fields, ok := body.(map[string]any)
			if !ok || len(fields) != 1 {
				return nil, ErrInvalidQuery
			}
			for field, value := range fields {
				if !slices.Contains(edgeColumns, field) || field == "attributes" || field == "sync_time" {
					return nil, ErrInvalidQuery
				}
				switch op {
				case "term":
					text, ok := value.(string)
					if !ok {
						return nil, ErrInvalidQuery
					}
					if field == "bk_tenant_id" {
						tenant = text
					}
					clauses = append(clauses, "`"+field+"` = ?")
					args = append(args, text)
				case "terms":
					values, ok := value.([]string)
					if !ok || len(values) < 1 || len(values) > 1024 {
						return nil, ErrInvalidQuery
					}
					clauses = append(clauses, "`"+field+"` IN ("+strings.TrimSuffix(strings.Repeat("?,", len(values)), ",")+")")
					for _, v := range values {
						args = append(args, v)
					}
				default:
					return nil, ErrInvalidQuery
				}
			}
		}
	}
	if tenant == "" {
		return nil, ErrInvalidQuery
	}
	rows, err := d.rows(ctx, d.edges, edgeColumns, sqlFragment{strings.Join(clauses, " AND "), args}, "`producer`, `source_entity_uid`, `target_entity_uid`, `relation_identity`", limit)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row["bk_tenant_id"] != tenant {
			return nil, ErrInvalidDataSourceResponse
		}
	}
	return rows, nil
}

// rows 限制总行数和响应字节；包括取连接在内受同一超时约束。任何部分结果失败都不返回空集合。
func (d *dorisReader) rows(ctx context.Context, table string, columns []string, where sqlFragment, order string, limit int) ([]map[string]any, error) {
	if ctx == nil || limit < 1 || limit > 1025 {
		return nil, ErrInvalidQuery
	}
	call, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()
	query := "SELECT `" + strings.Join(columns, "`, `") + "` FROM " + table + " WHERE " + where.text + " ORDER BY " + order + " LIMIT ?"
	rows, err := d.db.QueryContext(call, query, append(where.args, limit)...)
	if err != nil {
		if call.Err() != nil {
			return nil, call.Err()
		}
		return nil, dorisFailure{err}
	}
	defer func() { _ = rows.Close() }()
	result := []map[string]any{}
	size := 0
	for rows.Next() {
		if len(result) >= limit {
			return nil, ErrInvalidDataSourceResponse
		}
		values := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		if err = rows.Scan(dest...); err != nil {
			return nil, dorisFailure{err}
		}
		row := map[string]any{}
		for i, name := range columns {
			value := values[i]
			if b, ok := value.([]byte); ok {
				value = string(b)
			}
			if t, ok := value.(time.Time); ok {
				value = t.UTC().Format(time.RFC3339Nano)
			}
			if text, ok := value.(string); ok {
				size += len(text)
			}
			if size > maxOneModelResponseBytes {
				return nil, ErrResultLimit
			}
			if name == "attributes" || name == "attribute_values" || name == "bk_biz_ids" {
				text, ok := value.(string)
				if !ok {
					return nil, ErrInvalidDataSourceResponse
				}
				decoder := json.NewDecoder(strings.NewReader(text))
				decoder.UseNumber()
				if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF {
					return nil, ErrInvalidDataSourceResponse
				}
				if name == "attributes" {
					if _, ok = value.(map[string]any); !ok {
						return nil, ErrInvalidDataSourceResponse
					}
				} else if _, ok = value.([]any); !ok {
					return nil, ErrInvalidDataSourceResponse
				}
			}
			row[name] = value
		}
		result = append(result, row)
	}
	if err = rows.Err(); err != nil {
		if call.Err() != nil {
			return nil, call.Err()
		}
		return nil, dorisFailure{err}
	}
	if err = rows.Close(); err != nil {
		return nil, dorisFailure{err}
	}
	if call.Err() != nil {
		return nil, call.Err()
	}
	return result, nil
}
