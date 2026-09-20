// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package platformsettings

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Mode is the state of the process copy. It is a state, read at scrape and
// at publish; nothing here is a transition.
type Mode string

const (
	// ModeNotConfigured: the deployment renders no distribution source. The
	// publisher is not deployed, or alarmd was not told where it is; the
	// copy answers from the deployment layer and the code defaults, which
	// is what alarmd did before this package, and is not a fault.
	ModeNotConfigured Mode = "not_configured"
	// ModeNeverLoaded: a source is configured and no read has found a
	// publication yet since the process started. Same answer as
	// not_configured, different meaning: something is expected and has not
	// arrived.
	ModeNeverLoaded Mode = "never_loaded"
	// ModeAuthoritative: the last read found a publication and every field
	// decoded; the copy answers from it.
	ModeAuthoritative Mode = "authoritative"
	// ModeStale: there was a publication once and the last read did not
	// yield one -- the read failed, the revision was gone, or a field did
	// not decode. The copy answers from the last publication; past the
	// staleness bound the deployment's health says so.
	ModeStale Mode = "stale"
)

// Modes is the closed set, for a reader that pre-creates a series per mode.
var Modes = []Mode{ModeNotConfigured, ModeNeverLoaded, ModeAuthoritative, ModeStale}

// UnavailableReason says why a refresh did not yield a publication.
type UnavailableReason string

const (
	// UnavailableReadError: the read did not happen.
	UnavailableReadError UnavailableReason = "read_error"
	// UnavailableUnpublished: the revision key is absent. Per protocol
	// nothing was ever published, and the field keys beside it are not read
	// as "no override": "never published" and "override removed" have to
	// stay two different states.
	UnavailableUnpublished UnavailableReason = "unpublished"
	// UnavailableDecodeError: a field's JSON is not of its declared type.
	// The whole publication is refused rather than the one field: a
	// publication half applied is a state nobody can reason about.
	UnavailableDecodeError UnavailableReason = "decode_error"
)

// UnavailableReasons is the closed set.
var UnavailableReasons = []UnavailableReason{UnavailableReadError, UnavailableUnpublished, UnavailableDecodeError}

// DefaultStalenessBound is how long the copy may answer from a publication
// the source no longer yields before the deployment is degraded for it. The
// protocol's own reconciliation is what refreshes the distribution; twice
// the cadence alarmd asked the platform for (every few minutes) is ten
// minutes, and that is the bound whether or not the platform reconciles
// that often: the promise here is minutes, not the platform's day.
const DefaultStalenessBound = 10 * time.Minute

// RefreshInterval is how often the copy reads the distribution. The
// protocol requires a periodic full read of every consumer; a minute is the
// change detection latency alarmd promises for every platform-side input.
const RefreshInterval = time.Minute

// Stats is what the copy reports about itself.
type Stats struct {
	Mode     Mode
	Revision string
	// LoadedAt is when the last publication was read. Zero until there has
	// been one; a reader emits no age from a zero.
	LoadedAt    time.Time
	Unavailable map[UnavailableReason]uint64
	Refreshes   map[string]uint64
	// Changes counts, per field, the refreshes on which the effective value
	// changed: the count an operator's page edit produces exactly once.
	Changes map[Field]uint64
	// LastUnavailable is the text of the last failure, kept beside the
	// counts because a decode error names the field and the value shape,
	// which the counter cannot.
	LastUnavailable string
}

// Cache is the process copy.
type Cache struct {
	mu         sync.RWMutex
	source     Source
	deployment Layer
	defaults   Settings
	now        func() time.Time
	bound      time.Duration

	mode            Mode
	current         Settings
	platform        *Layer
	revision        string
	loadedAt        time.Time
	unavailable     map[UnavailableReason]uint64
	refreshes       map[string]uint64
	changes         map[Field]uint64
	lastUnavailable string
}

// Options configure a copy. A nil Source is a deployment that renders no
// distribution: the copy is not_configured for its whole life.
type Options struct {
	Source Source
	// Deployment is alarmd's own layer: what its configuration states,
	// standing in for the YAML layer the platform's own consumers hold.
	Deployment Layer
	Now        func() time.Time
	// StalenessBound overrides DefaultStalenessBound; zero keeps it.
	StalenessBound time.Duration
}

func New(options Options) (*Cache, error) {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.StalenessBound < 0 {
		return nil, errors.New("alarmd platformsettings: the staleness bound must not be negative")
	}
	if options.StalenessBound == 0 {
		options.StalenessBound = DefaultStalenessBound
	}
	cache := &Cache{
		source: options.Source, deployment: options.Deployment, defaults: CodeDefaults(),
		now: options.Now, bound: options.StalenessBound,
		mode:        ModeNeverLoaded,
		unavailable: make(map[UnavailableReason]uint64, len(UnavailableReasons)),
		refreshes:   make(map[string]uint64, 2),
		changes:     make(map[Field]uint64, len(Fields)),
	}
	if options.Source == nil {
		cache.mode = ModeNotConfigured
	}
	cache.current = Resolve(cache.defaults, cache.deployment)
	return cache, nil
}

