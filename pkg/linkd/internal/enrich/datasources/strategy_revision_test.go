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
	"crypto/sha256"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"linkd/internal/enrich/description"
	"linkd/internal/enrich/models"
)

func revisionTestRows(raw []byte) *readerTestRows {
	digest := sha256.Sum256(raw)
	return &readerTestRows{columns: []string{"bk_tenant_id", "bk_biz_id", "strategy_id", "strategy_revision", "snapshot_schema_version", "snapshot_digest", "snapshot_bytes", "snapshot_json"}, values: [][]driver.Value{{"tenant", int64(2), int64(1), revisionTestVersion, int64(1), hex.EncodeToString(digest[:]), int64(len(raw)), raw}}}
}

func TestStrategyRevisionReadUsesImmutableIdentityAndIntegrity(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"strategy":{"id":1,"bk_biz_id":2,"items":[]}}`)
	for _, tc := range []struct {
		name, code string
		change     func(*readerTestRows)
	}{
		{name: "found"},
		{name: "missing", change: func(r *readerTestRows) { r.values = nil }},
		{name: "ambiguous", code: "strategy_revision_ambiguous", change: func(r *readerTestRows) { r.values = append(r.values, r.values[0]) }},
		{name: "tenant", code: "strategy_revision_identity_mismatch", change: func(r *readerTestRows) { r.values[0][0] = "other" }},
		{name: "strategy", code: "strategy_revision_identity_mismatch", change: func(r *readerTestRows) { r.values[0][2] = int64(9) }},
		{name: "version", code: "strategy_revision_identity_mismatch", change: func(r *readerTestRows) { r.values[0][3] = revisionTestVersion + 1 }},
		{name: "schema", code: "strategy_revision_schema_unsupported", change: func(r *readerTestRows) { r.values[0][4] = int64(2) }},
		{name: "digest", code: "strategy_revision_corrupt", change: func(r *readerTestRows) { r.values[0][5] = strings.Repeat("0", 64) }},
		{name: "bytes", code: "strategy_revision_corrupt", change: func(r *readerTestRows) { r.values[0][6] = int64(len(raw) + 1) }},
		{name: "zero business", code: "strategy_revision_identity_mismatch", change: func(r *readerTestRows) { r.values[0][1] = int64(0) }},
		{name: "oversize excluded by SQL", code: "strategy_revision_corrupt", change: func(r *readerTestRows) { r.values[0][7] = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			db := openReaderTestDB(t, func(ctx context.Context, statement string, args []driver.NamedValue) (driver.Rows, error) {
				calls++
				if !strings.Contains(statement, "FROM `alarm_strategy_revision`") || !strings.Contains(statement, "bk_tenant_id = ? AND strategy_id = ? AND strategy_revision = ?") || !strings.Contains(statement, "OCTET_LENGTH(snapshot_json)") || strings.Contains(statement, "publish_status") || len(args) != 5 || args[1].Value != "tenant" || args[2].Value != int64(1) || args[3].Value != revisionTestVersion || args[4].Value != int64(2) {
					t.Fatalf("unexpected history SQL: %s", statement)
				}
				rows := revisionTestRows(raw)
				if tc.change != nil {
					tc.change(rows)
				}
				return rows, nil
			})
			result, found, err := readStrategyRevision(t.Context(), db, models.StrategyQuery{TenantID: "tenant", ID: 1, Version: revisionTestVersion})
			if calls != 1 {
				t.Fatalf("history SQL calls=%d", calls)
			}
			if tc.code != "" {
				var failure *description.Error
				if !errors.As(err, &failure) || failure.Code != tc.code {
					t.Fatalf("error=%v want=%s", err, tc.code)
				}
			} else if err != nil || found != (tc.name == "found") {
				t.Fatalf("found=%v error=%v", found, err)
			} else if found && string(result.Snapshot) != string(raw) {
				t.Fatal("history snapshot changed")
			}
		})
	}
}

// 快照形状无效时即使摘要正确也不能作为策略；错误不会触发 current/latest 查询。
func TestStrategyRevisionRejectsInvalidJSONWithMatchingDigest(t *testing.T) {
	t.Parallel()
	for _, raw := range [][]byte{[]byte("{"), {0xff, 0xfe}} {
		db := openReaderTestDB(t, func(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
			return revisionTestRows(raw), nil
		})
		_, found, err := readStrategyRevision(t.Context(), db, models.StrategyQuery{TenantID: "tenant", ID: 1, Version: revisionTestVersion})
		var failure *description.Error
		if found || !errors.As(err, &failure) || failure.Code != "strategy_revision_corrupt" {
			t.Fatalf("found=%v error=%v", found, err)
		}
	}
}
