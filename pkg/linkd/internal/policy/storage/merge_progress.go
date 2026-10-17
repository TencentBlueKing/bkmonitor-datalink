// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"linkd/internal/policy"
)

// CompareAndSetMergeProgress 只提交执行进度；成员位置来自同版本的固定成员列表。
// 返回提交后的版本，调用方复用已经验证的不可变快照，不再读回整组。
func (s *Store) CompareAndSetMergeProgress(ctx context.Context, kind, id, expected string, fields map[string]any, members map[int]string, work bool) (string, error) {
	if !isMergeWork(kind) || expected == "" {
		return "", policy.ErrInvalid
	}
	if err := valid(kind, id); err != nil {
		return "", err
	}
	if s.db != nil {
		version, err := strconv.ParseUint(expected, 10, 64)
		if err != nil {
			return "", err
		}
		// 1. 字段值和 JSON 路径均绑定参数；SQL 不包含调用方提供的文本。
		names := make([]string, 0, len(fields))
		for name := range fields {
			names = append(names, name)
		}
		slices.Sort(names)
		args := []any{}
		pairs := []string{}
		for _, name := range names {
			raw, err := json.Marshal(fields[name])
			if err != nil {
				return "", err
			}
			pairs = append(pairs, "?,CAST(? AS JSON)")
			args = append(args, "$."+name, string(raw))
		}
		for index, state := range members {
			pairs = append(pairs, "?,?")
			args = append(args, fmt.Sprintf("$.members[%d].state", index), state)
		}
		args = append(args, work, s.namespace, id, version)
		// 2. 版本条件及派生 work 与进度一次提交；零行仍由原重试流程重读。
		//nolint:gosec // G202: kind 已限定，pairs 只含固定占位符。
		result, err := s.db.ExecContext(ctx, "UPDATE "+s.table(kind)+" SET payload=JSON_SET(CONVERT(payload USING utf8mb4),"+strings.Join(pairs, ",")+"),work=?,version=version+1 WHERE namespace=? AND id=? AND version=?", args...)
		if err != nil {
			return "", err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return "", err
		}
		if n != 1 {
			return "", policy.ErrConflict
		}
		return strconv.FormatUint(version+1, 10), nil
	}
	var seq, term int64
	if _, err := fmt.Sscanf(expected, "%d:%d", &seq, &term); err != nil {
		return "", err
	}
	states := make([]map[string]any, 0, len(members))
	for index, state := range members {
		states = append(states, map[string]any{"index": index, "state": state})
	}
	// 3. 脚本仅替换可变字段与本批成员状态；不把整个 members 数组发回 ES。
	raw, err := json.Marshal(map[string]any{"script": map[string]any{
		"lang":   "painless",
		"source": "for (def e : params.fields.entrySet()) { ctx._source.payload[e.getKey()] = e.getValue(); } for (def m : params.members) { ctx._source.payload.members[m.index].state = m.state; } ctx._source.work = params.work;",
		"params": map[string]any{"fields": fields, "members": states, "work": work},
	}})
	if err != nil {
		return "", err
	}
	q := url.Values{"refresh": {"false"}, "if_seq_no": {strconv.FormatInt(seq, 10)}, "if_primary_term": {strconv.FormatInt(term, 10)}}
	status, raw, err := s.request(ctx, http.MethodPost, "/"+s.table(kind)+"/_update/"+url.PathEscape(id)+"?"+q.Encode(), raw)
	if err != nil {
		return "", err
	}
	if status == http.StatusConflict || status == http.StatusNotFound {
		return "", policy.ErrConflict
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("update merge progress: HTTP %d", status)
	}
	var response struct {
		Seq  int64 `json:"_seq_no"`
		Term int64 `json:"_primary_term"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d:%d", response.Seq, response.Term), nil
}
