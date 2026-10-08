package fleet

import (
	"context"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/redisfailure"
)

// The diagnostic store names its Redis calls: every write of a window's
// records as diagnostic_write and every read of them back as
// diagnostic_read, a series sample's included, so the diagnostics client's
// failures say which it lost.
// Nothing answers on the address: the calls fail, and a failed call is the
// one whose name matters.
func TestTheDiagnosticStoreNamesItsRedisCalls(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 100 * time.Millisecond, MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	hook := &redistest.CallerHook{}
	client.AddHook(hook)
	store, err := NewDiagnosticStore(client, "alarmd:test")
	if err != nil {
		t.Fatal(err)
	}
	store.writeOnce(context.Background(), diagnosticWrite{queryGroup: "qg-a", record: []byte(`{"stage":"runner_decision"}`)})
	written := hook.Callers()
	_, _ = store.Load(context.Background(), "qg-a", 0)
	store.samples, store.samplePerObject = &observability.SeriesSampler{}, 4
	_, _ = store.LoadSeriesSamples(context.Background(), "qg-a", 0)
	read := hook.Callers()[len(written):]
	if len(written) == 0 || len(read) < 2 {
		t.Fatalf("calls = written %v read %v, want both to reach Redis", written, read)
	}
	for _, caller := range written {
		if caller != redisfailure.CallerDiagnosticWrite {
			t.Fatalf("a write named itself %q, want %s: %v", caller, redisfailure.CallerDiagnosticWrite, written)
		}
	}
	for _, caller := range read {
		if caller != redisfailure.CallerDiagnosticRead {
			t.Fatalf("a read named itself %q, want %s: %v", caller, redisfailure.CallerDiagnosticRead, read)
		}
	}
}
