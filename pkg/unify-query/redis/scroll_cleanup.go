package redis

import (
	"context"
	"crypto/sha256"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

const ScrollCleanupTimeout = 2 * time.Second

type ClearScrollFunc func(context.Context, ElasticsearchScrollTarget, []string) error

func (session *ScrollSession) clearCompletedScrolls(ctx context.Context, clearScroll ClearScrollFunc) {
	if !session.Done() {
		return
	}

	sessionID := sha256.Sum256([]byte(session.SessionKey))
	groups := make(map[ElasticsearchScrollTarget][]string)
	for _, slice := range session.Slices() {
		if slice.ScrollID == "" {
			continue
		}
		if slice.ScrollTarget == nil {
			log.Warnf(ctx, "scroll cleanup target missing: session=%x slice=%s", sessionID, slice.SliceKey)
			continue
		}
		groups[*slice.ScrollTarget] = append(groups[*slice.ScrollTarget], slice.ScrollID)
	}
	if len(groups) == 0 {
		return
	}

	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ScrollCleanupTimeout)
	defer cancel()
	type cleanupGroup struct {
		target    ElasticsearchScrollTarget
		scrollIDs []string
	}
	work := make(chan cleanupGroup, len(groups))
	for target, scrollIDs := range groups {
		work <- cleanupGroup{target: target, scrollIDs: scrollIDs}
	}
	close(work)

	var workers sync.WaitGroup
	for range min(4, len(groups)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for group := range work {
				if err := clearScroll(cleanupCtx, group.target, group.scrollIDs); err != nil {
					log.Warnf(ctx, "scroll cleanup failed: session=%x storage_id=%s source_type=%s space_uid=%s scroll_count=%d error=%v",
						sessionID, group.target.StorageID, group.target.SourceType, group.target.SpaceUID, len(group.scrollIDs), err)
				}
			}
		}()
	}
	workers.Wait()
}
