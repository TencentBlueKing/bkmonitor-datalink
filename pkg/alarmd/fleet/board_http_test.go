// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
)

func (rig *diagnosisRig) board(t *testing.T, query string) (int, BoardResponse, map[string]any) {
	t.Helper()
	w := httptest.NewRecorder()
	rig.handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/diagnose?"+query, nil))
	var body BoardResponse
	var raw map[string]any
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
	}
	_ = json.Unmarshal(w.Body.Bytes(), &raw)
	return w.Code, body, raw
}

// The board and the first-screen count are one computation: asked together
// while fresh they read the universe once, and the board's state facet is
// the count by verdict, strategy for strategy.
func TestTheBoardAndTheFirstScreenCountAreOneComputation(t *testing.T) {
	rig := newDiagnosisRig(t, diagnosisFacts(), nil)
	rig.universe = []string{"4101", "4102", "4103", "4109", "5001", "5002"}
	code, board, _ := rig.board(t, "board=1")
	if code != http.StatusOK {
		t.Fatalf("board status %d", code)
	}
	_, summary := rig.summary(t, "summary=1")
	if rig.universeReads != 1 {
		t.Fatalf("universe read %d times for a board and a count within the freshness, want once", rig.universeReads)
	}
	if board.Universe != summary.Summary.Strategies || board.Universe != 6 {
		t.Fatalf("board universe %d, summary %d; want six", board.Universe, summary.Summary.Strategies)
	}
	for _, word := range DiagnosisVerdicts() {
		if board.Facets.State[word] != summary.Summary.ByVerdict[word] {
			t.Errorf("%s: board %d, summary %d", word, board.Facets.State[word], summary.Summary.ByVerdict[word])
		}
	}
	rig.clock = rig.clock.Add(DiagnosisSummaryFreshFor)
	if code, _, _ := rig.board(t, "board=1"); code != http.StatusOK || rig.universeReads != 2 {
		t.Fatalf("after the freshness: status %d, %d reads; want a new read", code, rig.universeReads)
	}
}

// A board page continues its computation; once the computation is replaced
// the old cursor is answered with the new computation's first page, marked.
func TestABoardCursorFromAnEarlierComputationIsReread(t *testing.T) {
	rig := newDiagnosisRig(t, diagnosisFacts(), nil)
	for i := 1; i <= 5; i++ {
		rig.universe = append(rig.universe, strconv.Itoa(5000+i))
	}
	_, first, _ := rig.board(t, "board=1&limit=2")
	if len(first.Rows) != 2 || first.NextCursor == "" {
		t.Fatalf("first page %+v", first)
	}
	_, second, _ := rig.board(t, "board=1&limit=2&cursor="+url.QueryEscape(first.NextCursor))
	if second.Reread || len(second.Rows) != 2 || second.Rows[0].StrategyID == first.Rows[0].StrategyID {
		t.Fatalf("second page %+v", second)
	}
	rig.clock = rig.clock.Add(DiagnosisSummaryFreshFor)
	_, reread, _ := rig.board(t, "board=1&limit=2&cursor="+url.QueryEscape(first.NextCursor))
	if !reread.Reread || len(reread.Rows) != 2 || reread.Rows[0].StrategyID != first.Rows[0].StrategyID {
		t.Fatalf("after a new computation: %+v, want its first page marked reread", reread)
	}
}

// The board's selection is closed: an unknown word, a bad size or board mixed
// with the count is refused; an unreadable universe is said by name.
func TestTheBoardRefusesWhatItCannotReadAndNamesAnUnreadableUniverse(t *testing.T) {
	rig := newDiagnosisRig(t, diagnosisFacts(), nil)
	rig.universe = []string{"4101"}
	for _, query := range []string{"board=true", "board=1&summary=1", "board=1&state=NOPE", "board=1&punctuality=soon",
		"board=1&capability=maybe", "board=1&action=X", "board=1&limit=0", "board=1&limit=" + strconv.Itoa(BoardMaxRows+1)} {
		if code, _, _ := rig.board(t, query); code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want refused", query, code)
		}
	}
	rig.universeErr = errors.New("SOURCE_UNREADABLE")
	code, _, raw := rig.board(t, "board=1")
	if code != http.StatusOK || raw["error"] != "DIAGNOSIS_UNREADABLE" || raw["reason"] != "SOURCE_UNREADABLE" {
		t.Fatalf("status %d body %v, want the universe named unreadable", code, raw)
	}
}
