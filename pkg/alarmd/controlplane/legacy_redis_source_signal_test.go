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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// The Legacy source reports the cache manager's change signal as written: the
// integer second of the run that last changed something. Anything else it
// finds under that key is reported as no signal, which makes the reconciler
// read everything, as it did before the signal was consulted.
func TestLegacyRedisStrategySourceReadsTheChangeSignalAsWritten(t *testing.T) {
	ctx := context.Background()
	client := newControlplaneRedis(t)
	source := newRedisStrategySource(t, client)
	if signal, err := source.ChangeSignal(ctx); err != nil || signal != (controlplane.SourceChangeSignal{}) {
		t.Fatalf("ChangeSignal() without the key = (%+v, %v), want absent", signal, err)
	}
	if err := client.Set(ctx, "bkmonitor.cache.last_updated", "1700000000", 0).Err(); err != nil {
		t.Fatal(err)
	}
	signal, err := source.ChangeSignal(ctx)
	if err != nil || !signal.Present || signal.Value != "1700000000" || !signal.WrittenAt.Equal(time.Unix(1_700_000_000, 0)) {
		t.Fatalf("ChangeSignal() = (%+v, %v), want the written second", signal, err)
	}
	if signal.HoldsLastGoodFor != "" {
		t.Fatalf("ChangeSignal() without the writer's statement = %+v, want no statement", signal)
	}
	for _, unreadable := range []string{"", "not-a-second", "-5", "0", "1700000000.5"} {
		if err := client.Set(ctx, "bkmonitor.cache.last_updated", unreadable, 0).Err(); err != nil {
			t.Fatal(err)
		}
		if signal, err := source.ChangeSignal(ctx); err != nil || signal.Present {
			t.Fatalf("ChangeSignal() with %q = (%+v, %v), want absent", unreadable, signal, err)
		}
	}
}

// writerStrategyIDs and writerStatement are a publication as the writer
// makes it: strategy_ids stored as these bytes, and the statement naming
// their SHA-256 and the last_updated of the same publication. The digest is
// the writer's own, not one computed here, so the two sides are held to one
// spelling.
const (
	writerStrategyIDs    = `[7,425]`
	writerLastUpdated    = "1788868800"
	writerIDsDigest      = "972cd23cb4e47ffb41b7c85ed20c48e074da6b4f0b509e291cb522396bb337ef"
	writerStatement      = `{"hold_last_good":true,"last_updated":1788868800,"strategy_ids_sha256":"` + writerIDsDigest + `","version":1}`
	publicationStatement = "bkmonitor.cache.publication_semantics"
)

