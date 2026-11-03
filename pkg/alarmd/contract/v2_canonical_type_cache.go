// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package contract

import (
	"reflect"
	"sync"
)

// Whether a type is closed is fixed when the program is compiled. Deriving it
// again on every call is work that cannot produce a different answer.
//
// This cache lives in its own file on purpose, and that placement is the whole
// of an earlier decision that still stands: v2_canonical.go defines persisted
// identity and holds no cross-call state. Caching a property of the program's
// own types is not state about any value, but keeping it out of that file
// keeps the rule checkable rather than argued.
//
// It was measured, cached, and then removed once before, and the reasoning for
// removing it deserves to sit next to the reasoning for bringing it back,
// because otherwise this goes around a third time.
//
//	e3b39224, 2026-09-10, dropped the cache. It measured the walk correctly --
//	62 ns for a string, 376 ns for the Slot identity, 5345 ns for the State
//	mutation payload -- and concluded the whole of it was "about 6 us per
//	series, near one per cent of what a series spends in evaluation". One per
//	cent did not justify holding cross-call state in that file, and that was a
//	fair trade at one per cent.
//
//	The per-call cost was right. The call frequency was not: it was reasoned
//	from one call site, roughly once per series. A production CPU profile puts
//	canonicalClosedType at 4.52% of process CPU, which against that same
//	measured per-call cost implies about 14,700 walks a second, or roughly
//	1,755 per completed Slot. CanonicalJSONV2 is called upwards of a thousand
//	times while one Slot completes, and each call walked the type graph again.
//
//	So the number that decided it was out by three orders of magnitude, and it
//	read like a measurement because a measured per-call cost had been
//	multiplied by an unstated assumption inside one sentence.
//
// Bound: the keys are reflect.Type values, so the set is closed by the number
// of distinct types this program passes to CanonicalJSONV2 -- a few dozen, and
// production coverage has observed 53. Nothing at runtime can mint a new one.
// sync.Map gives the concurrency safety; the entries are immutable once
// written, so a racing pair of writers store the same verdict.
var canonicalClosedTypeCache sync.Map // reflect.Type -> bool

// canonicalClosedTypeCached answers for a top-level value only. The recursive
// descent still carries its path and is not cached, because a nested verdict
// can depend on what is already on the path and only the outermost answer is
// path independent.
//
// Path independence at the top is the reason this is sound rather than merely
// convenient: a type inside a cycle returns false whichever member of the
// cycle the walk entered from, and a type outside every cycle never consults
// the path at all.
func canonicalClosedTypeCached(t reflect.Type) bool {
	if t == nil {
		return false
	}
	if verdict, ok := canonicalClosedTypeCache.Load(t); ok {
		return verdict.(bool)
	}
	verdict := canonicalClosedType(t, nil)
	canonicalClosedTypeCache.Store(t, verdict)
	return verdict
}
