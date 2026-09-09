package legacyoutput

import (
	"context"
	"fmt"
	"github.com/go-redis/redis/v8"
	"sort"
	"time"
)

// ServiceRoute follows Python CacheRouter: the first upper bound strictly
// greater than the strategy ID owns that strategy.
type ServiceRoute struct {
	UpperBound int64  `yaml:"upper_bound"`
	NodeID     string `yaml:"node_id"`
}
type RedisSnapshotStore struct {
	Nodes  map[string]redis.Cmdable
	Routes []ServiceRoute
}

func (s RedisSnapshotStore) Validate() error {
	if len(s.Routes) == 0 {
		return fmt.Errorf("legacy service routes required")
	}
	var previous int64
	for _, route := range s.Routes {
		if route.UpperBound <= previous || s.Nodes[route.NodeID] == nil {
			return fmt.Errorf("invalid or unsorted legacy service route")
		}
		previous = route.UpperBound
	}
	return nil
}
func (s RedisSnapshotStore) SaveBatch(ctx context.Context, snapshots []Snapshot) error {
	if err := s.Validate(); err != nil {
		return err
	}
	groups := map[string][]Snapshot{}
	for _, snapshot := range snapshots {
		i := sort.Search(len(s.Routes), func(i int) bool { return s.Routes[i].UpperBound > snapshot.StrategyID })
		if snapshot.StrategyID <= 0 || i == len(s.Routes) {
			return fmt.Errorf("no legacy service route for strategy %d", snapshot.StrategyID)
		}
		id := s.Routes[i].NodeID
		groups[id] = append(groups[id], snapshot)
	}
	for id, batch := range groups {
		client := s.Nodes[id]
		if client == nil {
			return fmt.Errorf("missing legacy service node")
		}
		_, err := client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
			for _, snapshot := range batch {
				pipe.Set(ctx, snapshot.Key, []byte(snapshot.Value), time.Hour)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}
