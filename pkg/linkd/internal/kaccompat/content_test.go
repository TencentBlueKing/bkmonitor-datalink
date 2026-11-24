// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package kaccompat

import (
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/enrich/kingeye"

	"linkd/internal/store/storetest"
)

func TestProjectionUpdatesCurrentContentIncludingEmptyText(t *testing.T) {
	client := &Client{level: func(level string) (string, error) { return level, nil }}
	for _, content := range []string{"new detection content", ""} {
		t.Run(content, func(t *testing.T) {
			alert := storetest.Alert("tenant", "alert", "event", "fp", "warning")
			alert.Content = content
			fields, _, err := client.fields(t.Context(), alert, false)
			if err != nil {
				t.Fatal(err)
			}
			if value, exists := fields["content"]; !exists || value != content {
				t.Fatalf("projection omitted current content: %v", fields)
			}
			for _, key := range []string{"conductor", "notify_status", "field_extra_info", "storage_time"} {
				if _, exists := fields[key]; exists {
					t.Fatalf("state update resets KAC-owned %s", key)
				}
			}
		})
	}
}

// 时间只投影 LastOccurredAt，不能误用更新、关闭或入库时间，也不在投影中取最大值。
func TestLatestEventTimeUsesAcceptedEventOccurrence(t *testing.T) {
	client := &Client{level: func(level string) (string, error) { return level, nil }}
	for _, status := range []domain.AlertStatus{domain.AlertStatusActive, domain.AlertStatusRecovered, domain.AlertStatusClosed} {
		t.Run(string(status), func(t *testing.T) {
			alert := storetest.Alert("tenant", "alert", "event", "fp", "warning")
			alert.Status = status
			alert.LastOccurredAt = time.Date(2026, 11, 5, 16, 0, 1, 0, time.UTC)
			alert.UpdateAt = alert.LastOccurredAt.Add(time.Hour)
			if status.Terminal() {
				alert.EndAt = &alert.UpdateAt
			}
			fields, _, err := client.fields(t.Context(), alert, false)
			if err != nil {
				t.Fatal(err)
			}
			if fields["latest_event_time"] != "2026-11-06 00:00:01" {
				t.Fatalf("latest_event_time = %v", fields["latest_event_time"])
			}
			alert.LastOccurredAt = alert.LastOccurredAt.Add(-time.Minute)
			fields, _, err = client.fields(t.Context(), alert, false)
			if err != nil {
				t.Fatal(err)
			}
			if fields["latest_event_time"] != "2026-11-05 23:59:01" {
				t.Fatalf("late event = %v", fields["latest_event_time"])
			}
		})
	}
	for _, name := range []string{"latest_event_time", "strategy_config_uid", "strategy_config_version"} {
		if err := kingeye.ValidateFieldMappings(map[string]string{name: "$.extra_data.custom"}); err == nil {
			t.Errorf("custom mapping accepted reserved %s", name)
		}
	}
}