// Current is the effective settings: the platform's publication resolved
// through the deployment layer and the code defaults, or, before any
// publication, the deployment layer and the defaults alone.
func (cache *Cache) Current() Settings {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	return cache.current
}

// Refresh reads the distribution once and installs what it found. Called
// on a fixed cadence by the runtime, and never by a lookup.
func (cache *Cache) Refresh(ctx context.Context) {
	if cache == nil || cache.source == nil {
		return
	}
	publication, err := cache.source.Read(ctx, Fields)
	if err != nil {
		cache.becomeUnavailable(UnavailableReadError, err.Error())
		return
	}
	if !publication.Published {
		cache.becomeUnavailable(UnavailableUnpublished, "the revision key is absent")
		return
	}
	platform, err := decodeLayer(publication.Values)
	if err != nil {
		cache.becomeUnavailable(UnavailableDecodeError, err.Error())
		return
	}
	cache.becomeAuthoritative(publication.Revision, platform)
}

func (cache *Cache) becomeUnavailable(reason UnavailableReason, text string) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.refreshes["unavailable"]++
	cache.unavailable[reason]++
	cache.lastUnavailable = text
	if cache.mode == ModeAuthoritative {
		cache.mode = ModeStale
	}
}

func (cache *Cache) becomeAuthoritative(revision string, platform Layer) {
	resolved := Resolve(cache.defaults, platform, cache.deployment)
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.refreshes["authoritative"]++
	for _, field := range cache.current.ChangedFields(resolved) {
		cache.changes[field]++
	}
	cache.current = resolved
	cache.platform = &platform
	cache.revision = revision
	cache.loadedAt = cache.now()
	cache.mode = ModeAuthoritative
	cache.lastUnavailable = ""
}

// Stats reports the copy's state and counts.
func (cache *Cache) Stats() Stats {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	stats := Stats{
		Mode: cache.mode, Revision: cache.revision, LoadedAt: cache.loadedAt, LastUnavailable: cache.lastUnavailable,
		Unavailable: make(map[UnavailableReason]uint64, len(UnavailableReasons)),
		Refreshes:   make(map[string]uint64, 2),
		Changes:     make(map[Field]uint64, len(Fields)),
	}
	for reason, count := range cache.unavailable {
		stats.Unavailable[reason] = count
	}
	for result, count := range cache.refreshes {
		stats.Refreshes[result] = count
	}
	for field, count := range cache.changes {
		stats.Changes[field] = count
	}
	return stats
}

// StaleBeyondBound is the one fact the deployment's verdict reads: the copy
// had a publication and has been without one for longer than the bound. A
// copy that never loaded is not stale -- the publisher may not be deployed
// -- and the mode says so without degrading anything.
func (cache *Cache) StaleBeyondBound() bool {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	return cache.mode == ModeStale && !cache.loadedAt.IsZero() && cache.now().Sub(cache.loadedAt) > cache.bound
}

// decodeLayer turns the distributed JSON into a Layer, strictly by each
// field's declared type. A key that is absent is absent from the layer. A
// JSON null is present: for a list field it is the empty list, for a bool
// it is not a bool and the publication is refused.
func decodeLayer(values map[Field]json.RawMessage) (Layer, error) {
	var layer Layer
	for _, field := range Fields {
		raw, present := values[field]
		if !present {
			continue
		}
		switch field {
		case FieldIsAccessBKData:
			value, err := decodeBool(raw)
			if err != nil {
				return Layer{}, fmt.Errorf("alarmd platformsettings: field %s: %w", field, err)
			}
			layer.IsAccessBKData = &value
		default:
			value, err := decodeStringList(raw)
			if err != nil {
				return Layer{}, fmt.Errorf("alarmd platformsettings: field %s: %w", field, err)
			}
			switch field {
			case FieldHostDisableMonitorStates:
				layer.HostDisableMonitorStates = &value
			case FieldBKDataCMDBLevelTables:
				layer.BKDataCMDBLevelTables = &value
			case FieldFileSystemTypeIgnore:
				layer.FileSystemTypeIgnore = &value
			}
		}
	}
	return layer, nil
}

var jsonNull = []byte("null")

func decodeBool(raw json.RawMessage) (bool, error) {
	trimmed := bytes.TrimSpace(raw)
	switch string(trimmed) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return false, fmt.Errorf("%q is not a JSON boolean", shorten(trimmed))
}

func decodeStringList(raw json.RawMessage) ([]string, error) {
	trimmed := bytes.TrimSpace(raw)
	if bytes.Equal(trimmed, jsonNull) {
		return []string{}, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(trimmed, &items); err != nil {
		return nil, fmt.Errorf("%q is not a JSON list: %w", shorten(trimmed), err)
	}
	values := make([]string, 0, len(items))
	for index, item := range items {
		var value string
		if err := json.Unmarshal(item, &value); err != nil {
			return nil, fmt.Errorf("element %d %q is not a JSON string", index, shorten(item))
		}
		values = append(values, value)
	}
	return values, nil
}

// shorten bounds how much of a refused value an error repeats: enough to
// find it, not all of it.
func shorten(raw []byte) string {
	const limit = 64
	if len(raw) <= limit {
		return string(raw)
	}
	return string(raw[:limit]) + "..."
}
