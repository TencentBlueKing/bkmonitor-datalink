// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package rawgen

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"linkd/internal/cleaner"
	"linkd/internal/domain"
)

func TestGenerateIsDeterministicAndMatchesScenarioSemantics(t *testing.T) {
	t.Parallel()
	config := Config{
		Seed:          42,
		EventSourceID: "e2e-source",
		TenantPrefix:  "tenant-test",
		TenantCount:   2,
		Counts: map[ScenarioType]int{
			ScenarioRecovered:        1,
			ScenarioSeverityRotation: 1,
			ScenarioCrossTenant:      1,
		},
		DuplicateRecords: 1,
		InvalidRecords:   1,
		MinUpdates:       1,
		MaxUpdates:       1,
		StartTime:        time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC),
	}
	first, err := Generate(config)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	second, err := Generate(config)
	if err != nil {
		t.Fatalf("Generate(replay) error = %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("same seed generated different datasets")
	}
	if first.Expected.InputRecords != 12 || first.Expected.OutputMessages != 10 || len(first.Expected.Alerts) != 5 {
		t.Fatalf("generated counts = %#v", first.Expected)
	}
	wantOperations := map[domain.OperationKind]int{
		domain.OperationKindTrigger:  5,
		domain.OperationKindRecover:  1,
		domain.OperationKindClose:    2,
		domain.OperationKindSuppress: 1,
		domain.OperationKindPush:     10,
	}
	if !reflect.DeepEqual(first.Expected.OperationCounts, wantOperations) {
		t.Fatalf("operation counts = %v, want %v", first.Expected.OperationCounts, wantOperations)
	}

	validRecords := 0
	invalidRecords := 0
	seenEvents := make(map[string]int)
	for _, record := range first.Records {
		if !record.Valid {
			invalidRecords++
			if _, err := (cleaner.StandardCleaner{}).Clean(context.Background(), cleaner.RawEventMessage{Payload: record.Body}); err == nil {
				t.Fatalf("invalid record unexpectedly decoded: %s", record.Body)
			}
			continue
		}
		validRecords++
		raw, err := (cleaner.StandardCleaner{}).Clean(context.Background(), cleaner.RawEventMessage{Payload: record.Body})
		if err != nil {
			t.Fatalf("StandardCleaner.Clean() error = %v, body=%s", err, record.Body)
		}
		if record.BKTenantID == "" || record.KafkaKey != raw.SourceAlertID || record.KafkaTimestamp.IsZero() {
			t.Fatalf("record metadata/raw mismatch: record=%#v raw=%#v", record, raw)
		}
		seenEvents[raw.SourceEventID]++
	}
	if validRecords != 11 || invalidRecords != 1 {
		t.Fatalf("valid/invalid records = %d/%d", validRecords, invalidRecords)
	}
	duplicates := 0
	for _, count := range seenEvents {
		if count > 1 {
			duplicates += count - 1
		}
	}
	if duplicates != 1 {
		t.Fatalf("duplicate record count = %d", duplicates)
	}
}

func TestGenerateDifferentSeedChangesDataset(t *testing.T) {
	t.Parallel()
	counts, err := BalancedCounts(20, []ScenarioType{
		ScenarioActive, ScenarioRecovered, ScenarioClosed, ScenarioSeverityRotation,
	})
	if err != nil {
		t.Fatal(err)
	}
	config := Config{Seed: 1, TenantCount: 4, Counts: counts, MaxUpdates: 2}
	first, err := Generate(config)
	if err != nil {
		t.Fatal(err)
	}
	config.Seed = 2
	second, err := Generate(config)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(first.Records, second.Records) {
		t.Fatal("different seeds generated identical records")
	}
}

func TestParseScenarioSelectionAndValidation(t *testing.T) {
	t.Parallel()
	types, err := ParseScenarioTypes("active,recovered,severity_rotation")
	if err != nil {
		t.Fatal(err)
	}
	counts, err := BalancedCounts(8, types)
	if err != nil {
		t.Fatal(err)
	}
	wantBalanced := map[ScenarioType]int{
		ScenarioActive: 3, ScenarioRecovered: 3, ScenarioSeverityRotation: 2,
	}
	if !reflect.DeepEqual(counts, wantBalanced) {
		t.Fatalf("BalancedCounts() = %v, want %v", counts, wantBalanced)
	}
	mix, err := ParseMix("active=2,closed=3,cross_tenant=1")
	if err != nil {
		t.Fatal(err)
	}
	if mix[ScenarioActive] != 2 || mix[ScenarioClosed] != 3 || mix[ScenarioCrossTenant] != 1 {
		t.Fatalf("ParseMix() = %v", mix)
	}
	if _, err := Generate(Config{
		TenantCount: 1,
		Counts:      map[ScenarioType]int{ScenarioCrossTenant: 1},
	}); err == nil {
		t.Fatal("Generate() accepted cross_tenant with one tenant")
	}
	if _, err := ParseMix("active=1x"); err == nil {
		t.Fatal("ParseMix() accepted a count with trailing characters")
	}
	if _, err := Generate(Config{
		Counts:     map[ScenarioType]int{ScenarioRecovered: 1},
		MaxUpdates: maxRecordCount,
	}); err == nil || !strings.Contains(err.Error(), "record count exceeds") {
		t.Fatalf("Generate() oversized update error = %v", err)
	}
}

func TestLoadConfigRejectsUnknownFields(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, []byte(`{"counts":{"active":1},"unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("LoadConfig() error = %v, want unknown field", err)
	}
}
