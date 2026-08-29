// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package coordinator

import (
	"context"
	"testing"
	"time"
)

func TestOrderedKeyGateSerializesSharedKeyInRegistrationOrder(t *testing.T) {
	t.Parallel()

	gate := NewOrderedKeyGate()
	first, err := gate.Register(10, []RuntimeKey{{StrategyID: "1"}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := gate.Register(11, []RuntimeKey{{StrategyID: "1"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertReservationBlocked(t, second)
	first.Release()
	if err := second.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	second.Release()
}

func TestOrderedKeyGateRunsDisjointKeysConcurrently(t *testing.T) {
	t.Parallel()

	gate := NewOrderedKeyGate()
	first, err := gate.Register(20, []RuntimeKey{{StrategyID: "1"}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := gate.Register(21, []RuntimeKey{{StrategyID: "2"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := second.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	first.Release()
	second.Release()
}

func TestOrderedKeyGateAcquiresMultipleKeysAtomically(t *testing.T) {
	t.Parallel()

	gate := NewOrderedKeyGate()
	first, err := gate.Register(30, []RuntimeKey{{StrategyID: "1"}})
	if err != nil {
		t.Fatal(err)
	}
	middle, err := gate.Register(31, []RuntimeKey{{StrategyID: "1"}, {StrategyID: "2"}})
	if err != nil {
		t.Fatal(err)
	}
	later, err := gate.Register(32, []RuntimeKey{{StrategyID: "2"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertReservationBlocked(t, middle)
	assertReservationBlocked(t, later)
	first.Release()
	if err := middle.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertReservationBlocked(t, later)
	middle.Release()
	if err := later.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	later.Release()
}

func TestOrderedKeyGateCancelRemovesPendingReservation(t *testing.T) {
	t.Parallel()

	gate := NewOrderedKeyGate()
	first, err := gate.Register(40, []RuntimeKey{{StrategyID: "1"}})
	if err != nil {
		t.Fatal(err)
	}
	canceled, err := gate.Register(41, []RuntimeKey{{StrategyID: "1"}})
	if err != nil {
		t.Fatal(err)
	}
	later, err := gate.Register(42, []RuntimeKey{{StrategyID: "1"}})
	if err != nil {
		t.Fatal(err)
	}
	canceled.Cancel()
	first.Release()
	if err := later.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	later.Release()
}

func assertReservationBlocked(t *testing.T, reservation *KeyReservation) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := reservation.Wait(ctx); err == nil {
		t.Fatal("reservation acquired a key before the earlier registered owner released it")
	}
}
