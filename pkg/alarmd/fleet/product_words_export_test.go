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
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"testing"
)

var updateProductWords = flag.Bool("update-product-words", false, "rewrite product_words.json from ProductWords()")

// productWordsFile is the page vocabulary as a file a deployment pins beside
// its own documentation: the words a page shows for a strategy's state, who
// acts on it, why it is a wait, how sure a duration is, and the deployment's
// health. Each key there needs an entry in the deployment's knowledge base;
// the file is how that repository knows the keys without reading this code.
const productWordsFile = "product_words.json"

// productWordsExport is the part of ProductWords a page shows. The window,
// hole and coverage words belong to the developer's view and stay out.
type productWordsExport struct {
	State        map[StateWord]string   `json:"state"`
	StateOrder   []StateWord            `json:"state_order"`
	Action       map[ActionWord]string  `json:"action"`
	ActionOrder  []ActionWord           `json:"action_order"`
	Watch        map[WatchReason]string `json:"watch"`
	SinceBasis   map[SinceBasis]string  `json:"since_basis"`
	Health       map[Health]string      `json:"health"`
	Urgency      map[Urgency]string     `json:"urgency"`
	UrgencyOrder []Urgency              `json:"urgency_order"`
}

func productWordsExportBytes(t *testing.T) []byte {
	t.Helper()
	words := ProductWords()
	encoded, err := json.MarshalIndent(productWordsExport{
		State: words.State, StateOrder: words.StateOrder, Action: words.Action, ActionOrder: words.ActionOrder,
		Watch: words.Watch, SinceBasis: words.SinceBasis, Health: words.Health,
		Urgency: words.Urgency, UrgencyOrder: words.UrgencyOrder,
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(encoded, '\n')
}

// The exported file is the code's vocabulary, word for word: a word added,
// renamed or reworded here without regenerating the file fails, so a pinned
// copy elsewhere can never lag the build it was taken from.
func TestProductWordsFileMatchesTheCode(t *testing.T) {
	want := productWordsExportBytes(t)
	if *updateProductWords {
		if err := os.WriteFile(productWordsFile, want, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(productWordsFile)
	if err != nil {
		t.Fatalf("read %s: %v (regenerate: go test ./fleet -run TestProductWordsFileMatchesTheCode -update-product-words)", productWordsFile, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s differs from ProductWords(); regenerate: go test ./fleet -run TestProductWordsFileMatchesTheCode -update-product-words\n--- file\n%s\n--- code\n%s", productWordsFile, got, want)
	}
}

// Every word a page can show is in the file with a rendering: the closed
// lists and the one word a diagnosis adds.
func TestProductWordsFileCoversEveryPageWord(t *testing.T) {
	var file productWordsExport
	raw, err := os.ReadFile(productWordsFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	for _, state := range append(append([]StateWord(nil), StateWords...), DiagnosisUnknown) {
		if file.State[state] == "" {
			t.Errorf("state %s has no word in %s", state, productWordsFile)
		}
	}
	for _, action := range ActionWords {
		if file.Action[action] == "" {
			t.Errorf("action %s has no word in %s", action, productWordsFile)
		}
	}
	for _, reason := range WatchReasons {
		if file.Watch[reason] == "" {
			t.Errorf("watch reason %s has no word in %s", reason, productWordsFile)
		}
	}
	for _, basis := range []SinceBasis{SinceExact, SinceAtLeast, SinceAtMost, SinceRefused} {
		if file.SinceBasis[basis] == "" {
			t.Errorf("since basis %s has no word in %s", basis, productWordsFile)
		}
	}
}
