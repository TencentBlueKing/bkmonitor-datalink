// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package kaccompat

import (
	"testing"

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
