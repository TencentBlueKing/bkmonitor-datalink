package redis

import (
	"context"
	"crypto/sha256"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

// ScrollCleanupTimeout 是整个会话清理共享的时限，避免耗时随目标数量累加。
const ScrollCleanupTimeout = 2 * time.Second

// ClearScrollFunc 由调用方注入存储操作，避免 Redis 会话层反向依赖 TSDB 层。
type ClearScrollFunc func(context.Context, ElasticsearchScrollTarget, []string) error

// clearCompletedScrolls 在会话结束后回收全部已保存的 ES 游标，清理失败只记录日志。
func (session *ScrollSession) clearCompletedScrolls(ctx context.Context, clearScroll ClearScrollFunc) {
	// HTTP 层在参数错误等场景也会返回 done=true；只有会话自身结束才能释放续查所需的游标。
	if !session.Done() {
		return
	}

	// 日志用摘要关联会话，避免输出原始会话键。
	sessionID := sha256.Sum256([]byte(session.SessionKey))
	// 按完整目标分组，避免将不同来源、空间或租户的游标合并到同一次请求。
	groups := make(map[ElasticsearchScrollTarget][]string)
	// 遍历会话保存的全部分片，覆盖提前完成、当前查询计划已不再访问的分支。
	for _, slice := range session.Slices() {
		if slice.ScrollID == "" {
			continue
		}
		if slice.ScrollTarget == nil {
			// 旧会话若未能补齐目标，就无法确定清理路由；保留告警并等待 ES keep-alive 到期回收。
			log.Warnf(ctx, "scroll cleanup target missing: session=%x slice=%s", sessionID, slice.SliceKey)
			continue
		}
		groups[*slice.ScrollTarget] = append(groups[*slice.ScrollTarget], slice.ScrollID)
	}
	if len(groups) == 0 {
		return
	}

	// 保留请求的上下文值，但让已完成会话的清理不受客户端取消影响。
	// 所有目标（包括排队中的目标）共享同一个 deadline。
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
	// 限制同时清理的目标数，并等待本轮清理收尾后释放会话锁。
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
