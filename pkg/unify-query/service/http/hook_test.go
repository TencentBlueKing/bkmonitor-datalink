// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

var queryRawESBatchConfigPaths = []string{
	QueryRawESBatchMaxMembersConfigPath,
	QueryRawESBatchMaxBodyBytesConfigPath,
	QueryRawESBatchMaxConcurrentSearchesConfigPath,
}

func preserveQueryRawESBatchTestState(t *testing.T) {
	t.Helper()

	setDefaultConfig()
	for _, configPath := range queryRawESBatchConfigPaths {
		viper.Set(configPath, nil)
	}
	oldSnapshot := queryRawESBatchSettingsSnapshot.Load()
	oldWarn := warnQueryRawESBatchConfig

	t.Cleanup(func() {
		for _, configPath := range queryRawESBatchConfigPaths {
			viper.Set(configPath, nil)
		}
		queryRawESBatchSettingsSnapshot.Store(oldSnapshot)
		warnQueryRawESBatchConfig = oldWarn
	})
}

func TestQueryRawESBatchSettingsDefaults(t *testing.T) {
	preserveQueryRawESBatchTestState(t)

	LoadConfig()

	want := queryRawESBatchSettings{
		maxMembers:            DefaultQueryRawESBatchMaxMembers,
		maxBodyBytes:          DefaultQueryRawESBatchMaxBodyBytes,
		maxConcurrentSearches: DefaultQueryRawESBatchMaxConcurrentSearches,
	}
	if got := getQueryRawESBatchSettings(); !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected default query_raw ES batch settings: got=%+v want=%+v", got, want)
	}
}

func TestLoadConfigQueryRawESBatchNumericValidation(t *testing.T) {
	tests := []struct {
		name                      string
		maxMembers                any
		maxBodyBytes              any
		maxConcurrentSearches     any
		wantMaxMembers            int
		wantMaxBodyBytes          int
		wantMaxConcurrentSearches int
	}{
		{
			name:                      "valid values",
			maxMembers:                8,
			maxBodyBytes:              2048,
			maxConcurrentSearches:     3,
			wantMaxMembers:            8,
			wantMaxBodyBytes:          2048,
			wantMaxConcurrentSearches: 3,
		},
		{
			name:                      "invalid ranges use defaults",
			maxMembers:                1,
			maxBodyBytes:              0,
			maxConcurrentSearches:     -1,
			wantMaxMembers:            DefaultQueryRawESBatchMaxMembers,
			wantMaxBodyBytes:          DefaultQueryRawESBatchMaxBodyBytes,
			wantMaxConcurrentSearches: DefaultQueryRawESBatchMaxConcurrentSearches,
		},
		{
			name:                      "parse failures use defaults",
			maxMembers:                "invalid-members-secret",
			maxBodyBytes:              "invalid-body-secret",
			maxConcurrentSearches:     "invalid-concurrency-secret",
			wantMaxMembers:            DefaultQueryRawESBatchMaxMembers,
			wantMaxBodyBytes:          DefaultQueryRawESBatchMaxBodyBytes,
			wantMaxConcurrentSearches: DefaultQueryRawESBatchMaxConcurrentSearches,
		},
		{
			name:                      "strict integer types reject coercion",
			maxMembers:                true,
			maxBodyBytes:              2048.9,
			maxConcurrentSearches:     "4.0",
			wantMaxMembers:            DefaultQueryRawESBatchMaxMembers,
			wantMaxBodyBytes:          DefaultQueryRawESBatchMaxBodyBytes,
			wantMaxConcurrentSearches: DefaultQueryRawESBatchMaxConcurrentSearches,
		},
		{
			name:                      "canonical integer strings are accepted",
			maxMembers:                "8",
			maxBodyBytes:              "2048",
			maxConcurrentSearches:     "3",
			wantMaxMembers:            8,
			wantMaxBodyBytes:          2048,
			wantMaxConcurrentSearches: 3,
		},
		{
			name:                      "zero concurrency is preserved",
			maxMembers:                2,
			maxBodyBytes:              1,
			maxConcurrentSearches:     0,
			wantMaxMembers:            2,
			wantMaxBodyBytes:          1,
			wantMaxConcurrentSearches: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			preserveQueryRawESBatchTestState(t)
			viper.Set(QueryRawESBatchMaxMembersConfigPath, tt.maxMembers)
			viper.Set(QueryRawESBatchMaxBodyBytesConfigPath, tt.maxBodyBytes)
			viper.Set(QueryRawESBatchMaxConcurrentSearchesConfigPath, tt.maxConcurrentSearches)

			LoadConfig()

			got := getQueryRawESBatchSettings()
			if got.maxMembers != tt.wantMaxMembers {
				t.Fatalf("unexpected max members: got=%d want=%d", got.maxMembers, tt.wantMaxMembers)
			}
			if got.maxBodyBytes != tt.wantMaxBodyBytes {
				t.Fatalf("unexpected max body bytes: got=%d want=%d", got.maxBodyBytes, tt.wantMaxBodyBytes)
			}
			if got.maxConcurrentSearches != tt.wantMaxConcurrentSearches {
				t.Fatalf(
					"unexpected max concurrent searches: got=%d want=%d",
					got.maxConcurrentSearches,
					tt.wantMaxConcurrentSearches,
				)
			}
		})
	}
}

func TestLoadConfigQueryRawESBatchWarningsDoNotContainRawValues(t *testing.T) {
	preserveQueryRawESBatchTestState(t)
	const rawValue = "sensitive-capacity-value"
	var warnings []string
	warnQueryRawESBatchConfig = func(field, reason string) {
		warnings = append(warnings, field+" "+reason)
	}
	viper.Set(QueryRawESBatchMaxMembersConfigPath, rawValue)

	LoadConfig()

	if len(warnings) == 0 {
		t.Fatal("expected invalid configuration warning")
	}
	if strings.Contains(strings.Join(warnings, " "), rawValue) {
		t.Fatal("configuration warning contains the raw invalid value")
	}
}
