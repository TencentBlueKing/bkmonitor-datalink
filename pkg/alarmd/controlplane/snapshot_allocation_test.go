package controlplane

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

func TestWarmSnapshotDecodeReservationsReleaseOnDenialAndSuccess(t *testing.T) {
	for _, denyAt := range []int{1, 2} {
		t.Run(fmt.Sprint(denyAt), func(t *testing.T) {
			ctx := context.Background()
			revision, raw := neutralSnapshotPayload(t, "qg-a")
			var used uint64
			calls, rejection := 0, 0
			denied := errors.New("preparation exhausted")
			admit := func(_ context.Context, n uint64) (func(), error) {
				calls++
				if calls == rejection {
					return nil, denied
				}
				used += n
				return func() { used -= n }, nil
			}
			repo := &RedisCatalogRepository{snapshotCache: newVerifiedSnapshotCache(1, 64<<20)}
			repo.ConfigureSnapshotMemory(admit, worker.PreparationObjectBytes)
			defer repo.ReleaseSnapshotCache()
			first, err := repo.snapshotCache.loadSnapshot(ctx, revision, string(raw), 1)
			if err != nil {
				t.Fatal(err)
			}
			steady := used
			calls, rejection = 0, denyAt
			if _, err := repo.snapshotCache.loadSnapshot(ctx, revision, string(raw), 1); !errors.Is(err, denied) {
				t.Fatalf("denial at reservation %d: %v", denyAt, err)
			}
			if used != steady {
				t.Fatalf("failed decode retained %d bytes, want %d", used, steady)
			}
			calls, rejection = 0, 0
			next, err := repo.snapshotCache.loadSnapshot(ctx, revision, string(raw), 1)
			if err != nil {
				t.Fatal(err)
			}
			if calls != 2 || used != steady || !reflect.DeepEqual(first, next) {
				t.Fatalf("warm decode changed result/account: calls=%d used=%d want=%d", calls, used, steady)
			}
			repo.ReleaseSnapshotCache()
			if used != 0 {
				t.Fatalf("cache close leaked %d bytes", used)
			}
		})
	}
}

func TestSnapshotCacheAndScopeShareOneAllocationUntilLastReference(t *testing.T) {
	ctx, closeScope := WithSnapshotReadScope(context.Background())
	defer closeScope()
	revision, raw := neutralSnapshotPayload(t, "qg-a")
	var used uint64
	admit := func(_ context.Context, n uint64) (func(), error) {
		used += n
		return func() { used -= n }, nil
	}
	repo := &RedisCatalogRepository{snapshotCache: newVerifiedSnapshotCache(1, 64<<20)}
	repo.ConfigureSnapshotMemory(admit, worker.PreparationObjectBytes)
	read, err := reserveSnapshotAllocation(ctx, admit, uint64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	entry, _, err := repo.snapshotCache.load(ctx, revision, string(raw), 1, read)
	if err != nil {
		t.Fatal(err)
	}
	repo.retainScopedSnapshot(ctx, revision, "qg-a", entry.payload, 1, entry.allocation)
	entry.allocation.release()
	read.release()
	steady := used
	if steady == 0 {
		t.Fatal("live Snapshot memory was released")
	}
	// Equal fresh wire data has its own temporary owner, but the cache hit
	// must not charge its identical immutable retained entry again.
	secondRead, err := reserveSnapshotAllocation(ctx, admit, uint64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := repo.snapshotCache.load(ctx, revision, string(raw), 1, secondRead)
	if err != nil {
		t.Fatal(err)
	}
	second.allocation.release()
	secondRead.release()
	if used != steady {
		t.Fatalf("cache hit changed retained account: %d want %d", used, steady)
	}
	repo.ReleaseSnapshotCache()
	if used != steady {
		t.Fatalf("eviction prematurely released live scope: %d want %d", used, steady)
	}
	ClearSnapshotReadScope(ctx)
	if used != 0 {
		t.Fatalf("last scope left %d bytes reserved", used)
	}
	closeScope()
	if used != 0 {
		t.Fatal("repeated close changed account")
	}
}
