// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// withoutLifetime is a full backend less the one capability under test. Each
// of the three below drops exactly one, so the refusal can be checked to name
// that one and not the others.
type withoutLifetime struct{ *casMemoryBackend }

func (withoutLifetime) RenewIfBelow() {}

type withoutNoDataHash struct{ *casMemoryBackend }

func (withoutNoDataHash) ReadHashField() {}

type withoutCompareAndSet struct{ *casMemoryBackend }

func (withoutCompareAndSet) CompareAndSet() {}

func probeOptions(router StorageRouter) ExecutionStoreOptions {
	return ExecutionStoreOptions{
		Prefix: "alarmd", Router: router, MaxValueBytes: 4096, MaxItemsPerCall: 4,
		MinTTL: time.Minute, MaxTTL: 30 * 24 * time.Hour, RestartMargin: time.Minute,
	}
}

// A backend that lacks a capability the store will need is refused when the
// store opens, by name. Until now each of these was met on a Slot, as a
// deterministic refusal that recurred every round for as long as the process
// lived, on the one deployment -- production -- whose backend nobody had
// compiled a test double for.
func TestAnExecutionStoreRefusesToOpenOnATargetLackingACapability(t *testing.T) {
	full := &casMemoryBackend{values: map[string][]byte{}}
	for _, tc := range []struct {
		name    string
		backend Backend
		lacks   BackendCapability
	}{
		{"no compare-and-set", withoutCompareAndSet{full}, CapabilityCompareAndSet},
		{"no lifetime renewal", withoutLifetime{full}, CapabilityLifetime},
		{"no no-data hash", withoutNoDataHash{full}, CapabilityNoDataHash},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router, err := NewFixedRouter("primary", tc.backend)
			if err != nil {
				t.Fatal(err)
			}
			store, err := NewExecutionStore(probeOptions(router))
			if err == nil || store != nil {
				t.Fatalf("a store opened on a backend lacking %s", tc.lacks)
			}
			text := err.Error()
			if !strings.Contains(text, "primary") || !strings.Contains(text, string(tc.lacks)) {
				t.Fatalf("refusal %q does not name the target and the missing capability %s", text, tc.lacks)
			}
			for _, other := range executionStoreCapabilities {
				if other != tc.lacks && strings.Contains(text, string(other)) {
					t.Fatalf("refusal %q blames %s, which the backend has", text, other)
				}
			}
		})
	}
	router, err := NewFixedRouter("primary", full)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewExecutionStore(probeOptions(router)); err != nil {
		t.Fatalf("a fully capable backend was refused: %v", err)
	}
}

// The refusal lists everything at once. A deployment that wired the wrong
// client wants the whole list on the first restart, not one item per restart.
func TestTheProbeNamesEveryMissingCapabilityOfEveryTarget(t *testing.T) {
	router, err := NewFixedRouter("primary", &readOnlyBackend{values: map[string][]byte{}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewExecutionStore(probeOptions(router))
	if err == nil {
		t.Fatal("a read-only backend opened an execution store")
	}
	for _, capability := range executionStoreCapabilities {
		if !strings.Contains(err.Error(), string(capability)) {
			t.Fatalf("refusal %q omits %s", err.Error(), capability)
		}
	}
	// A router with nothing to list is refused too: a store that checked
	// nothing would report itself as checked.
	if _, err := NewExecutionStore(probeOptions(emptyRouter{})); err == nil {
		t.Fatal("a router listing no target opened an execution store")
	}
}

type emptyRouter struct{}

func (emptyRouter) Route(string, string) (StorageTarget, error) {
	return StorageTarget{}, context.DeadlineExceeded
}

func (emptyRouter) Targets() []StorageTarget { return nil }

// The slot-applied mark store has its own capability and its own probe.
func TestTheSlotAppliedMarkStoreRefusesToOpenWithoutItsBackend(t *testing.T) {
	router, err := NewFixedRouter("primary", &casMemoryBackend{values: map[string][]byte{}})
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewSlotAppliedMarkStore("alarmd", router)
	if err == nil || store != nil {
		t.Fatal("a mark store opened on a backend without the slot-applied set")
	}
	if !strings.Contains(err.Error(), string(CapabilitySlotApplied)) {
		t.Fatalf("refusal %q does not name %s", err.Error(), CapabilitySlotApplied)
	}
}

// Every capability a store asserts at a point of use is in the probe's table,
// so a new assertion cannot be added without deciding whether it is required
// at open. The one exception is the fenced batch, whose sequential fallback is
// the documented reason it is not required.
func TestEveryPointOfUseAssertionIsProbedAtOpen(t *testing.T) {
	probed := map[string]bool{
		"CompareAndSetBackend": true, "LifetimeBackend": true, "NoDataHashBackend": true, "SlotAppliedBackend": true,
		// Optional by design: the sequential path is its fallback.
		"FencedBatchBackend": true,
	}
	// The names above are the ones backendHas maps to capabilities; a name
	// added here without a backendHas branch would pass the scan and probe
	// nothing, so each required one is checked to be refused by the probe.
	for name, capability := range map[string]BackendCapability{
		"CompareAndSetBackend": CapabilityCompareAndSet, "LifetimeBackend": CapabilityLifetime,
		"NoDataHashBackend": CapabilityNoDataHash, "SlotAppliedBackend": CapabilitySlotApplied,
	} {
		if backendHas(&readOnlyBackend{}, capability) {
			t.Fatalf("backendHas says a read-only backend has %s (%s)", capability, name)
		}
	}
	assertion := regexp.MustCompile(`\.Backend\.\((\w+)\)`)
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range assertion.FindAllStringSubmatch(string(source), -1) {
			seen[match[1]] = true
			if !probed[match[1]] {
				t.Errorf("%s asserts %s at a point of use, and the probe at open does not know it: "+
					"add it to backendHas and to the store's capability list, or document why it is optional",
					file, match[1])
			}
		}
	}
	for _, required := range []string{"CompareAndSetBackend", "LifetimeBackend", "NoDataHashBackend", "SlotAppliedBackend"} {
		if !seen[required] {
			t.Errorf("no point of use asserts %s any more; drop it from the probe or the scan is stale", required)
		}
	}
}
