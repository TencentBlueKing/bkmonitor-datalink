// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package merge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/lifecycle"
	"linkd/internal/lifecycle/mailbox"
	"linkd/internal/policy"
	"linkd/internal/store"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
)

type memoryDocuments struct {
	mu       sync.Mutex
	data     map[string]json.RawMessage
	versions map[string]int
	failKind string
}

func (d *memoryDocuments) Get(ctx context.Context, kind, id string) (json.RawMessage, string, error) {
	if ctx.Err() != nil {
		return nil, "", ctx.Err()
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	key := kind + "/" + id
	raw, ok := d.data[key]
	if !ok {
		return nil, "", policy.ErrNotFound
	}
	return slices.Clone(raw), fmt.Sprint(d.versions[key]), nil
}

func (d *memoryDocuments) Put(ctx context.Context, kind, id, expected string, raw json.RawMessage) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.failKind == kind {
		d.failKind = ""
		return errors.New("storage unavailable")
	}
	key := kind + "/" + id
	version := d.versions[key]
	if expected == "" && version != 0 || expected != "" && expected != fmt.Sprint(version) {
		return policy.ErrConflict
	}
	d.data[key] = slices.Clone(raw)
	d.versions[key]++
	return nil
}

func (d *memoryDocuments) List(ctx context.Context, kind, prefix, after string, limit int) ([]json.RawMessage, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	keys := []string{}
	for key := range d.data {
		if strings.HasPrefix(key, kind+"/"+prefix) && strings.TrimPrefix(key, kind+"/") > after {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	rows := []json.RawMessage{}
	for _, key := range keys[:min(limit, len(keys))] {
		rows = append(rows, slices.Clone(d.data[key]))
	}
	return rows, nil
}

func newJournal(t *testing.T) (*Journal, *memoryDocuments) {
	t.Helper()
	d := &memoryDocuments{data: map[string]json.RawMessage{}, versions: map[string]int{}}
	j, err := NewJournal(d)
	if err != nil {
		t.Fatal(err)
	}
	return j, d
}

func decisionFixture(t *testing.T) Decision {
	t.Helper()
	raw := json.RawMessage(`{"name":"merge","is_enable":true,"updated_at":"2026-09-30T00:00:00Z","space_code":"bkcc__2","activate_times":[{"period":"everyday","open_clock_time":"00:00:00","close_clock_time":"23:59:59"}],"policy":[{"expression":"A","A":{"condition":"term","target_key":"level","target_value":"warning"}}],"merge_cycle":60,"is_cycle_merge":false,"aggregate_fields":[],"new_alarm_config":[{"key":"name","value":"joint ${alarm_num}"},{"key":"level","value":"warning"}]}`)
	compiled, err := policy.Compile(policy.Merge, raw)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	id, err := domain.MergeDecisionID("tenant", strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	return Decision{ID: id, TenantID: "tenant", WindowID: strings.Repeat("b", 64), GroupKey: strings.Repeat("c", 64), Policy: policy.Release{Scope: policy.Scope{TenantID: "tenant", Kind: policy.Merge}, ID: "merge", Version: 1, Spec: compiled.Canonical, Compiled: compiled.Summary}, FrozenAt: at.Add(time.Second), StartedAt: at, Deadline: at.Add(time.Minute), Outcome: "succeeded", MemberIDs: []string{"a", "b"}, WaitMemberIDs: []string{"a", "b", "uncommitted"}}
}

func parentEvent(t *testing.T, d Decision) domain.Event {
	t.Helper()
	e := storetest.Event(d.TenantID, "placeholder", "placeholder", "warning")
	e.EventSourceID = domain.BuiltinMergeEventSourceID
	e.Fingerprint = ParentFingerprint(d.TenantID, d.Policy.ID, d.MemberIDs)
	e.SourceEventID = d.ID
	e.SourceAlertID = e.Fingerprint
	e.OccurredAt = d.FrozenAt
	e.ProducedAt = d.FrozenAt
	e.ReceivedAt = d.FrozenAt
	e.CreateAt = d.FrozenAt
	var err error
	e.EventID, err = domain.GenerateEventID(d.TenantID, e.EventSourceID, e.SourceEventID, d.FrozenAt)
	if err != nil {
		t.Fatal(err)
	}
	e.MergeOrigin = &domain.MergeOrigin{OperationID: d.ID, Policy: domain.PolicyVersion{ID: d.Policy.ID, Version: d.Policy.Version, Digest: d.Policy.Compiled.Digest}, WindowID: d.WindowID, MembersDigest: MembersDigest(d.MemberIDs), MemberCount: len(d.MemberIDs)}
	e, err = e.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func captureMembers(t *testing.T, j *Journal, d Decision) {
	t.Helper()
	for _, id := range d.MemberIDs {
		a := storetest.Alert(d.TenantID, id, "event-"+id, "fp-"+id, "warning")
		if _, err := j.Capture(t.Context(), d.TenantID, d.ID, a); err != nil {
			t.Fatal(err)
		}
	}
}

func preparedFixture(t *testing.T, j *Journal) StoredDecision {
	t.Helper()
	d := decisionFixture(t)
	saved, err := j.Claim(t.Context(), d)
	if err != nil {
		t.Fatal(err)
	}
	captureMembers(t, j, saved.Decision)
	prepared, err := j.Prepare(t.Context(), saved, parentEvent(t, saved.Decision), d.FrozenAt)
	if err != nil {
		t.Fatal(err)
	}
	return prepared
}

func TestJournalFreezesMembersAndParentBeforeEnqueue(t *testing.T) {
	j, _ := newJournal(t)
	d := decisionFixture(t)
	saved, err := j.Claim(t.Context(), d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := j.Prepare(t.Context(), saved, parentEvent(t, saved.Decision), d.FrozenAt); !errors.Is(err, policy.ErrNotFound) {
		t.Fatal("partial snapshots prepared parent", err)
	}
	captureMembers(t, j, saved.Decision)
	changed := storetest.Alert("tenant", "a", "event-a", "fp-a", "warning")
	changed.Title = "changed after first snapshot"
	got, err := j.Capture(t.Context(), "tenant", d.ID, changed)
	if err != nil || got.Alert.Title == changed.Title {
		t.Fatal("snapshot refreshed", err)
	}
	prepared, err := j.Prepare(t.Context(), saved, parentEvent(t, saved.Decision), d.FrozenAt)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := j.Claim(t.Context(), d)
	if err != nil || !reflect.DeepEqual(replay, prepared) {
		t.Fatal("claim reset progress", err)
	}
	broken := d.Clone()
	broken.GroupKey = strings.Repeat("d", 64)
	if _, err := j.Claim(t.Context(), broken); !errors.Is(err, policy.ErrConflict) {
		t.Fatal("operation identity reused for different facts", err)
	}
	if _, err := j.Get(t.Context(), "other", d.ID); !errors.Is(err, policy.ErrNotFound) {
		t.Fatal("tenant lookup escaped scope", err)
	}
	rows, next, err := j.ListMembers(t.Context(), "tenant", d.ID, "", 1)
	if err != nil || len(rows) != 1 || next == "" {
		t.Fatal("member paging", err)
	}
	rows, _, err = j.ListMembers(t.Context(), "tenant", d.ID, next, 1)
	if err != nil || len(rows) != 1 {
		t.Fatal(err)
	}
	if _, _, err := j.ListMembers(t.Context(), "other", d.ID, next, 1); !errors.Is(err, policy.ErrInvalid) {
		t.Fatal("cross-tenant member cursor accepted", err)
	}
	if _, err := j.RejectBeforeEvent(t.Context(), prepared, "render_failed", d.FrozenAt); err == nil {
		t.Fatal("prepared Event abandoned")
	}
}

func TestJournalCASRealParentAndCompletionOrdering(t *testing.T) {
	j, _ := newJournal(t)
	prepared := preparedFixture(t, j)
	at := prepared.Decision.FrozenAt
	waiting, err := j.MarkEnqueued(t.Context(), prepared, at)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := j.MarkEnqueued(t.Context(), prepared, at); !errors.Is(err, policy.ErrConflict) {
		t.Fatal("stale CAS accepted", err)
	}
	repo := memory.New()
	e := waiting.Decision.Progress.ParentEvent
	a := storetest.Alert("tenant", "parent", e.EventID, e.Fingerprint, "warning")
	a.EventSourceID = domain.BuiltinMergeEventSourceID
	a.Merge = &domain.AlertMerge{Role: "aggregate", State: "none", OperationID: waiting.Decision.ID}
	if _, err := j.RecordParent(t.Context(), waiting, store.StoredAlert{Alert: a}, at); err == nil {
		t.Fatal("expected parent ID counted as real parent")
	}
	created, err := repo.CreateAlert(t.Context(), a)
	if err != nil {
		t.Fatal(err)
	}
	linking, err := j.RecordParent(t.Context(), waiting, created.StoredAlert, at)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := j.Complete(t.Context(), linking, &created.StoredAlert, at); err == nil {
		t.Fatal("completed before member operations")
	}
	linked, err := j.AdvanceMembers(t.Context(), linking, len(linking.Decision.WaitMemberIDs), at)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := j.Complete(t.Context(), linked, &created.StoredAlert, at); err == nil {
		t.Fatal("completed before parent relation readiness")
	}
	next := created.Alert.Clone()
	next.UpdateAt = next.UpdateAt.Add(time.Second)
	next.Merge.RelationsReady = true
	next.Merge.State = "merged"
	next.Merge.RelationIDs = []string{linking.Decision.ID}
	ready, err := repo.CompareAndSetAlert(t.Context(), a.BKTenantID, a.AlertID, created.Version, next)
	if err != nil {
		t.Fatal(err)
	}
	done, err := j.Complete(t.Context(), linked, &ready, at)
	if err != nil || done.Decision.Progress.Phase != "completed" {
		t.Fatal(err)
	}
	page, err := j.ListWork(t.Context(), "", 16)
	if err != nil || len(page.Decisions) != 1 {
		t.Fatal("unconfirmed window cleanup lost", err)
	}
	if _, err := j.MarkWindowFinished(t.Context(), done, at); err != nil {
		t.Fatal(err)
	}
	page, err = j.ListWork(t.Context(), "", 16)
	if err != nil || len(page.Decisions) != 0 {
		t.Fatal("completed work returned", err)
	}
}

func TestJournalRetainsRealParentIfItEndedBeforeObservation(t *testing.T) {
	j, _ := newJournal(t)
	prepared := preparedFixture(t, j)
	waiting, err := j.MarkEnqueued(t.Context(), prepared, prepared.Decision.FrozenAt)
	if err != nil {
		t.Fatal(err)
	}
	e := waiting.Decision.Progress.ParentEvent
	a := storetest.Alert("tenant", "ended-parent", e.EventID, e.Fingerprint, "warning")
	a.EventSourceID = domain.BuiltinMergeEventSourceID
	a.Merge = &domain.AlertMerge{Role: "aggregate", State: "none", OperationID: waiting.Decision.ID}
	repo := memory.New()
	created, err := repo.CreateAlert(t.Context(), a)
	if err != nil {
		t.Fatal(err)
	}
	a.Status = domain.AlertStatusClosed
	a.EndType = domain.AlertEndTypeUser
	a.EndReason = "manual"
	a.UpdateAt = a.UpdateAt.Add(time.Second)
	a.EndAt = &a.UpdateAt
	real, err := repo.CompareAndSetAlert(t.Context(), a.BKTenantID, a.AlertID, created.Version, a)
	if err != nil {
		t.Fatal(err)
	}
	ended, err := j.RecordParent(t.Context(), waiting, real, waiting.Decision.FrozenAt)
	if err != nil || ended.Decision.Progress.Phase != "ending" || ended.Decision.Progress.ParentAlertID != a.AlertID || ended.Decision.Progress.ReasonCode != "parent_ended" {
		t.Fatal("terminal parent treated as failed creation", err)
	}
	if _, err := j.AdvanceMembers(t.Context(), ended, 1, waiting.Decision.FrozenAt); err == nil {
		t.Fatal("ending could use ordinary release progress")
	}
}

func TestJournalFindsFrozenDecisionByWindowWithoutRedis(t *testing.T) {
	j, _ := newJournal(t)
	d := decisionFixture(t)
	if _, err := j.GetByWindow(t.Context(), d.TenantID, d.WindowID); !errors.Is(err, policy.ErrNotFound) {
		t.Fatal("missing window misreported", err)
	}
	saved, err := j.Claim(t.Context(), d)
	if err != nil {
		t.Fatal(err)
	}
	found, err := j.GetByWindow(t.Context(), d.TenantID, d.WindowID)
	if err != nil || !reflect.DeepEqual(found, saved) {
		t.Fatal("window lookup requires Redis", err)
	}
	conflicting := d.Clone()
	conflicting.MemberIDs = []string{"a", "uncommitted"}
	if _, err := j.Claim(t.Context(), conflicting); !errors.Is(err, policy.ErrConflict) {
		t.Fatal("one window accepted two member verdicts", err)
	}
	if _, err := j.GetByWindow(t.Context(), "foreign", d.WindowID); !errors.Is(err, policy.ErrNotFound) {
		t.Fatal("window lookup escaped tenant", err)
	}
	if _, err := j.GetByWindow(t.Context(), d.TenantID, "bad"); !errors.Is(err, policy.ErrInvalid) {
		t.Fatal("bad window accepted", err)
	}
}

type mailboxFailure struct {
	repo   store.Repository
	calls  int
	events []domain.Event
	fail   bool
}

func (m *mailboxFailure) EnqueueBatch(ctx context.Context, events []domain.Event) ([]mailbox.EnqueueResult, error) {
	m.calls++
	for _, e := range events {
		if _, err := m.repo.GetEvent(ctx, e.BKTenantID, e.EventID); err != nil {
			return nil, errors.New("mailbox before persistence")
		}
		m.events = append(m.events, e.Clone())
	}
	if m.fail {
		m.fail = false
		return nil, errors.New("unknown enqueue result")
	}
	return []mailbox.EnqueueResult{{Signaled: true}}, nil
}

func TestPublisherRetriesPreparedEventAfterUnknownMailboxOutcome(t *testing.T) {
	j, _ := newJournal(t)
	prepared := preparedFixture(t, j)
	repo := memory.New()
	mailboxes := &mailboxFailure{repo: repo, fail: true}
	p := Publisher{Journal: j, Events: repo, Mailboxes: mailboxes}
	if _, err := p.Submit(t.Context(), "tenant", prepared.Decision.ID, prepared.Decision.FrozenAt); err == nil {
		t.Fatal("unknown enqueue outcome hidden")
	}
	current, err := j.Get(t.Context(), "tenant", prepared.Decision.ID)
	if err != nil || current.Decision.Progress.Phase != "prepared" {
		t.Fatal("advanced before enqueue confirmation", err)
	}
	final, err := p.Submit(t.Context(), "tenant", prepared.Decision.ID, prepared.Decision.FrozenAt.Add(time.Minute))
	if err != nil || final.Decision.Progress.Phase != "waiting_parent" {
		t.Fatal(err)
	}
	if len(mailboxes.events) != 2 || !reflect.DeepEqual(mailboxes.events[0], mailboxes.events[1]) {
		t.Fatal("retry changed internal Event identity or payload")
	}
	if _, err := p.Submit(t.Context(), "tenant", prepared.Decision.ID, prepared.Decision.FrozenAt.Add(time.Hour)); err != nil || mailboxes.calls != 2 {
		t.Fatal("submitted stage enqueued again", err)
	}
}

func TestJournalFailedWindowAndCanceledCapture(t *testing.T) {
	j, _ := newJournal(t)
	d := decisionFixture(t)
	d.Outcome = "failed"
	d.FrozenAt = d.Deadline
	d.MemberIDs = nil
	failed, err := j.Claim(t.Context(), d)
	if err != nil || failed.Decision.Progress.Phase != "releasing" {
		t.Fatal(err)
	}
	if _, err := j.AdvanceMembers(t.Context(), failed, 4, d.FrozenAt); err == nil {
		t.Fatal("cursor advanced past member set")
	}
	released, err := j.AdvanceMembers(t.Context(), failed, 3, d.FrozenAt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := j.Complete(t.Context(), released, nil, d.FrozenAt); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := j.Get(ctx, "tenant", d.ID); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation swallowed", err)
	}
}

type journalClock struct{ at time.Time }

func (c journalClock) Now() time.Time { return c.at }

type actionCounter struct{ calls int }

func (h *actionCounter) Execute(context.Context, lifecycle.FinalHookInput) (lifecycle.FinalHookResult, error) {
	h.calls++
	return lifecycle.FinalHookResult{Skipped: true}, nil
}

func TestInternalEventCreatesRealAggregateButWaitsForRelations(t *testing.T) {
	j, _ := newJournal(t)
	prepared := preparedFixture(t, j)
	repo := memory.New()
	mailboxes := &mailboxFailure{repo: repo}
	publisher := Publisher{Journal: j, Events: repo, Mailboxes: mailboxes}
	waiting, err := publisher.Submit(t.Context(), "tenant", prepared.Decision.ID, prepared.Decision.FrozenAt)
	if err != nil {
		t.Fatal(err)
	}
	hook := &actionCounter{}
	processor, err := lifecycle.NewProcessor(repo, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, enrich.NoopEnricher{}, []lifecycle.NamedFinalHook{{Name: "action", Purpose: "action", Hook: hook}}, config.DefaultSeverityConfig(), journalClock{at: waiting.Decision.FrozenAt.Add(time.Second)}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	event, err := repo.GetEvent(t.Context(), "tenant", waiting.Decision.Progress.ParentEvent.EventID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := processor.ProcessEvent(t.Context(), event)
	if err != nil || len(result.AlertIDs) != 1 {
		t.Fatal("internal event did not create real parent", err)
	}
	parent, err := repo.GetAlert(t.Context(), "tenant", result.AlertIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if parent.Alert.Merge == nil || parent.Alert.Merge.Role != "aggregate" || parent.Alert.Merge.RelationsReady || parent.Alert.Admission.AdmittedAt != nil || hook.calls != 0 {
		t.Fatal("parent action ran before relations")
	}
	linking, err := j.RecordParent(t.Context(), waiting, parent, waiting.Decision.FrozenAt)
	if err != nil || linking.Decision.Progress.ParentAlertID != parent.Alert.AlertID {
		t.Fatal("real parent did not advance journal", err)
	}
}

func TestRedisInternalPublisherUsesExistingMailboxProtocol(t *testing.T) {
	address := os.Getenv("LINKD_TEST_REDIS_ADDRESS")
	if address == "" {
		t.Skip("set LINKD_TEST_REDIS_ADDRESS")
	}
	client := redis.NewClient(&redis.Options{Addr: address, Password: os.Getenv("LINKD_TEST_REDIS_PASSWORD"), ContextTimeoutEnabled: true, MaxRetries: 0})
	if err := client.Ping(t.Context()).Err(); err != nil {
		t.Fatal(err)
	}
	prefix := fmt.Sprintf("linkd-merge-publish-test:%d:%d", os.Getpid(), time.Now().UnixNano())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var cursor uint64
		for {
			keys, next, err := client.Scan(ctx, cursor, prefix+":*", 100).Result()
			if err != nil {
				t.Error(err)
				break
			}
			if len(keys) > 0 {
				if err := client.Del(ctx, keys...).Err(); err != nil {
					t.Error(err)
				}
			}
			cursor = next
			if next == 0 {
				break
			}
		}
		_ = client.Close()
	})
	mailboxes, err := mailbox.NewStore(client, mailbox.Config{KeyPrefix: prefix + ":mailbox", SignalStream: prefix + ":signals", MaxPendingPerMailbox: 32})
	if err != nil {
		t.Fatal(err)
	}
	j, _ := newJournal(t)
	prepared := preparedFixture(t, j)
	repo := memory.New()
	publisher := Publisher{Journal: j, Events: repo, Mailboxes: mailboxes}
	if _, err := publisher.Submit(t.Context(), prepared.Decision.TenantID, prepared.Decision.ID, prepared.Decision.FrozenAt); err != nil {
		t.Fatal(err)
	}
	e := prepared.Decision.Progress.ParentEvent
	key := mailbox.CorrelationKey(e.BKTenantID, e.EventSourceID, e.Fingerprint)
	head, err := mailboxes.Peek(t.Context(), key)
	if err != nil || head != e.EventID {
		t.Fatal("internal event not in normal fingerprint mailbox", err)
	}
	if n, err := client.XLen(t.Context(), prefix+":signals").Result(); err != nil || n != 1 {
		t.Fatal("missing ordinary lifecycle signal", err)
	}
	if err := mailboxes.AckHead(t.Context(), key, e.EventID); err != nil {
		t.Fatal(err)
	}
}
