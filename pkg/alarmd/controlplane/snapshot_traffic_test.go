// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane_test

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// snapshotTrafficHook counts the Redis traffic that carries a Snapshot's
// content in either direction: arguments at least as long as the payload
// (content sent) and replies at least as long as it (content read). The keys
// touched are not counted; a renewal has to name the Snapshot key to move its
// expiry, and that is not moving the content.
type snapshotTrafficHook struct {
	mu          sync.Mutex
	payloadLen  int
	contentSent int
	contentRead int
}

func (hook *snapshotTrafficHook) reset() {
	hook.mu.Lock()
	defer hook.mu.Unlock()
	hook.contentSent, hook.contentRead = 0, 0
}

func (hook *snapshotTrafficHook) counts() (sent, read int) {
	hook.mu.Lock()
	defer hook.mu.Unlock()
	return hook.contentSent, hook.contentRead
}

func (hook *snapshotTrafficHook) before(cmd redis.Cmder) {
	hook.mu.Lock()
	defer hook.mu.Unlock()
	for _, arg := range cmd.Args() {
		if hook.payloadLen > 0 && argumentBytes(arg) >= hook.payloadLen {
			hook.contentSent++
		}
	}
}

func (hook *snapshotTrafficHook) after(cmd redis.Cmder) {
	hook.mu.Lock()
	defer hook.mu.Unlock()
	if hook.payloadLen > 0 && replyBytes(cmd) >= hook.payloadLen {
		hook.contentRead++
	}
}

func argumentBytes(value interface{}) int {
	switch typed := value.(type) {
	case string:
		return len(typed)
	case []byte:
		return len(typed)
	case []interface{}:
		total := 0
		for _, element := range typed {
			total += argumentBytes(element)
		}
		return total
	default:
		return 0
	}
}

func replyBytes(cmd redis.Cmder) int {
	switch typed := cmd.(type) {
	case *redis.StringCmd:
		return len(typed.Val())
	case *redis.SliceCmd:
		return argumentBytes(typed.Val())
	case *redis.Cmd:
		return argumentBytes(typed.Val())
	default:
		return 0
	}
}

func (hook *snapshotTrafficHook) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	hook.before(cmd)
	return ctx, nil
}

func (hook *snapshotTrafficHook) AfterProcess(_ context.Context, cmd redis.Cmder) error {
	hook.after(cmd)
	return nil
}

func (hook *snapshotTrafficHook) BeforeProcessPipeline(ctx context.Context, cmds []redis.Cmder) (context.Context, error) {
	for _, cmd := range cmds {
		hook.before(cmd)
	}
	return ctx, nil
}

func (hook *snapshotTrafficHook) AfterProcessPipeline(_ context.Context, cmds []redis.Cmder) error {
	for _, cmd := range cmds {
		hook.after(cmd)
	}
	return nil
}

// activatedHarness takes the change-gate harness to a settled, activated
// publication and returns the activation, the Snapshot key and its bytes.
func activatedHarness(t *testing.T) (*changeGateHarness, controlplane.ActivationState, string, []byte) {
	t.Helper()
	harness := newChangeGateHarness(t)
	settled := harness.settle()
	activation, err := controlplane.NewScheduleActivationReconciler(harness.repository, harness.compiler, harness.semantics,
		func() time.Time { return harness.clock })
	if err != nil {
		t.Fatal(err)
	}
	state, err := activation.Ensure(harness.ctx, settled.Publication)
	if err != nil {
		t.Fatal(err)
	}
	snapshotKey := harness.prefix + ":snapshot:" + string(state.Current.SnapshotRevision)
	payload, err := harness.client.Get(harness.ctx, snapshotKey).Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return harness, state, snapshotKey, payload
}

