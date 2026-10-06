package legacyoutput

import (
	"context"
	"errors"
	"time"

	"github.com/go-redis/redis/v8"
)

// RedisSnapshotStore writes every strategy snapshot to one Redis: the routing
// table's default node.
//
// Python shards this cache by strategy ID, so a writer normally has to land on
// the node the reader will consult. The reader here is the alert builder, and
// it consults the default node unconditionally: it passes the snapshot key it
// read out of the Kafka event, a plain string with no strategy ID attached, and
// the router answers that with the default node. Reproducing the shard map
// would therefore put snapshots on nodes nothing reads.
type RedisSnapshotStore struct {
	Client redis.Cmdable
}

func (s RedisSnapshotStore) SaveBatch(ctx context.Context, snapshots []Snapshot) error {
	if s.Client == nil {
		return errors.New("legacy snapshot store has no Redis client")
	}
	for _, snapshot := range snapshots {
		if snapshot.StrategyID <= 0 {
			return errors.New("legacy snapshot has no strategy identity")
		}
	}
	if len(snapshots) == 0 {
		return nil
	}
	_, err := s.Client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		for _, snapshot := range snapshots {
			// One hour matches Python's own STRATEGY_SNAPSHOT_KEY TTL. The key
			// carries the strategy's update time, so writing it again either
			// refreshes an identical value or restores the exact version this
			// event refers to.
			pipe.Set(ctx, snapshot.Key, []byte(snapshot.Value), time.Hour)
		}
		return nil
	})
	return err
}