// The writer's publication statement counts only as written for this very
// change signal, and only as naming a strategy_ids digest spelled the way the
// writer spells it. Every other shape - a statement left behind by a later
// last_updated, one of another version, one saying false, the first version-1
// shape that named no digest, a digest spelled otherwise, one that does not
// decode, one that cannot be read, or none at all - is no statement.
func TestLegacyRedisStrategySourceTakesTheWritersStatementOnlyForItsOwnSignal(t *testing.T) {
	ctx := context.Background()
	client := newControlplaneRedis(t)
	source := newRedisStrategySource(t, client)
	if err := client.Set(ctx, publicationStatement, writerStatement, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if signal, err := source.ChangeSignal(ctx); err != nil || signal != (controlplane.SourceChangeSignal{}) {
		t.Fatalf("ChangeSignal() with a statement and no last_updated = (%+v, %v), want absent", signal, err)
	}
	if err := client.Set(ctx, "bkmonitor.cache.last_updated", writerLastUpdated, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if signal, err := source.ChangeSignal(ctx); err != nil || !signal.Present || signal.HoldsLastGoodFor != writerIDsDigest {
		t.Fatalf("ChangeSignal() with the statement for this signal = (%+v, %v), want the digest it names", signal, err)
	}
	for name, statement := range map[string]string{
		"written for an earlier signal":  `{"hold_last_good":true,"last_updated":1788868799,"strategy_ids_sha256":"` + writerIDsDigest + `","version":1}`,
		"another version":                `{"hold_last_good":true,"last_updated":1788868800,"strategy_ids_sha256":"` + writerIDsDigest + `","version":2}`,
		"saying false":                   `{"hold_last_good":false,"last_updated":1788868800,"strategy_ids_sha256":"` + writerIDsDigest + `","version":1}`,
		"without a version":              `{"hold_last_good":true,"last_updated":1788868800,"strategy_ids_sha256":"` + writerIDsDigest + `"}`,
		"without a digest":               `{"hold_last_good":true,"last_updated":1788868800,"version":1}`,
		"with an empty digest":           `{"hold_last_good":true,"last_updated":1788868800,"strategy_ids_sha256":"","version":1}`,
		"with an uppercase digest":       `{"hold_last_good":true,"last_updated":1788868800,"strategy_ids_sha256":"` + strings.ToUpper(writerIDsDigest) + `","version":1}`,
		"with a short digest":            `{"hold_last_good":true,"last_updated":1788868800,"strategy_ids_sha256":"` + writerIDsDigest[:63] + `","version":1}`,
		"with a digest that is no hex":   `{"hold_last_good":true,"last_updated":1788868800,"strategy_ids_sha256":"` + writerIDsDigest[:63] + `g","version":1}`,
		"with a digest that is a number": `{"hold_last_good":true,"last_updated":1788868800,"strategy_ids_sha256":972,"version":1}`,
		"with last_updated as a string":  `{"hold_last_good":true,"last_updated":"1788868800","strategy_ids_sha256":"` + writerIDsDigest + `","version":1}`,
		"not JSON":                       `hold_last_good`,
		"empty":                          ``,
	} {
		if err := client.Set(ctx, publicationStatement, statement, 0).Err(); err != nil {
			t.Fatal(err)
		}
		if signal, err := source.ChangeSignal(ctx); err != nil || !signal.Present || signal.HoldsLastGoodFor != "" {
			t.Fatalf("ChangeSignal() with a statement %s = (%+v, %v), want the signal without it", name, signal, err)
		}
	}
	// A statement that cannot be read is no statement, and does not fail the
	// signal: the round goes on with every guard in place.
	if err := client.Del(ctx, publicationStatement).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(ctx, publicationStatement, "hold_last_good", "true").Err(); err != nil {
		t.Fatal(err)
	}
	if signal, err := source.ChangeSignal(ctx); err != nil || !signal.Present || signal.HoldsLastGoodFor != "" {
		t.Fatalf("ChangeSignal() with an unreadable statement = (%+v, %v), want the signal without it", signal, err)
	}
	if err := client.Del(ctx, publicationStatement).Err(); err != nil {
		t.Fatal(err)
	}
	if signal, err := source.ChangeSignal(ctx); err != nil || !signal.Present || signal.HoldsLastGoodFor != "" {
		t.Fatalf("ChangeSignal() after the statement expired = (%+v, %v), want the signal without it", signal, err)
	}
}

// The Legacy source names the active set it read by the SHA-256 of the
// strategy_ids value exactly as the store returned it. The same ids stored
// with other spacing are other bytes, and another digest: the writer's
// statement is about what it stored, not about the ids it meant.
func TestLegacyRedisStrategySourceNamesTheActiveSetByItsStoredBytes(t *testing.T) {
	ctx := context.Background()
	client := newControlplaneRedis(t)
	source := newRedisStrategySource(t, client)
	if _, digest, err := source.ActiveStrategyIDsWithDigest(ctx); err == nil || digest != "" {
		t.Fatalf("ActiveStrategyIDsWithDigest() without the key = (%q, %v), want the incomplete source", digest, err)
	}
	if err := client.Set(ctx, "bkmonitor.cache.strategy_ids", writerStrategyIDs, 0).Err(); err != nil {
		t.Fatal(err)
	}
	ids, digest, err := source.ActiveStrategyIDsWithDigest(ctx)
	if err != nil || len(ids) != 2 || ids[0] != "7" || ids[1] != "425" || digest != writerIDsDigest {
		t.Fatalf("ActiveStrategyIDsWithDigest() = (%v, %q, %v), want [7 425] named %s", ids, digest, err, writerIDsDigest)
	}
	if plain, err := source.ActiveStrategyIDs(ctx); err != nil || len(plain) != 2 || plain[0] != "7" || plain[1] != "425" {
		t.Fatalf("ActiveStrategyIDs() = (%v, %v), want the ids the digest read returned", plain, err)
	}
	for _, respelled := range []string{`[7, 425]`, ` [7,425]`, "[7,425]\n"} {
		if err := client.Set(ctx, "bkmonitor.cache.strategy_ids", respelled, 0).Err(); err != nil {
			t.Fatal(err)
		}
		ids, digest, err := source.ActiveStrategyIDsWithDigest(ctx)
		if err != nil || len(ids) != 2 || ids[0] != "7" || ids[1] != "425" {
			t.Fatalf("ActiveStrategyIDsWithDigest() of %q = (%v, %v), want the same ids", respelled, ids, err)
		}
		if sum := sha256.Sum256([]byte(respelled)); digest != hex.EncodeToString(sum[:]) || digest == writerIDsDigest {
			t.Fatalf("ActiveStrategyIDsWithDigest() of %q named %q, want the digest of those bytes", respelled, digest)
		}
	}
	if err := client.Set(ctx, "bkmonitor.cache.strategy_ids", `[7,"x"]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if _, digest, err := source.ActiveStrategyIDsWithDigest(ctx); !errors.Is(err, controlplane.ErrLegacySourceIncomplete) || digest != "" {
		t.Fatalf("ActiveStrategyIDsWithDigest() of an invalid set = (%q, %v), want it refused without a digest", digest, err)
	}
}
