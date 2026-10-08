// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package datasources

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"linkd/internal/enrich/models"
)

func TestCWStrategyDoesNotRequireLegacyConfigurationTable(t *testing.T) {
	t.Parallel()
	db := openReaderTestDB(t, func(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
		if strings.Contains(query, "core_v1alpha1_strategy") {
			return nil, errors.New("legacy configuration table does not exist")
		}
		return &readerTestRows{columns: []string{"id"}}, nil
	})
	client, err := NewCWStrategyClient(CWStrategyClientConfig{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := client.GetByStrategyID(t.Context(), models.StrategyQuery{TenantID: "tenant", ID: 1, Version: 1}); err != nil {
		t.Fatalf("strategy reader still depends on legacy configuration: %v", err)
	}
}
