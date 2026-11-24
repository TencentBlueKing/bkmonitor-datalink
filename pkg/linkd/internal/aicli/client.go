// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package aicli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const responseLimit int64 = 16 << 20

func apiCommand(opts *options) *cobra.Command {
	root := &cobra.Command{Use: "api", Short: "发现及调用固定 Console 接口；默认只读"}
	root.AddCommand(&cobra.Command{Use: "list", Short: "离线列出接口及读写属性，完整参数见 describe", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		// 目录只返回索引，详细参数按需 describe，避免 AI 为一次排障加载全部接口 schema。
		items := make([]map[string]any, 0)
		for _, op := range catalog() {
			items = append(items, map[string]any{"name": op.Name, "description": op.Description, "method": op.Method, "path": op.Path, "requires_write": op.RequiresWrite})
		}
		return emit(cmd.OutOrStdout(), items)
	}})
	root.AddCommand(&cobra.Command{Use: "describe <operation>", Short: "离线查看接口参数、示例、影响及分页方式", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		op, err := findOperation(args[0])
		if err != nil {
			return err
		}
		return emit(cmd.OutOrStdout(), op)
	}})
	var pathPairs, queryPairs []string
	var bodyFile string
	var dryRun, allowWrite bool
	call := &cobra.Command{Use: "call <operation>", Short: "调用接口；操作需最终人工确认后显式 --allow-write", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		op, err := findOperation(args[0])
		if err != nil {
			return err
		}
		// 必须在读取连接、请求体及创建网络客户端前检查，不从环境或配置导入操作授权。
		if op.RequiresWrite && !dryRun && (!cmd.Flags().Changed("allow-write") || !allowWrite) {
			return problem("write_confirmation_required", "操作默认禁止；先 --dry-run，向用户确认最终环境、参数、范围及影响，再显式 --allow-write")
		}
		paths, err := parsePairs(pathPairs, op.PathParams, true)
		if err != nil {
			return err
		}
		query, err := parsePairs(queryPairs, op.QueryParams, false)
		if err != nil {
			return err
		}
		body, err := readBody(cmd, op, bodyFile)
		if err != nil {
			return err
		}
		if err := validateRequest(op, paths, query, body); err != nil {
			return err
		}
		name, p, err := opts.selected()
		if err != nil {
			return err
		}
		target := requestURL(p, op, paths, query)
		redact := connectionRedactor(p)
		if dryRun {
			return emit(cmd.OutOrStdout(), redact.value(map[string]any{"dry_run": true, "network_request_sent": false, "profile": name, "base_url": p.URL, "operation": op.Name, "method": op.Method, "url": target, "path": stringMap(paths), "query": stringMap(query), "body": body, "requires_write": op.RequiresWrite, "impact": op.Impact, "validation": "仅本地结构校验；不验证远端 revision、业务配置、对象存在性或权限"}))
		}
		data, status, err := callHTTP(cmd.Context(), p, op, target, body)
		if err != nil {
			return err
		}
		if err := emit(cmd.OutOrStdout(), map[string]any{"profile": name, "operation": op.Name, "http_status": status, "data": redact.value(data)}); err != nil {
			return operationFailure(op, "output_error", "HTTP 请求已成功，但结果输出失败，请先核对状态", status, op.RequiresWrite)
		}
		return nil
	}}
	call.Flags().StringArrayVar(&pathPairs, "path", nil, "路径身份 key=value，可重复，不接受预转义值")
	call.Flags().StringArrayVar(&queryPairs, "query", nil, "查询参数 key=value，可重复，游标由调用者显式传入")
	call.Flags().StringVar(&bodyFile, "body-file", "", "JSON 请求文件，- 表示标准输入")
	call.Flags().BoolVar(&dryRun, "dry-run", false, "仅本地校验和脱敏预览，零网络请求")
	call.Flags().BoolVar(&allowWrite, "allow-write", false, "仅允许本次操作；不能代替用户对最终方案的确认")
	root.AddCommand(call)
	return root
}

func findOperation(name string) (operation, error) {
	for _, op := range catalog() {
		if op.Name == name {
			return op, nil
		}
	}
	return operation{}, problem("unknown_operation", "接口未登记，请先 api list；不支持任意 URL 或 HTTP 方法")
}

