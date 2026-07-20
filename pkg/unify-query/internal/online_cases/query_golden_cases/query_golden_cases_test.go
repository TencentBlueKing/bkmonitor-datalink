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

	sensitiveKeyAssignmentPattern = regexp.MustCompile(`(?im)(?:^|\\?"|['\s,{])(?:access[_-]?token|app[_-]?secret|authorization|bk[_-]?ticket|bk[_-]?token|cookie|x-bkapi-authorization|x-bkbase-authorization|bkdata[_-]?authentication[_-]?method|bkdata[_-]?data[_-]?token)(?:\\?"|['\s])*\s*[:=]`)
	sensitiveAuthorizationPattern = regexp.MustCompile(`(?i)\b(?:bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}`)
	sensitiveIPv4Pattern          = regexp.MustCompile(`\b(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\.(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\.(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\.(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\b`)
)

type queryGoldenCase struct {
	ID           string   `yaml:"id"`
	Storage      string   `yaml:"storage"`
	Enabled      *bool    `yaml:"enabled"`
	GoldenStatus string   `yaml:"golden_status"`
	Tags         []string `yaml:"tags"`
	Notes        []string `yaml:"notes"`
	Source       struct {
		Environment string `yaml:"environment"`
		BizID       string `yaml:"biz_id"`
		SampledFrom string `yaml:"sampled_from"`
		SampledAt   string `yaml:"sampled_at"`
		SourceCase  string `yaml:"source_case"`
	} `yaml:"source"`
	Files struct {
		Request            string `yaml:"request"`
		ExpectDownstream   string `yaml:"expect_downstream"`
		ExpectDownstreamTy string `yaml:"expect_downstream_type"`
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
	ipWhitelist := []*regexp.Regexp{
		regexp.MustCompile(`^127\.`),
		regexp.MustCompile(`^192\.168\.`),
	}

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
			name:        "non whitelisted ip",
			path:        "expect.downstream.json",
			content:     `{"server":"11.166.1.2"}`,
			wantFinding: `non-whitelisted ip "11.166.1.2"`,
		},
		{
			name:    "whitelisted ip",
			path:    "expect.downstream.json",
			content: `{"server":"127.0.0.1"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings := findSensitiveQueryGoldenContent(tt.path, tt.content, ipWhitelist)
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
	for _, caseDir := range caseDirs {
		t.Run(filepath.Base(caseDir), func(t *testing.T) {
			tc := loadQueryGoldenCase(t, caseDir)
			assertQueryGoldenCaseMetadata(t, seen, caseDir, tc)
			expect := assertJSONFile(t, filepath.Join(caseDir, tc.Files.ExpectDownstream))
			assertStorageExpect(t, tc, expect)
			if tc.GoldenStatus == "golden_ready" {
				assertJSONFile(t, filepath.Join(caseDir, tc.Files.Request))
			}
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

func assertQueryGoldenCaseMetadata(t *testing.T, seen map[string]string, caseDir string, tc queryGoldenCase) {
	t.Helper()

	if tc.ID == "" {
		t.Fatalf("%s: id is required", caseDir)
	}
	if previous, ok := seen[tc.ID]; ok {
		t.Fatalf("duplicate case id %s: %s and %s", tc.ID, previous, caseDir)
	}
	seen[tc.ID] = caseDir

	switch tc.Storage {
	case "es", "vm", "doris":
	default:
		t.Fatalf("%s: unexpected storage %q", tc.ID, tc.Storage)
	}
	switch tc.GoldenStatus {
	case "captured_downstream", "golden_ready":
	default:
		t.Fatalf("%s: golden_status = %q, want captured_downstream or golden_ready", tc.ID, tc.GoldenStatus)
	}
	if len(tc.Tags) == 0 {
		t.Fatalf("%s: tags are required", tc.ID)
	}
	if tc.GoldenStatus == "captured_downstream" && len(tc.Notes) == 0 {
		t.Fatalf("%s: captured_downstream case should explain what is still missing", tc.ID)
	}
	if tc.Source.Environment == "" || tc.Source.BizID == "" ||
		tc.Source.SampledFrom == "" || tc.Source.SampledAt == "" {
		t.Fatalf("%s: source is incomplete: %+v", tc.ID, tc.Source)
	}
	if tc.Files.ExpectDownstream == "" || tc.Files.ExpectDownstreamTy == "" {
		t.Fatalf("%s: files.expect_downstream and files.expect_downstream_type are required", tc.ID)
	}
	if tc.GoldenStatus == "golden_ready" && tc.Files.Request == "" {
		t.Fatalf("%s: golden_ready case should provide files.request", tc.ID)
	}
}

func assertStorageExpect(t *testing.T, tc queryGoldenCase, expect any) {
	t.Helper()

	switch tc.Storage {
	case "vm":
		assertVMDownstreamExpect(t, tc, expect)
	}
}

func assertVMDownstreamExpect(t *testing.T, tc queryGoldenCase, expect any) {
	t.Helper()

	root, ok := expect.(map[string]any)
	if !ok {
		t.Fatalf("%s: expect.downstream should be a JSON object", tc.ID)
	}
	body := assertMap(t, tc.ID, "body", root["body"])
	if got := body["prefer_storage"]; got != "vm" {
		t.Fatalf("%s: body.prefer_storage = %v, want vm", tc.ID, got)
	}
	sql := assertMap(t, tc.ID, "body.sql", body["sql"])
	if got := sql["api_type"]; got != "query_range" && got != "query" {
		t.Fatalf("%s: body.sql.api_type = %v, want query_range or query", tc.ID, got)
	}
	apiParams := assertMap(t, tc.ID, "body.sql.api_params", sql["api_params"])
	if query, ok := apiParams["query"].(string); !ok || query == "" {
		t.Fatalf("%s: body.sql.api_params.query is required", tc.ID)
	}
	resultTables, ok := sql["result_table_list"].([]any)
	if !ok || len(resultTables) == 0 {
		t.Fatalf("%s: body.sql.result_table_list is required", tc.ID)
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

	ipWhitelist, err := loadSensitiveIPWhitelist()
	if err != nil {
		t.Fatal(err)
	}

	var findings []string
	err = filepath.WalkDir(caseDir, func(path string, d os.DirEntry, err error) error {
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

		findings = append(findings, findSensitiveQueryGoldenContent(path, string(content), ipWhitelist)...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) > 0 {
		t.Fatalf("query golden case contains sensitive content:\n%s", strings.Join(findings, "\n"))
	}
}

func findSensitiveQueryGoldenContent(path, content string, ipWhitelist []*regexp.Regexp) []string {
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
		if !isWhitelistedSensitiveIP(ip, ipWhitelist) {
			findings = append(findings, fmt.Sprintf("%s: contains non-whitelisted ip %q", path, ip))
		}
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
	default:
		return nil
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

func loadSensitiveIPWhitelist() ([]*regexp.Regexp, error) {
	path, err := findRepoFile("scripts/pre_commit/sensitive_info_check/ip_white_list.dat")
	if err != nil {
		return nil, err
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var whitelist []*regexp.Regexp
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		pattern, err := regexp.Compile(line)
		if err != nil {
			return nil, fmt.Errorf("%s contains invalid ip whitelist pattern %q: %w", path, line, err)
		}
		whitelist = append(whitelist, pattern)
	}
	return whitelist, nil
}

func findRepoFile(rel string) (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		candidate := filepath.Join(dir, rel)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("cannot find %s from %s", rel, dir)
		}
		dir = parent
	}
}

func isWhitelistedSensitiveIP(ip string, whitelist []*regexp.Regexp) bool {
	for _, pattern := range whitelist {
		if pattern.MatchString(ip) {
			return true
		}
	}
	return false
}
