// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
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
)

// recordReader is the link's record of each alert, counting the reads.
type recordReader struct {
	records map[string]AlertRecord
	reads   int
}

func (r *recordReader) read(_ context.Context, _, alertID string) (AlertRecord, error) {
	r.reads++
	record, ok := r.records[alertID]
	if !ok {
		return AlertRecord{}, ErrAlertNotFound
	}
	return record, nil
}

func filedRecord(alertID string, business int64) AlertRecord {
	return AlertRecord{AlertID: alertID, TenantID: "tenant", EventSourceID: "own", StrategyID: "10", BusinessID: business}
}

// The business an alert was filed under is read from its record once: the
// alert keeps it for its whole life, so the close sent again next round
// asks nothing.
func TestAnAlertsBusinessIsReadOnceAndRemembered(t *testing.T) {
	reader := &recordReader{records: map[string]AlertRecord{"a": filedRecord("a", 2), "b": filedRecord("b", 3)}}
	businesses := NewAlertBusinesses(reader.read)
	for round := 0; round < 3; round++ {
		for id, want := range map[string]int64{"a": 2, "b": 3} {
			got, err := businesses.Of(context.Background(), "tenant", "own", "10", id)
			if err != nil || got != want {
				t.Fatalf("round %d alert %s: %d, %v; want %d", round, id, got, err, want)
			}
		}
	}
	if reader.reads != 2 {
		t.Fatalf("%d record reads for two alerts over three rounds, want 2", reader.reads)
	}
}

// A record of another producer or another strategy, or one without a
// business, is not this alert's business; a failed read is passed on as it
// is. None of them is remembered.
func TestARecordThatIsNotThisAlertsNamesNoBusiness(t *testing.T) {
	foreign := filedRecord("foreign", 2)
	foreign.EventSourceID = "another"
	other := filedRecord("other", 2)
	other.StrategyID = "11"
	reader := &recordReader{records: map[string]AlertRecord{"foreign": foreign, "other": other, "none": filedRecord("none", 0)}}
	businesses := NewAlertBusinesses(reader.read)
	for _, id := range []string{"foreign", "other", "none"} {
		if got, err := businesses.Of(context.Background(), "tenant", "own", "10", id); !errors.Is(err, ErrAlertBusinessUnknown) || got != 0 {
			t.Fatalf("%s: %d, %v; want ErrAlertBusinessUnknown", id, got, err)
		}
	}
	if _, err := businesses.Of(context.Background(), "tenant", "own", "10", "missing"); !errors.Is(err, ErrAlertNotFound) {
		t.Fatalf("a failed read became %v", err)
	}
	reads := reader.reads
	_, _ = businesses.Of(context.Background(), "tenant", "own", "10", "foreign")
	if reader.reads != reads+1 {
		t.Fatal("a record that named no business was remembered")
	}
}

// The memory has a bound: reaching it drops what was remembered, which is
// counted, and the answers stay right - read again from the records.
func TestTheMemoryOfAlertBusinessesIsBounded(t *testing.T) {
	reader := &recordReader{records: map[string]AlertRecord{"a": filedRecord("a", 2), "b": filedRecord("b", 3), "c": filedRecord("c", 4)}}
	businesses := NewAlertBusinesses(reader.read)
	businesses.limit = 2
	for _, id := range []string{"a", "b", "c"} {
		if _, err := businesses.Of(context.Background(), "tenant", "own", "10", id); err != nil {
			t.Fatal(err)
		}
	}
	if len(businesses.known) > 2 || businesses.Dropped() != 1 {
		t.Fatalf("remembered %d past a bound of 2, dropped %d", len(businesses.known), businesses.Dropped())
	}
	if got, err := businesses.Of(context.Background(), "tenant", "own", "10", "a"); err != nil || got != 2 || reader.reads != 4 {
		t.Fatalf("after the drop: %d, %v, reads %d; want 2 read again", got, err, reader.reads)
	}
}