// Every round whose source did not change used to encode the whole Snapshot
// and send it to Redis, where a script compared it with the stored bytes and
// wrote it back unchanged. The Snapshot key is content-addressed and its
// readers verify the content, so a round that finds the key now moves its
// expiry and nothing else: the bytes Redis holds after the round are the
// bytes it held before, and the content travels only when the key is gone.
func TestUnchangedRoundRenewsTheActiveSnapshotWithoutMovingItsContent(t *testing.T) {
	harness, _, snapshotKey, payload := activatedHarness(t)
	hook := &snapshotTrafficHook{payloadLen: len(payload)}
	harness.client.AddHook(hook)
	if err := harness.client.PExpire(harness.ctx, snapshotKey, 2*time.Second).Err(); err != nil {
		t.Fatal(err)
	}
	harness.refresh(controlplane.SourceRefreshUnchanged, controlplane.SourceReadSkipped, controlplane.SourceReadUnchanged, 0)
	if sent, read := hook.counts(); sent != 0 || read != 0 {
		t.Fatalf("an unchanged round moved the Snapshot content: sent %d, read %d", sent, read)
	}
	after, err := harness.client.Get(harness.ctx, snapshotKey).Bytes()
	if err != nil || !bytes.Equal(after, payload) {
		t.Fatalf("the Snapshot bytes changed across an unchanged round (err=%v)", err)
	}
	if ttl, err := harness.client.PTTL(harness.ctx, snapshotKey).Result(); err != nil || ttl < 30*time.Minute {
		t.Fatalf("Snapshot TTL after the round = (%s, %v), want renewed", ttl, err)
	}

	// The Snapshot expired: the next round brings the content back, once, and
	// what it writes is what was there.
	if err := harness.client.Del(harness.ctx, snapshotKey).Err(); err != nil {
		t.Fatal(err)
	}
	hook.reset()
	harness.refresh(controlplane.SourceRefreshUnchanged, controlplane.SourceReadSkipped, controlplane.SourceReadUnchanged, 0)
	if sent, _ := hook.counts(); sent != 1 {
		t.Fatalf("the round after the Snapshot expired sent its content %d times, want once", sent)
	}
	restored, err := harness.client.Get(harness.ctx, snapshotKey).Bytes()
	if err != nil || !bytes.Equal(restored, payload) {
		t.Fatalf("the restored Snapshot differs from the one that expired (err=%v)", err)
	}
	// And the round after that is back to moving nothing.
	hook.reset()
	harness.refresh(controlplane.SourceRefreshUnchanged, controlplane.SourceReadSkipped, controlplane.SourceReadUnchanged, 0)
	if sent, read := hook.counts(); sent != 0 || read != 0 {
		t.Fatalf("the round after a restoration moved the Snapshot content: sent %d, read %d", sent, read)
	}
}

// The activation renewal used to read the Snapshot and the Active Set in full
// and send both back for the script to compare. Both keys are
// content-addressed, and the repository has already verified the Snapshot it
// loaded; the renewal now proves the objects are still there and moves their
// expiry without carrying either.
func TestActivationRenewalMovesNoSnapshotContent(t *testing.T) {
	harness, state, snapshotKey, payload := activatedHarness(t)
	hook := &snapshotTrafficHook{payloadLen: len(payload)}
	harness.client.AddHook(hook)
	if err := harness.client.PExpire(harness.ctx, snapshotKey, 2*time.Second).Err(); err != nil {
		t.Fatal(err)
	}
	if err := harness.repository.RenewCurrentActivationObjects(harness.ctx); err != nil {
		t.Fatalf("RenewCurrentActivationObjects() error = %v", err)
	}
	if sent, read := hook.counts(); sent != 0 || read != 0 {
		t.Fatalf("the renewal moved the Snapshot content: sent %d, read %d", sent, read)
	}
	if ttl, err := harness.client.PTTL(harness.ctx, snapshotKey).Result(); err != nil || ttl < 30*time.Minute {
		t.Fatalf("Snapshot TTL after renewal = (%s, %v), want renewed", ttl, err)
	}
	// A Snapshot that is gone is still not renewed, and says so.
	// The body is renewed while it is there but no longer required; the
	// manifest is the current content whose absence refuses the renewal.
	if err := harness.client.Del(harness.ctx, snapshotKey).Err(); err != nil {
		t.Fatal(err)
	}
	if err := harness.repository.RenewCurrentActivationObjects(harness.ctx); err != nil {
		t.Fatalf("renewal without the Snapshot body error = %v, want success", err)
	}
	if err := harness.client.Del(harness.ctx, harness.prefix+":manifest:"+string(state.Current.SnapshotRevision)).Err(); err != nil {
		t.Fatal(err)
	}
	if err := harness.repository.RenewCurrentActivationObjects(harness.ctx); err == nil {
		t.Fatalf("renewal without the manifest succeeded for revision %s", state.Current.SnapshotRevision)
	}
}

// A repository reads a Snapshot revision in full once and verifies it; every
// later load of the same revision reuses that, checking only that Redis still
// holds it under the same epoch at the same size.
func TestLoadSnapshotReadsARevisionInFullOnlyOnce(t *testing.T) {
	harness, state, _, payload := activatedHarness(t)
	fresh, err := controlplane.NewRedisCatalogRepository(harness.client, harness.prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	hook := &snapshotTrafficHook{payloadLen: len(payload)}
	harness.client.AddHook(hook)
	first, err := fresh.LoadPublishedSnapshot(harness.ctx, state.Current)
	if err != nil {
		t.Fatal(err)
	}
	if _, read := hook.counts(); read != 1 {
		t.Fatalf("the first load read the content %d times, want once", read)
	}
	hook.reset()
	second, err := fresh.LoadPublishedSnapshot(harness.ctx, state.Current)
	if err != nil {
		t.Fatal(err)
	}
	if sent, read := hook.counts(); sent != 0 || read != 0 {
		t.Fatalf("the second load moved the content: sent %d, read %d", sent, read)
	}
	if len(second.QueryGroups) != len(first.QueryGroups) || second.Publication != first.Publication {
		t.Fatalf("the reused load differs: %d vs %d Query Groups", len(second.QueryGroups), len(first.QueryGroups))
	}
}
