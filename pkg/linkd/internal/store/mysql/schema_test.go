// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package mysqlstore

import (
	"strings"
	"testing"
)

func TestSchemaSeparatesProcessingAndDropsRevision(t *testing.T) {
	t.Parallel()
	data, err := migrationFiles.ReadFile("migrations/001_init.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	schema := string(data)
	for _, forbidden := range []string{
		"location VARCHAR",
		"content_digest",
		"archive_state",
		"archive_pending_since_ns",
		"archived_time_ns",
		"revision BIGINT",
		"terminal_event_id",
	} {
		if strings.Contains(schema, forbidden) {
			t.Errorf("alert schema unexpectedly contains %q", forbidden)
		}
	}
	for _, required := range []string{
		"CREATE TABLE IF NOT EXISTS linkd_events",
		"processing_state VARCHAR(32)",
		"processing JSON NOT NULL",
		"CREATE TABLE IF NOT EXISTS linkd_alerts",
		"event_source_id VARBINARY(32) NOT NULL",
		"PRIMARY KEY (bk_tenant_id, alert_id)",
		"latest_event_id VARBINARY(160) NOT NULL",
		"related_alert_id VARBINARY(160) NULL",
		"idx_linkd_events_alert",
		"severity VARCHAR(32)",
		"end_type VARCHAR(32)",
		"active_marker TINYINT UNSIGNED NULL",
		"idx_linkd_alert_ended_event",
	} {
		if !strings.Contains(schema, required) {
			t.Errorf("alert schema is missing %q", required)
		}
	}
}
