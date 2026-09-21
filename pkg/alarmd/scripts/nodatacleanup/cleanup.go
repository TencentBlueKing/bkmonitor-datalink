// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Command nodatacleanup removes the whole-memory no-data records that the
// per-group representation replaced.
//
// It is run once, by hand, after every worker is on a build that writes the
// per-group record and has been for longer than a rollout window. It is not a
// migration: nothing is copied across, because the new record is written by the
// first round each Plan runs. What is left behind is the old record, which
// nothing reads once the new one is newer, and which would otherwise sit in the
// store until its generation expires.
//
// Enumerating is the default and deleting needs --delete, because the two
// questions "what would this remove" and "remove it" are asked days apart.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

const (
	// wholeMemoryKind and perGroupKind are the key segments that separate the
	// two representations. They are the literal segments the store builds its
	// keys from; a change there and a change here have to happen together,
	// which is what the key-shape test exists to catch.
	wholeMemoryKind = "nodata"
	perGroupKind    = "nodata-hash"
	// headerField is where the per-group record keeps everything that is not a
	// group.
	headerField = "_meta"
	// scanBatch is the COUNT hint. It bounds how much of the keyspace one call
	// walks, not how many keys come back.
	scanBatch = int64(1000)
)

// WholeMemoryPattern is the glob that enumerates the records this removes.
//
// It cannot match a per-group key, and that is a property of the two key
// templates rather than of this string: the character after "nodata" is ":" in
// one and "-" in the other, so a pattern anchored on "nodata:" excludes the
// other key space entirely. Without that the cleanup would walk over live
// memory, and the only thing standing between the walk and a delete would be
// the version check.
func WholeMemoryPattern(prefix string) string {
	return prefix + ":" + wholeMemoryKind + ":v2:*"
}

// perGroupKeyFor is where the same Plan's per-group record lives.
//
// It is derived from the old key by replacing one segment rather than by
// rebuilding the key from an identity, because the identity is not in the key:
// the tenant and the generation are digests. Two keys that differ by one
// segment is exactly what the two templates are.
func perGroupKeyFor(prefix, key string) (string, bool) {
	head := prefix + ":" + wholeMemoryKind + ":"
	if !strings.HasPrefix(key, head) {
		return "", false
	}
	return prefix + ":" + perGroupKind + ":" + strings.TrimPrefix(key, head), true
}

// Store is the part of Redis this needs. It is narrow and returns plain values
// so the whole walk can be driven by a fake: a cleanup tested only against a
// real server is a cleanup nobody tests.
type Store interface {
	// Scan returns one page of keys matching a glob, and the next cursor. A
	// zero cursor ends the walk.
	Scan(ctx context.Context, cursor uint64, match string, count int64) ([]string, uint64, error)
	// Get returns nil and no error for a key that is not there.
	Get(ctx context.Context, key string) ([]byte, error)
	// HGet returns nil and no error for a missing key or a missing field.
	HGet(ctx context.Context, key, field string) ([]byte, error)
	// Dump returns the serialisation RESTORE takes back.
	Dump(ctx context.Context, key string) ([]byte, error)
	Del(ctx context.Context, key string) error
}

// Copier receives one saved record before it is deleted. Writing the copy is
// not optional and not best-effort: a copy that failed to write stops the
// delete for that key, because the delete is the irreversible half.
type Copier interface {
	Save(key string, dump []byte) error
}

// Counts is what one run reports.
//
// Four numbers rather than one, because the interesting reading is the gaps
// between them. Enumerated minus WithPerGroupRecord is Plans that have not run
// since the upgrade; WithPerGroupRecord minus Eligible is Plans whose old
// record is the newer one, which during a rollout is a Plan that went back to
// an older build and is the one case where deleting would lose rounds.
type Counts struct {
	Enumerated         int
	WithPerGroupRecord int
	Eligible           int
	Deleted            int
	// Unreadable is an old record whose bytes could not be decoded. It is
	// skipped rather than deleted: a record nobody can read is the one most
	// worth keeping a copy of, and the version check it would have to pass
	// cannot be run on it.
	Unreadable int
	// Vanished is a key that was enumerated and was gone by the time it was
	// read. Its generation expired between the two, which is the cleanup's own
	// job happening on its own.
	Vanished int
}

