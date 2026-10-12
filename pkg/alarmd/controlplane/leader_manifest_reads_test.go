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
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// The Control Leader's rounds on a publication that has not changed read no
// manifest.
//
// Two of them used to read the whole of it every time: the reconcile round,
// for each Query Group's content scope, and the refresh round's renewal, only
// to prove the manifest was still there. The manifest is most of a megabyte
// on a few thousand Query Groups and the reconcile round runs every few
// seconds, so that was a few hundred KB a second out of a shared Redis, and
// as much JSON decoded, for an answer that cannot change under one revision.
//
// The count is taken at the socket, as the other manifest read tests do.
func TestALeaderRoundOnAnUnchangedPublicationReadsNoManifest(t *testing.T) {
	fixture := newCutoverFixture(t, "alarmd:control:leader-manifest-reads")
	fixture.publish(t, cutoverCatalog(t, 80, nil), 60)
	ctx := context.Background()

	// A fresh process, as a newly elected leader is.
	repository, err := controlplane.NewRedisCatalogRepository(fixture.client, fixture.prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	gets := newControlGetCountingHook()
	fixture.client.AddHook(gets)
	round := func() {
		t.Helper()
		// What the reconcile round reads for the content scopes, as the view
		// reads it.
		state, err := repository.LoadActivationHead(ctx)
		if err != nil {
			t.Fatal(err)
		}
		published, err := repository.LoadPublishedContent(ctx, state.Current)
		if err != nil {
			t.Fatal(err)
		}
		blocked, err := repository.ActivationBlocked(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_ = controlplane.ApplyBlockedToContent(published.Groups, blocked)
		// And the refresh round's renewal.
		if err := repository.RenewCurrentActivationObjects(ctx); err != nil {
			t.Fatal(err)
		}
	}

	// The first round on a revision decodes it where each reader keeps it:
	// the published content, the renewal's proof that it decodes, and the
	// object catalog's list of the keys it names. Once each per revision.
	round()
	if got := gets.count("manifest"); got > 3 {
		t.Fatalf("the first round read the manifest %d times, want at most one for each of the three "+
			"readers that keep it per revision", got)
	}
	gets.reset()
	const rounds = 5
	for index := 0; index < rounds; index++ {
		round()
	}
	if got := gets.count("manifest"); got != 0 {
		t.Fatalf("%d rounds on an unchanged publication read the manifest %d times, want 0: one revision's "+
			"manifest cannot change, and every round after the first is told that by an existence check",
			rounds, got)
	}

	// The read the reconcile round used to make, for comparison: the whole
	// manifest, every round.
	state, err := repository.LoadActivationHead(ctx)
	if err != nil {
		t.Fatal(err)
	}
	gets.reset()
	for index := 0; index < rounds; index++ {
		if _, err := repository.LoadCatalogManifest(ctx, state.Current.SnapshotRevision); err != nil {
			t.Fatal(err)
		}
	}
	if got := gets.count("manifest"); got != rounds {
		t.Fatalf("the uncached read counted %d manifest GETs over %d calls, want one each: the hook does not "+
			"see what this test says it sees", got, rounds)
	}
}

// A renewal that finds the manifest gone says the snapshot is unavailable,
// as it did when it read the manifest through: the decoded copy is not an
// answer about the store.
func TestARenewalThatFindsTheManifestGoneSaysTheSnapshotIsUnavailable(t *testing.T) {
	fixture := newCutoverFixture(t, "alarmd:control:renewal-manifest-gone")
	fixture.publish(t, cutoverCatalog(t, 80, nil), 60)
	ctx := context.Background()
	if err := fixture.repository.RenewCurrentActivationObjects(ctx); err != nil {
		t.Fatal(err)
	}
	state, err := fixture.repository.LoadActivationHead(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.client.Del(ctx, fixture.prefix+":manifest:"+string(state.Current.SnapshotRevision)).Err(); err != nil {
		t.Fatal(err)
	}
	if err := fixture.repository.RenewCurrentActivationObjects(ctx); err != controlplane.ErrSnapshotUnavailable {
		t.Fatalf("renewal after the manifest was deleted = %v, want ErrSnapshotUnavailable", err)
	}
}
