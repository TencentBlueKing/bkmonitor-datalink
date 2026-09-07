package controlplane_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func TestSnapshotAdmissionRejectsGrowthBeforeRedisReturnsBody(t *testing.T) {
	ctx := context.Background()
	client := newControlplaneRedis(t)
	const prefix = "snapshot-memory-test"
	const revision execution.SnapshotRevision = "snapshot-revision"
	key := prefix + ":snapshot:" + string(revision)
	if err := client.Set(ctx, key, "{}", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, prefix+":snapshot_epoch:"+string(revision), "1", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.ConfigSet(ctx, "slowlog-log-slower-than", "0").Err(); err != nil {
		t.Fatal(err)
	}
	repo, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var used uint64
	repo.ConfigureSnapshotMemory(func(_ context.Context, size uint64) (func(), error) {
		if size != 2 {
			t.Fatalf("reserved unobserved body size %d", size)
		}
		used += size
		// Actual mutation after STRLEN and before EVAL, on the same Redis.
		if err := client.Set(ctx, key, strings.Repeat("x", 1<<20), 0).Err(); err != nil {
			t.Fatal(err)
		}
		return func() { used -= size }, nil
	}, nil)
	_, err = repo.LoadSnapshot(ctx, revision)
	var dependency *controlplane.ActivationDependencyIOError
	if !errors.As(err, &dependency) || used != 0 {
		t.Fatalf("growth classification/release: err=%v used=%d", err, used)
	}
	entries, err := client.SlowLogGet(ctx, 128).Result()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if len(entry.Args) > 1 && strings.EqualFold(entry.Args[0], "get") && entry.Args[1] == key {
			t.Fatal("bounded reader fetched grown payload before rejecting")
		}
	}
}