func (counts Counts) String() string {
	return fmt.Sprintf(
		"enumerated=%d with_per_group_record=%d eligible=%d deleted=%d unreadable=%d vanished=%d",
		counts.Enumerated, counts.WithPerGroupRecord, counts.Eligible,
		counts.Deleted, counts.Unreadable, counts.Vanished)
}

// Options is one run.
type Options struct {
	Prefix string
	// Delete is false by default. A run that only enumerates answers "what
	// would this remove" and is the one that gets read before the other is run.
	Delete bool
	// Copier is required when Delete is set.
	Copier Copier
}

type wholeMemoryHeader struct {
	Schema       string                       `json:"schema"`
	Version      execution.NoDataMemorySchema `json:"version"`
	ApplyVersion execution.ApplyVersion       `json:"apply_version"`
}

type perGroupHeader struct {
	Schema       string                       `json:"schema"`
	Version      execution.NoDataMemorySchema `json:"version"`
	ApplyVersion execution.ApplyVersion       `json:"apply_version"`
}

// Run walks the old key space once and reports what it found.
func Run(ctx context.Context, store Store, options Options) (Counts, error) {
	var counts Counts
	if options.Prefix == "" {
		return counts, fmt.Errorf("nodatacleanup: a key prefix is required")
	}
	if options.Delete && options.Copier == nil {
		return counts, fmt.Errorf("nodatacleanup: deleting requires somewhere to save the copies")
	}
	pattern := WholeMemoryPattern(options.Prefix)
	var cursor uint64
	for {
		if err := ctx.Err(); err != nil {
			return counts, err
		}
		keys, next, err := store.Scan(ctx, cursor, pattern, scanBatch)
		if err != nil {
			return counts, fmt.Errorf("nodatacleanup: scan: %w", err)
		}
		for _, key := range keys {
			if err := considerOne(ctx, store, options, key, &counts); err != nil {
				return counts, err
			}
		}
		cursor = next
		if cursor == 0 {
			return counts, nil
		}
	}
}

func considerOne(ctx context.Context, store Store, options Options, key string, counts *Counts) error {
	counts.Enumerated++
	raw, err := store.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("nodatacleanup: read %s: %w", key, err)
	}
	if raw == nil {
		counts.Vanished++
		return nil
	}
	var stored wholeMemoryHeader
	if err := json.Unmarshal(raw, &stored); err != nil || stored.Schema == "" || stored.Version == 0 {
		counts.Unreadable++
		return nil
	}
	hashKey, ok := perGroupKeyFor(options.Prefix, key)
	if !ok {
		// The scan answered with a key its own pattern cannot produce.
		counts.Unreadable++
		return nil
	}
	header, err := store.HGet(ctx, hashKey, headerField)
	if err != nil {
		return fmt.Errorf("nodatacleanup: read %s: %w", hashKey, err)
	}
	if header == nil {
		// No per-group record: this Plan has not run since the upgrade, and its
		// only memory is the record being considered for deletion.
		return nil
	}
	counts.WithPerGroupRecord++
	var current perGroupHeader
	if err := json.Unmarshal(header, &current); err != nil || current.Version == 0 {
		counts.Unreadable++
		return nil
	}
	// The per-group record has to be at least as new. Existence alone is not
	// enough: during a rollout a Plan can go back to a build that writes the old
	// record, and then the old one is the memory and the new one is behind it.
	if execution.CompareApplyVersion(current.ApplyVersion, stored.ApplyVersion) ==
		execution.ApplyVersionPersistedOlder {
		return nil
	}
	counts.Eligible++
	if !options.Delete {
		return nil
	}
	dump, err := store.Dump(ctx, key)
	if err != nil {
		return fmt.Errorf("nodatacleanup: dump %s: %w", key, err)
	}
	if dump == nil {
		counts.Vanished++
		return nil
	}
	if err := options.Copier.Save(key, dump); err != nil {
		// The copy is what makes the delete reversible, so a copy that did not
		// land stops the run rather than the key.
		return fmt.Errorf("nodatacleanup: save a copy of %s: %w", key, err)
	}
	if err := store.Del(ctx, key); err != nil {
		return fmt.Errorf("nodatacleanup: delete %s: %w", key, err)
	}
	counts.Deleted++
	return nil
}
