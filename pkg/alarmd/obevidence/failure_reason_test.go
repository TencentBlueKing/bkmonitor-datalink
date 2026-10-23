package obevidence

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/redisfailure"
)

// idleCuttingRedis is a real server that closes a client connection idle
// for a second, as a network or a proxy does to a pool's idle connection.
func idleCuttingRedis(t *testing.T) string {
	t.Helper()
	return redistest.Start(t, "--timeout", "1").Addr
}

// A read on a pooled connection the server closed while it sat idle is
// unanswered, and says why -- in the result and to the binding's counter --
// rather than only that the store was unavailable.
func TestAReadOnAConnectionCutWhileIdleSaysWhy(t *testing.T) {
	address := idleCuttingRedis(t)
	client := redis.NewClient(&redis.Options{Addr: address, MaxRetries: -1, PoolSize: 1, DialTimeout: time.Second, ReadTimeout: time.Second})
	t.Cleanup(func() { _ = client.Close() })
	var heard []string
	read := RedisBinding{Client: client, Location: Location{Role: "runtime"}, OnFailure: func(reason string) { heard = append(heard, reason) }}
	ctx := context.Background()
	if got, _ := readOne(ctx, "fixture", read, "absent"); got.Status != "missing" || got.Reason != "" {
		t.Fatalf("first read %+v", got)
	}
	time.Sleep(2500 * time.Millisecond)
	got, _ := readOne(ctx, "fixture", read, "absent")
	if got.Status != "dependency_unavailable" || got.Reason != redisfailure.ConnectionClosed || got.ReasonText == "" {
		t.Fatalf("read after the idle cut: status %s reason %q text %q", got.Status, got.Reason, got.ReasonText)
	}
	if len(heard) != 1 || heard[0] != redisfailure.ConnectionClosed {
		t.Fatalf("the binding heard %v, want one connection_closed", heard)
	}
}

// A server that did not answer INFO says why beside its status.
func TestAnUnansweredInfoSaysWhy(t *testing.T) {
	dead := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1, DialTimeout: 200 * time.Millisecond})
	t.Cleanup(func() { _ = dead.Close() })
	var heard []string
	gone := RedisBinding{Client: dead, Location: Location{Role: "runtime", Address: "gone", Mode: "standalone"}, OnFailure: func(reason string) { heard = append(heard, reason) }}
	got := New(Options{Published: gone}).Info(context.Background())
	if len(got.Servers) != 1 || got.Servers[0].Status != "dependency_unavailable" || got.Servers[0].Reason != redisfailure.ConnectionRefused ||
		!strings.Contains(got.Servers[0].ReasonText, "127.0.0.1:1") ||
		len(heard) != 1 || heard[0] != redisfailure.ConnectionRefused {
		t.Fatalf("servers %+v heard %v", got.Servers, heard)
	}
}
