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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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
		lower := strings.ToLower(string(content))
		for _, fragment := range []string{
			"access_token",
			"app_secret",
			"authorization",
			"bk_ticket",
			"bk_token",
			"cookie",
			"x-bkapi-authorization",
			"x-bkbase-authorization",
			"bkdata_authentication_method",
			"bkdata_data_token",
			"11.166.",
			"11.157.",
			"9.166.",
			"21.230.",
			"21.212.",
		} {
			if strings.Contains(lower, fragment) {
				t.Fatalf("%s contains sensitive fragment %q", path, fragment)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
