package redis

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestScrollSessionTerminalBoundary(testContext *testing.T) {
	// 重试次数恰好达到上限时，session.Done() 为 true，但 slice.Done() 仍可能为 false。
	// 同时覆盖存在未完成分片的情况，确认清理以整个会话的结束状态为准。
	testCases := []struct {
		name            string
		status          string
		failedNum       int
		withPendingPeer bool
		wantSessionDone bool
		wantSliceDone   bool
	}{
		{name: "pending", status: StatusPending},
		{name: "retry_once", status: StatusPending, failedNum: 1},
		{name: "retry_twice", status: StatusPending, failedNum: 2},
		{name: "retry_exhausted", status: StatusPending, failedNum: 3, wantSessionDone: true},
		{name: "failed_status", status: StatusPending, failedNum: 4, wantSessionDone: true, wantSliceDone: true},
		{name: "completed", status: StatusCompleted, wantSessionDone: true, wantSliceDone: true},
		{name: "completed_with_pending_peer", status: StatusCompleted, withPendingPeer: true, wantSliceDone: true},
		{name: "exhausted_with_pending_peer", status: StatusPending, failedNum: 3, withPendingPeer: true},
	}

	for _, testCase := range testCases {
		testContext.Run(testCase.name, func(testContext *testing.T) {
			session := NewScrollSession(testCase.name, time.Minute, time.Second, 1, DefaultSliceMaxFailedNum, 2)
			slice := session.Slice("table|cluster|0")
			slice.ScrollID = "latest-scroll-id"
			slice.ScrollTarget = &ElasticsearchScrollTarget{StorageID: "cluster"}
			slice.Status = testCase.status
			slice.FailedNum = testCase.failedNum
			session.UpdateSliceStatus(slice.SliceKey, slice)
			if testCase.withPendingPeer {
				session.Slice("other-table|cluster|0")
			}

			if got := session.Done(); got != testCase.wantSessionDone {
				testContext.Fatalf("session.Done() = %v, want %v", got, testCase.wantSessionDone)
			}
			if got := slice.Done(); got != testCase.wantSliceDone {
				testContext.Fatalf("slice.Done() = %v, want %v", got, testCase.wantSliceDone)
			}
			if got := session.Slice(slice.SliceKey).ScrollID; got != "latest-scroll-id" {
				testContext.Fatalf("saved scroll ID = %q, want latest-scroll-id", got)
			}

			var clearedIDs []string
			var clearedTarget ElasticsearchScrollTarget
			session.clearCompletedScrolls(context.Background(), func(_ context.Context, target ElasticsearchScrollTarget, scrollIDs []string) error {
				clearedTarget, clearedIDs = target, scrollIDs
				return nil
			})
			if testCase.wantSessionDone {
				require.Equal(testContext, *slice.ScrollTarget, clearedTarget)
				require.Equal(testContext, []string{slice.ScrollID}, clearedIDs)
			} else {
				require.Empty(testContext, clearedIDs, "unfinished sessions must preserve their scrolls")
			}
		})
	}
}

func TestScrollSessionEmptySession(testContext *testing.T) {
	session := NewScrollSession("empty", time.Minute, time.Second, 1, DefaultSliceMaxFailedNum, 2)
	if session.Done() {
		testContext.Fatal("empty session is unexpectedly complete")
	}
}

func TestScrollSessionSavedTargets(testContext *testing.T) {
	session := NewScrollSession("saved-targets", 5*time.Minute, time.Second, 1, DefaultSliceMaxFailedNum, 2)
	slice := session.Slice("table|cluster|0")
	slice.ScrollID = "latest-scroll-id"
	slice.Status = StatusCompleted
	slice.ScrollTarget = &ElasticsearchScrollTarget{
		StorageID: "cluster", SourceType: "bkdata", SpaceUID: "bkcc__2", TenantID: "tenant",
	}
	session.UpdateSliceStatus(slice.SliceKey, slice)

	encoded, err := session.MarshalBinary()
	if err != nil {
		testContext.Fatal(err)
	}
	var restored ScrollSession
	if err := restored.UnmarshalBinary(encoded); err != nil {
		testContext.Fatal(err)
	}
	snapshots := restored.Slices()
	if len(snapshots) != 1 || *snapshots[0].ScrollTarget != *slice.ScrollTarget || snapshots[0].ScrollID != slice.ScrollID {
		testContext.Fatalf("unexpected saved scroll target: %+v", snapshots)
	}
	snapshots[0].ScrollID = "changed"
	snapshots[0].ScrollTarget.StorageID = "changed"
	if saved := restored.Slice(slice.SliceKey); saved.ScrollID != slice.ScrollID || saved.ScrollTarget.StorageID != "cluster" {
		testContext.Fatalf("snapshot changed saved state: %+v", saved)
	}
	if restored.ScrollWindowTimeout != 5*time.Minute || !restored.Done() {
		testContext.Fatal("serialization changed TTL or terminal state")
	}
}
