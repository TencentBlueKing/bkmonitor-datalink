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
	"fmt"
	"sort"
	"strings"
)

// A store asks its backend for more than the Backend interface promises: a
// compare-and-set for the write path, a lifetime renewal for the keys it reads
// and does not write, a hash for the no-data memory, a set for the slot-applied
// mark. Each of those used to be a type assertion at the point of use, and an
// assertion at the point of use fails at the point of use: on a Slot, after
// the read, as a named refusal that recurs every round for as long as the
// deployment runs. Every test double had the method; only production could
// lack it; and the symptom would have been the very state loss the method
// exists to prevent.
//
// So the assertions are made once, when the store opens, against every target
// the router can hand out. A backend that lacks a capability is wiring, not
// weather, and wiring is refused at startup with the missing capabilities
// named. The assertions at the point of use stay, as the defense for a router
// that routes to something other than what it listed.

// BackendCapability is one thing a store needs its backend to be able to do.
type BackendCapability string

const (
	CapabilityCompareAndSet BackendCapability = "compare_and_set"
	CapabilityLifetime      BackendCapability = "lifetime_renewal"
	CapabilityNoDataHash    BackendCapability = "no_data_hash"
	CapabilitySlotApplied   BackendCapability = "slot_applied_mark"
)

// executionStoreCapabilities is what the execution store needs of every
// target: the sequential write path, the generation and runtime-state
// renewals, and the no-data memory. The fenced batch is not in the list: the
// sequential path is its documented fallback, so a backend without it works,
// only slower.
var executionStoreCapabilities = []BackendCapability{
	CapabilityCompareAndSet, CapabilityLifetime, CapabilityNoDataHash,
}

// slotAppliedMarkCapabilities is what the slot-applied mark store needs.
var slotAppliedMarkCapabilities = []BackendCapability{CapabilitySlotApplied}

// backendHas answers whether one backend provides one capability. This is the
// single place the capability names meet the interfaces; the assertions at
// the points of use assert the same interfaces.
func backendHas(backend Backend, capability BackendCapability) bool {
	if backend == nil {
		return false
	}
	switch capability {
	case CapabilityCompareAndSet:
		_, ok := backend.(CompareAndSetBackend)
		return ok
	case CapabilityLifetime:
		_, ok := backend.(LifetimeBackend)
		return ok
	case CapabilityNoDataHash:
		_, ok := backend.(NoDataHashBackend)
		return ok
	case CapabilitySlotApplied:
		_, ok := backend.(SlotAppliedBackend)
		return ok
	}
	return false
}

// MissingBackendCapabilities lists, per target name, the capabilities the
// router's targets lack, sorted so the error text is stable. Empty when every
// target has every capability.
func MissingBackendCapabilities(router StorageRouter, required []BackendCapability) map[string][]BackendCapability {
	missing := make(map[string][]BackendCapability)
	if router == nil {
		return missing
	}
	for _, target := range router.Targets() {
		for _, capability := range required {
			if !backendHas(target.Backend, capability) {
				missing[target.Name] = append(missing[target.Name], capability)
			}
		}
	}
	return missing
}

// probeBackendCapabilities is the refusal a store constructor makes. The text
// names every target and every capability it lacks, because a deployment that
// wired the wrong client wants the whole list once, not one item per restart.
func probeBackendCapabilities(store string, router StorageRouter, required []BackendCapability) error {
	if router == nil {
		return fmt.Errorf("state: %s requires a storage router", store)
	}
	targets := router.Targets()
	if len(targets) == 0 {
		return fmt.Errorf("state: %s storage router lists no target", store)
	}
	missing := MissingBackendCapabilities(router, required)
	if len(missing) == 0 {
		return nil
	}
	names := make([]string, 0, len(missing))
	for name := range missing {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		capabilities := make([]string, 0, len(missing[name]))
		for _, capability := range missing[name] {
			capabilities = append(capabilities, string(capability))
		}
		parts = append(parts, fmt.Sprintf("%s lacks %s", name, strings.Join(capabilities, ", ")))
	}
	return fmt.Errorf("state: %s cannot open: storage target %s", store, strings.Join(parts, "; "))
}