func stringMap(values map[string]string) map[string]any {
	result := make(map[string]any, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func readBody(cmd *cobra.Command, op operation, path string) (map[string]any, error) {
	if len(op.BodyParams) == 0 {
		if path != "" {
			return nil, problem("invalid_argument", "此接口不接受请求体")
		}
		return nil, nil
	}
	if path == "" {
		return nil, problem("invalid_argument", "此接口必须通过 --body-file 提供 JSON 对象")
	}
	reader := cmd.InOrStdin()
	if path != "-" {
		f, err := os.Open(path) //nolint:gosec // 文件路径由本地用户通过 --body-file 显式指定，不来自远端响应。
		if err != nil {
			return nil, problem("io_error", "无法打开请求文件")
		}
		defer func() { _ = f.Close() }()
		reader = f
	}
	raw, err := readBounded(reader, op.BodyLimit)
	if err != nil {
		return nil, err
	}
	value, err := decodeJSON(raw)
	if err != nil {
		return nil, err
	}
	body, ok := value.(map[string]any)
	if !ok || body == nil {
		return nil, problem("invalid_argument", "请求体必须为 JSON 对象")
	}
	return body, nil
}

func requestURL(p profile, op operation, paths, query map[string]string) string {
	path := op.Path
	for key, value := range paths {
		path = strings.ReplaceAll(path, "{"+key+"}", url.PathEscape(value))
	}
	target := strings.TrimRight(p.URL, "/") + path
	q := url.Values{}
	for key, value := range query {
		q.Set(key, value)
	}
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	return target
}

func callHTTP(ctx context.Context, p profile, op operation, target string, body map[string]any) (any, int, error) {
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			return nil, 0, problem("invalid_argument", "无法编码请求体")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(p.TimeoutSeconds)*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, op.Method, target, bytes.NewReader(raw))
	if err != nil {
		return nil, 0, problem("invalid_argument", "无法构造请求")
	}
	req.SetBasicAuth(p.Username, p.Password)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// 一次命令一次请求，不保留连接，因此 Go Transport 不会在复用连接失败时偷偷重试操作。
	transport.DisableKeepAlives = true
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		code, message := "network_error", "请求失败，请核对地址、证书、网络及服务状态"
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			code, message = "timeout", "请求超时"
		} else if errors.Is(ctx.Err(), context.Canceled) {
			code, message = "canceled", "请求已取消"
		}
		return nil, 0, operationFailure(op, code, message, 0, op.RequiresWrite)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		code, message := statusFailure(response.StatusCode)
		return nil, response.StatusCode, operationFailure(op, code, message, response.StatusCode, op.RequiresWrite && response.StatusCode >= 500)
	}
	content, err := readBounded(response.Body, responseLimit)
	if err != nil {
		code, message := "invalid_response", "响应读取失败或超过 16 MiB"
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			code, message = "timeout", "响应读取超时"
		} else if errors.Is(ctx.Err(), context.Canceled) {
			code, message = "canceled", "响应读取被取消"
		}
		return nil, response.StatusCode, operationFailure(op, code, message, response.StatusCode, op.RequiresWrite)
	}
	data, err := decodeJSON(content)
	if err != nil {
		return nil, response.StatusCode, operationFailure(op, "invalid_response", "服务端未返回有效 JSON", response.StatusCode, op.RequiresWrite)
	}
	return data, response.StatusCode, nil
}

func operationFailure(op operation, code, message string, status int, unknown bool) error {
	if unknown {
		message += "；操作结果未确认，可能已部分生效。先只读核查，再向用户确认是否以原参数再次提交"
	}
	return &cliError{Code: code, Message: message, HTTPStatus: status, Operation: op.Name, OutcomeUnknown: unknown}
}

func statusFailure(status int) (string, string) {
	switch status {
	case 400, 422:
		return "invalid_argument", "服务端拒绝参数或查询超过限制，请核对 api describe 与部署契约"
	case 401:
		return "unauthorized", "认证失败，请核对目标 profile 的账号密码"
	case 403:
		return "forbidden", "没有权限，或租户、来源不匹配"
	case 404:
		return "not_found", "接口或对象不存在，请核对版本、身份和租户"
	case 409:
		return "conflict", "版本或状态冲突，请重新读取并确认，不得自动覆盖"
	case 410:
		return "cursor_expired", "查询快照已过期，请从第一页重新查询"
	case 429:
		return "rate_limited", "服务端查询或操作预算已满，本次不自动重试"
	case 503:
		return "unavailable", "接口未配置或服务不可用"
	default:
		if status >= 300 && status < 400 {
			return "redirect_refused", "拒绝重定向，请直接配置最终 Console 基础地址"
		}
		return "http_error", "服务端请求失败；不回显可能含敏感内容的原始错误响应"
	}
}
