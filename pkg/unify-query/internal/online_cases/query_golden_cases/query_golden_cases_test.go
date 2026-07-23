// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package query_golden_cases

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

var (
	sensitiveQueryGoldenKeyNames = map[string]struct{}{
		"access_token":                 {},
		"app_secret":                   {},
		"authorization":                {},
		"bk_ticket":                    {},
		"bk_token":                     {},
		"cookie":                       {},
		"x_bkapi_authorization":        {},
		"x_bkbase_authorization":       {},
		"bkdata_authentication_method": {},
		"bkdata_data_token":            {},
	}
	sensitiveQueryGoldenSourceKeyNames = map[string]struct{}{
		"biz_id":       {},
		"environment":  {},
		"sampled_from": {},
		"source_case":  {},
	}

	sensitiveKeyAssignmentPattern = regexp.MustCompile(`(?im)(?:^|\\?"|['\s,{])(?:access[_-]?token|app[_-]?secret|authorization|bk[_-]?ticket|bk[_-]?token|cookie|x-bkapi-authorization|x-bkbase-authorization|bkdata[_-]?authentication[_-]?method|bkdata[_-]?data[_-]?token)(?:\\?"|['\s])*\s*[:=]`)
	sensitiveAuthorizationPattern = regexp.MustCompile(`(?i)\b(?:bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}`)
	sensitiveIPv4Pattern          = regexp.MustCompile(`\b(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\.(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\.(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\.(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\b`)
	sensitiveDomainPattern        = regexp.MustCompile(`(?i)\b(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+(?:com|cn|net|org|io|internal|local|invalid)\b`)
	queryGoldenFingerprintPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

type queryGoldenCase struct {
	ID             string   `yaml:"id"`
	Storage        string   `yaml:"storage"`
	Enabled        *bool    `yaml:"enabled"`
	ShapeSignature string   `yaml:"shape_signature"`
	Tags           []string `yaml:"tags"`
	Notes          []string `yaml:"notes"`
	Source         struct {
		Kind        string `yaml:"kind"`
		OutputsKind string `yaml:"outputs_kind"`
		SampledAt   string `yaml:"sampled_at"`
		Fingerprint string `yaml:"fingerprint"`
	} `yaml:"source"`
	Files struct {
		Request      string `yaml:"request"`
		Route        string `yaml:"route"`
		Dependencies string `yaml:"dependencies"`
		ExpectOutput string `yaml:"expect_outputs"`
	} `yaml:"files"`
}

func TestQueryGoldenCasesDataset(t *testing.T) {
	assertQueryGoldenCasesDataset(t, "testdata/cases", true)
}

func TestLocalQueryGoldenCasesDataset(t *testing.T) {
	dir := os.Getenv("UQ_QUERY_GOLDEN_LOCAL_CASE_DIR")
	if dir == "" {
		t.Skip("set UQ_QUERY_GOLDEN_LOCAL_CASE_DIR to validate local query golden cases")
	}
	assertQueryGoldenCasesDataset(t, dir, false)
}

func TestFindSensitiveQueryGoldenContent(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		content     string
		wantFinding string
	}{
		{
			name:    "clean golden content",
			path:    "request.json",
			content: `{"headers":{"X-Bk-Scope-Space-Uid":"bksaas__demo"},"body":{"promql":"sum(a)"}}`,
		},
		{
			name:        "structured sensitive header",
			path:        "request.json",
			content:     `{"headers":{"Authorization":"Bearer abcdefghijklmnop"}}`,
			wantFinding: `headers.Authorization`,
		},
		{
			name:        "sensitive key hidden in log string",
			path:        "expect.downstream.json",
			content:     `{"message":"body: {\"bkdata_data_token\":\"abcdefghijklmnop\"}"}`,
			wantFinding: `sensitive assignment`,
		},
		{
			name:        "non local ip",
			path:        "expect.downstream.json",
			content:     `{"server":"192.0.2.1"}`,
			wantFinding: `non-local ip "192.0.2.1"`,
		},
		{
			name:    "loopback ip",
			path:    "expect.downstream.json",
			content: `{"server":"127.0.0.1"}`,
		},
		{
			name:        "private ip",
			path:        "expect.downstream.json",
			content:     `{"server":"192.168.1.10"}`,
			wantFinding: `non-local ip "192.168.1.10"`,
		},
		{
			name:        "raw source environment metadata",
			path:        "case.yaml",
			content:     "source:\n  environment: production-a\n  biz_id: '7'\n",
			wantFinding: `source.environment`,
		},
		{
			name:        "non local url",
			path:        "route.json",
			content:     `{"address":"https://query.internal.invalid/api"}`,
			wantFinding: `non-local URL host`,
		},
		{
			name:        "bare domain",
			path:        "route.json",
			content:     `{"address":"query.internal.invalid"}`,
			wantFinding: `domain "query.internal.invalid"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings := findSensitiveQueryGoldenContent(tt.path, tt.content)
			if tt.wantFinding == "" {
				if len(findings) > 0 {
					t.Fatalf("unexpected sensitive findings: %s", strings.Join(findings, "\n"))
				}
				return
			}
			for _, finding := range findings {
				if strings.Contains(finding, tt.wantFinding) {
					return
				}
			}
			t.Fatalf("findings %q do not contain %q", findings, tt.wantFinding)
		})
	}
}

func assertQueryGoldenCasesDataset(t *testing.T, root string, requireCases bool) {
	t.Helper()

	caseDirs := findQueryGoldenCaseDirs(t, root)
	if requireCases && len(caseDirs) == 0 {
		t.Fatalf("no query golden cases found under %s", root)
	}
	if len(caseDirs) == 0 {
		t.Skipf("no query golden cases found under %s", root)
	}

	seen := make(map[string]string, len(caseDirs))
	seenShapes := make(map[string]string, len(caseDirs))
	for _, caseDir := range caseDirs {
		t.Run(filepath.Base(caseDir), func(t *testing.T) {
			tc := loadQueryGoldenCase(t, caseDir)
			assertQueryGoldenCaseMetadata(t, seen, seenShapes, caseDir, tc)
			assertJSONFile(t, filepath.Join(caseDir, tc.Files.Request))
			assertJSONFile(t, filepath.Join(caseDir, tc.Files.Route))
			assertJSONFile(t, filepath.Join(caseDir, tc.Files.Dependencies))
			expect := assertJSONFile(t, filepath.Join(caseDir, tc.Files.ExpectOutput))
			assertStorageExpect(t, tc, expect)
			assertNoSensitiveQueryGoldenContent(t, caseDir)
		})
	}
}

func findQueryGoldenCaseDirs(t *testing.T, root string) []string {
	t.Helper()

	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}

	var dirs []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Base(path) == "case.yaml" {
			dirs = append(dirs, filepath.Dir(path))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return dirs
}

func loadQueryGoldenCase(t *testing.T, caseDir string) queryGoldenCase {
	t.Helper()

	content, err := os.ReadFile(filepath.Join(caseDir, "case.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var tc queryGoldenCase
	if err := yaml.Unmarshal(content, &tc); err != nil {
		t.Fatal(err)
	}
	return tc
}

func assertQueryGoldenCaseMetadata(t *testing.T, seen, seenShapes map[string]string, caseDir string, tc queryGoldenCase) {
	t.Helper()

	if tc.ID == "" {
		t.Fatalf("%s: id is required", caseDir)
	}
	if previous, ok := seen[tc.ID]; ok {
		t.Fatalf("duplicate case id %s: %s and %s", tc.ID, previous, caseDir)
	}
	seen[tc.ID] = caseDir
	if tc.ShapeSignature == "" {
		t.Fatalf("%s: shape_signature is required", tc.ID)
	}
	if previous, ok := seenShapes[tc.ShapeSignature]; ok {
		t.Fatalf("duplicate shape_signature %s: %s and %s", tc.ShapeSignature, previous, caseDir)
	}
	seenShapes[tc.ShapeSignature] = caseDir

	switch tc.Storage {
	case "es", "vm", "doris", "tspider", "hdfs", "influxdb":
	default:
		t.Fatalf("%s: unexpected storage %q", tc.ID, tc.Storage)
	}
	if len(tc.Tags) == 0 {
		t.Fatalf("%s: tags are required", tc.ID)
	}
	if tc.Source.Kind == "" || tc.Source.SampledAt == "" || tc.Source.Fingerprint == "" {
		t.Fatalf("%s: source is incomplete: %+v", tc.ID, tc.Source)
	}
	switch tc.Source.Kind {
	case "production_log":
	case "merged_pr":
		if tc.Source.OutputsKind != "post_fix_handler_replay" ||
			!containsQueryGoldenString(tc.Tags, "regression_fix") {
			t.Fatalf("%s: merged_pr source requires post_fix_handler_replay and regression_fix", tc.ID)
		}
	default:
		t.Fatalf("%s: unsupported source.kind %q", tc.ID, tc.Source.Kind)
	}
	switch tc.Source.OutputsKind {
	case "production_log":
	case "handler_replay":
		if !containsQueryGoldenString(tc.Tags, "provisional_output") {
			t.Fatalf("%s: handler_replay output should be tagged provisional_output", tc.ID)
		}
	case "post_fix_handler_replay":
		if !containsQueryGoldenString(tc.Tags, "post_fix_expected") {
			t.Fatalf("%s: post_fix_handler_replay output should be tagged post_fix_expected", tc.ID)
		}
	default:
		t.Fatalf("%s: unsupported source.outputs_kind %q", tc.ID, tc.Source.OutputsKind)
	}
	if !queryGoldenFingerprintPattern.MatchString(tc.Source.Fingerprint) {
		t.Fatalf("%s: source.fingerprint should be sha256:<64 lowercase hex chars>", tc.ID)
	}
	if tc.Files.Request == "" || tc.Files.Route == "" || tc.Files.Dependencies == "" || tc.Files.ExpectOutput == "" {
		t.Fatalf("%s: request, route, dependencies and expect_outputs files are required", tc.ID)
	}
}

func containsQueryGoldenString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func assertStorageExpect(t *testing.T, tc queryGoldenCase, expect any) {
	t.Helper()
	outputs, ok := expect.([]any)
	if !ok || len(outputs) == 0 {
		t.Fatalf("%s: expect.outputs should be a non-empty JSON array", tc.ID)
	}

	backendCount := make(map[string]int)
	for index, output := range outputs {
		root, ok := output.(map[string]any)
		if !ok {
			t.Fatalf("%s: expect.outputs[%d] should be a JSON object", tc.ID, index)
		}
		backend, ok := root["backend"].(string)
		if !ok || backend == "" {
			t.Fatalf("%s: expect.outputs[%d].backend is required", tc.ID, index)
		}
		switch backend {
		case "bkbase", "elasticsearch", "influxdb":
		default:
			t.Fatalf("%s: expect.outputs[%d].backend = %q is unsupported", tc.ID, index, backend)
		}
		method, ok := root["method"].(string)
		if !ok || (method != "GET" && method != "POST") {
			t.Fatalf("%s: expect.outputs[%d].method = %v, want GET or POST", tc.ID, index, root["method"])
		}
		path, ok := root["path"].(string)
		if !ok || !strings.HasPrefix(path, "/") {
			t.Fatalf("%s: expect.outputs[%d].path should start with /", tc.ID, index)
		}
		backendCount[backend]++
	}

	switch tc.Storage {
	case "vm":
		assertVMDownstreamExpect(t, tc, outputs)
	case "es":
		assertExpectedBackend(t, tc.ID, backendCount, "elasticsearch")
	case "doris", "tspider", "hdfs":
		assertExpectedBackend(t, tc.ID, backendCount, "bkbase")
	case "influxdb":
		assertExpectedBackend(t, tc.ID, backendCount, "influxdb")
	}
}

func assertExpectedBackend(t *testing.T, caseID string, backendCount map[string]int, backend string) {
	t.Helper()
	if backendCount[backend] == 0 {
		t.Fatalf("%s: expect.outputs should contain backend %q", caseID, backend)
	}
}

func assertVMDownstreamExpect(t *testing.T, tc queryGoldenCase, outputs []any) {
	t.Helper()

	for _, output := range outputs {
		root, ok := output.(map[string]any)
		if !ok || root["backend"] != "bkbase" {
			continue
		}
		body, ok := root["body"].(map[string]any)
		if !ok || body["prefer_storage"] != "vm" {
			continue
		}
		assertVMQueryBody(t, tc.ID, body)
		return
	}
	t.Fatalf("%s: expect.outputs should contain a BKBase VM query", tc.ID)
}

func assertVMQueryBody(t *testing.T, caseID string, body map[string]any) {
	t.Helper()

	if got := body["prefer_storage"]; got != "vm" {
		t.Fatalf("%s: body.prefer_storage = %v, want vm", caseID, got)
	}
	sql := assertMap(t, caseID, "body.sql", body["sql"])
	if got := sql["api_type"]; got != "query_range" && got != "query" {
		t.Fatalf("%s: body.sql.api_type = %v, want query_range or query", caseID, got)
	}
	apiParams := assertMap(t, caseID, "body.sql.api_params", sql["api_params"])
	if query, ok := apiParams["query"].(string); !ok || query == "" {
		t.Fatalf("%s: body.sql.api_params.query is required", caseID)
	}
	resultTables, ok := sql["result_table_list"].([]any)
	if !ok || len(resultTables) == 0 {
		t.Fatalf("%s: body.sql.result_table_list is required", caseID)
	}
}

func assertMap(t *testing.T, caseID, field string, value any) map[string]any {
	t.Helper()

	m, ok := value.(map[string]any)
	if !ok || len(m) == 0 {
		t.Fatalf("%s: %s is required", caseID, field)
	}
	return m
}

func assertJSONFile(t *testing.T, path string) any {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value any
	if err := json.Unmarshal(content, &value); err != nil {
		t.Fatalf("%s is not valid json: %v", path, err)
	}
	return value
}

func assertNoSensitiveQueryGoldenContent(t *testing.T, caseDir string) {
	t.Helper()

	var findings []string
	err := filepath.WalkDir(caseDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		findings = append(findings, findSensitiveQueryGoldenContent(path, string(content))...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) > 0 {
		t.Fatalf("query golden case contains sensitive content:\n%s", strings.Join(findings, "\n"))
	}
}

func findSensitiveQueryGoldenContent(path, content string) []string {
	var findings []string

	// request/expect 文件通常是 JSON，case.yaml 是 YAML；先按结构化 key 检查，
	// 能避免单纯字符串包含带来的误判，也能明确指出是哪一个字段没有脱敏。
	if value, ok := decodeStructuredQueryGoldenContent(path, content); ok {
		findings = append(findings, findSensitiveStructuredKeys(path, nil, value)...)
	}

	// 线上日志有时会被作为字符串嵌在 JSON/YAML 里，结构化解析看不到内部字段；
	// 这里额外检查“敏感 key 后面跟 : 或 =”的赋值形态，避免 token 字段藏在日志原文里。
	if match := sensitiveKeyAssignmentPattern.FindString(content); match != "" {
		findings = append(findings, fmt.Sprintf("%s: contains sensitive assignment %q", path, strings.TrimSpace(match)))
	}
	if match := sensitiveAuthorizationPattern.FindString(content); match != "" {
		findings = append(findings, fmt.Sprintf("%s: contains authorization value %q", path, match))
	}
	for _, ip := range sensitiveIPv4Pattern.FindAllString(content, -1) {
		if ip != "127.0.0.1" {
			findings = append(findings, fmt.Sprintf("%s: contains non-local ip %q", path, ip))
		}
	}
	for _, domain := range sensitiveDomainPattern.FindAllString(content, -1) {
		findings = append(findings, fmt.Sprintf("%s: contains domain %q", path, domain))
	}
	return findings
}

func decodeStructuredQueryGoldenContent(path, content string) (any, bool) {
	var value any
	switch filepath.Ext(path) {
	case ".json":
		if err := json.Unmarshal([]byte(content), &value); err != nil {
			return nil, false
		}
	case ".yaml", ".yml":
		if err := yaml.Unmarshal([]byte(content), &value); err != nil {
			return nil, false
		}
	default:
		return nil, false
	}
	return value, true
}

func findSensitiveStructuredKeys(path string, parents []string, value any) []string {
	switch typed := value.(type) {
	case map[string]any:
		var findings []string
		for key, child := range typed {
			currentPath := appendFieldPath(parents, key)
			if _, ok := sensitiveQueryGoldenKeyNames[normalizeSensitiveQueryGoldenKey(key)]; ok {
				findings = append(findings, fmt.Sprintf("%s: contains sensitive field %q", path, strings.Join(currentPath, ".")))
			}
			if isSensitiveSourceMetadataKey(parents, key) {
				findings = append(findings, fmt.Sprintf("%s: contains raw source metadata %q", path, strings.Join(currentPath, ".")))
			}
			findings = append(findings, findSensitiveStructuredKeys(path, currentPath, child)...)
		}
		return findings
	case map[any]any:
		var findings []string
		for key, child := range typed {
			keyText := fmt.Sprint(key)
			currentPath := appendFieldPath(parents, keyText)
			if _, ok := sensitiveQueryGoldenKeyNames[normalizeSensitiveQueryGoldenKey(keyText)]; ok {
				findings = append(findings, fmt.Sprintf("%s: contains sensitive field %q", path, strings.Join(currentPath, ".")))
			}
			if isSensitiveSourceMetadataKey(parents, keyText) {
				findings = append(findings, fmt.Sprintf("%s: contains raw source metadata %q", path, strings.Join(currentPath, ".")))
			}
			findings = append(findings, findSensitiveStructuredKeys(path, currentPath, child)...)
		}
		return findings
	case []any:
		var findings []string
		for index, child := range typed {
			currentPath := appendFieldPath(parents, fmt.Sprintf("[%d]", index))
			findings = append(findings, findSensitiveStructuredKeys(path, currentPath, child)...)
		}
		return findings
	case string:
		if parsed, err := url.Parse(typed); err == nil && parsed.IsAbs() && parsed.Hostname() != "" && !isLocalQueryGoldenHost(parsed.Hostname()) {
			return []string{fmt.Sprintf("%s: contains non-local URL host %q at %q", path, parsed.Hostname(), strings.Join(parents, "."))}
		}
		return nil
	default:
		return nil
	}
}

func isSensitiveSourceMetadataKey(parents []string, key string) bool {
	if len(parents) == 0 || normalizeSensitiveQueryGoldenKey(parents[0]) != "source" {
		return false
	}
	_, ok := sensitiveQueryGoldenSourceKeyNames[normalizeSensitiveQueryGoldenKey(key)]
	return ok
}

func isLocalQueryGoldenHost(host string) bool {
	switch strings.ToLower(host) {
	case "127.0.0.1", "::1", "localhost":
		return true
	default:
		return false
	}
}

func appendFieldPath(parents []string, field string) []string {
	next := make([]string, 0, len(parents)+1)
	next = append(next, parents...)
	next = append(next, field)
	return next
}

func normalizeSensitiveQueryGoldenKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.Trim(key, `"'`)
	key = strings.ReplaceAll(key, "-", "_")
	return key
}
