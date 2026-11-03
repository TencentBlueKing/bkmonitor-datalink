// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package openalerts

import (
	"context"
	"errors"
	"testing"
	"time"
)

// ListActiveAlerts says whether the alerts were listed at all, apart from
// what it lists: a complete calibration with no alerts lists none, and is
// listed; a cut one that names an alert is listed; a set never calibrated,
// and a cut answer that names no alert, are not - their empty answer is not
// "none open".
func TestListActiveAlertsTellsAnUnlistedSetFromAnEmptyOne(t *testing.T) {
	t.Run("never calibrated", func(t *testing.T) {
		c := &clock{at: time.Unix(1700000000, 0)}
		options := indexOptions(c)
		options.Facts = confirmedFacts()
		options.Source = setReaderFunc(func(context.Context, StrategyKey) ([]string, error) { return []string{"open"}, nil })
		options.Reconciler = reconcilerFunc(func(context.Context, StrategyKey) (Reconciliation, error) {
			return Reconciliation{}, errors.New("reconcile: link answered 503")
		})
		cache := mustIndex(t, options)
		if err := cache.SetTracked([]StrategyKey{keyA}); err != nil {
			t.Fatal(err)
		}
		cache.Refresh(context.Background())
		if alerts, listed := cache.ListActiveAlerts(keyA); listed || alerts != nil {
			t.Fatalf("ListActiveAlerts = %+v, %v; want nothing, not listed", alerts, listed)
		}
	})
	t.Run("calibrated and empty", func(t *testing.T) {
		c := &clock{at: time.Unix(1700000000, 0)}
		options := indexOptions(c)
		options.Facts = confirmedFacts()
		options.Source = setReaderFunc(func(context.Context, StrategyKey) ([]string, error) { return nil, nil })
		options.Reconciler = reconcilerFunc(func(context.Context, StrategyKey) (Reconciliation, error) {
			return Reconciliation{EventSourceID: "source"}, nil
		})
		cache := mustIndex(t, options)
		if err := cache.SetTracked([]StrategyKey{keyA}); err != nil {
			t.Fatal(err)
		}
		cache.Refresh(context.Background())
		if alerts, listed := cache.ListActiveAlerts(keyA); !listed || alerts == nil || len(alerts) != 0 {
			t.Fatalf("ListActiveAlerts = %+v, %v; want none, listed", alerts, listed)
		}
	})
	t.Run("cut, naming an alert", func(t *testing.T) {
		f := newTruncatedFixture(t)
		f.answer = listedOnly()
		f.recalibrate(f.options.CalibrationMaxAge + time.Second)
		if alerts, listed := f.cache.ListActiveAlerts(keyA); !listed || len(alerts) != 1 || alerts[0].AlertID != "l" {
			t.Fatalf("ListActiveAlerts = %+v, %v; want the listed alert, listed", alerts, listed)
		}
	})
	t.Run("cut, naming none", func(t *testing.T) {
		f := newTruncatedFixture(t)
		f.answer = Reconciliation{EventSourceID: "source", Truncated: true}
		f.recalibrate(f.options.CalibrationMaxAge + time.Second)
		if alerts, listed := f.cache.ListActiveAlerts(keyA); listed || alerts != nil {
			t.Fatalf("ListActiveAlerts = %+v, %v; want nothing, not listed", alerts, listed)
		}
	})
}
