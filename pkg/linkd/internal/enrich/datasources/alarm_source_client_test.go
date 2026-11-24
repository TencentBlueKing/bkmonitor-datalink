package datasources

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"
)

func TestAlarmSourceClientLooksUpKACShortIDWithinTenant(t *testing.T) {
	t.Parallel()
	const tenantID = "tenant-a"
	const shortID = "kac_shortid"
	db := openReaderTestDB(t, func(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
		if !strings.Contains(query, "`bk_tenant_id` = ? AND linkd_source_id = ?") &&
			!strings.Contains(query, "bk_tenant_id = ? AND linkd_source_id = ?") {
			t.Errorf("query does not constrain tenant and short ID: %s", query)
		}
		if len(args) != 3 || args[0].Value != tenantID || args[1].Value != shortID || args[2].Value != int64(1) {
			t.Errorf("unexpected query args: %#v", args)
		}
		return &readerTestRows{
			columns: []string{"id", "name", "linkd_source_id", "linkd_channel"},
			values: [][]driver.Value{{
				"kac-database-id", "告警源", shortID,
				[]byte(`{"type":"kafka","config":{"topic":"raw-alerts"}}`),
			}},
		}, nil
	})
	client, err := NewAlarmSourceClient(AlarmSourceClientConfig{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	source, found, err := client.GetAlarmSource(context.Background(), tenantID, shortID)
	if err != nil || !found {
		t.Fatalf("GetAlarmSource: found=%t err=%v", found, err)
	}
	if source.Id != "kac-database-id" || source.LinkdSourceId != shortID || source.Name != "告警源" ||
		source.LinkdChannel.Type != "kafka" || source.LinkdChannel.Config["topic"] != "raw-alerts" {
		t.Fatalf("unexpected alarm source: %#v", source)
	}
}
