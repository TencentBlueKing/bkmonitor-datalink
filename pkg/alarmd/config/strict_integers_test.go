// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type strictInline struct {
	Inline int `yaml:"inline"`
}

type strictShape struct {
	strictInline `yaml:",inline"`
	Plain        int              `yaml:"plain"`
	Narrow       uint32           `yaml:"narrow"`
	Pointer      *int64           `yaml:"pointer"`
	Nested       struct{ N int8 } `yaml:"nested"`
	Listed       []int            `yaml:"listed"`
	Keyed        map[string]int   `yaml:"keyed"`
	Untagged     int
	Wait         Duration `yaml:"wait"`
	Ratio        float64  `yaml:"ratio"`
	Skipped      int      `yaml:"-"`
}

// Every integer key, whatever shape holds it, is a whole number written in
// decimal, bare or quoted; anything else is refused by the key's path. A
// type that reads itself (a Duration's "30s") and a float key are not
// integers and are left alone.
func TestEveryIntegerKeyIsAWholeDecimalOrRefusedByItsPath(t *testing.T) {
	refused := []string{"1.5", "1e3", "an hour", "true", "0x10", `"1.5"`, `""`}
	for _, test := range []struct {
		document, path string
	}{
		{"inline: %s", "inline"},
		{"plain: %s", "plain"},
		{"narrow: %s", "narrow"},
		{"pointer: %s", "pointer"},
		{"nested:\n  n: %s", "nested.n"},
		{"listed: [1, %s]", "listed[1]"},
		{"keyed:\n  a: %s", "keyed.a"},
		{"untagged: %s", "untagged"},
	} {
		for _, value := range refused {
			document := strings.Replace(test.document, "%s", value, 1)
			if _, err := strictCheck(t, document); err == nil || !strings.Contains(err.Error(), test.path+" ") {
				t.Errorf("%q: error = %v, want a refusal naming %s", document, err, test.path)
			}
		}
		for _, value := range []string{"7", `"7"`, `" 7 "`} {
			document := strings.Replace(test.document, "%s", value, 1)
			decoded, err := strictCheck(t, document)
			if err != nil {
				t.Errorf("%q: error = %v, want accepted", document, err)
				continue
			}
			if got := readBack(decoded, test.path); got != 7 {
				t.Errorf("%q: %s decoded as %d, want 7", document, test.path, got)
			}
		}
	}
	// A narrow key's range is its type's: one past it is refused by path.
	if _, err := strictCheck(t, "nested:\n  n: 128"); err == nil || !strings.Contains(err.Error(), "nested.n ") {
		t.Errorf("an int8 of 128: error = %v, want a refusal naming nested.n", err)
	}
	if _, err := strictCheck(t, "narrow: -1"); err == nil || !strings.Contains(err.Error(), "narrow ") {
		t.Errorf("a negative uint32: error = %v, want a refusal naming narrow", err)
	}
	// Not integers: left to their own readers.
	for _, document := range []string{"wait: 30s", "ratio: 1.5", "pointer:", "plain: null"} {
		if _, err := strictCheck(t, document); err != nil {
			t.Errorf("%q: error = %v, want accepted", document, err)
		}
	}
}

func strictCheck(t *testing.T, document string) (strictShape, error) {
	t.Helper()
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(document), &node); err != nil {
		t.Fatalf("fixture %q: %v", document, err)
	}
	var decoded strictShape
	if _, err := strictIntegers(&node, reflect.TypeOf(decoded), ""); err != nil {
		return decoded, err
	}
	encoded, err := yaml.Marshal(&node)
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("%q passed the check and then did not decode: %v", document, err)
	}
	return decoded, nil
}

func readBack(decoded strictShape, path string) int64 {
	switch path {
	case "inline":
		return int64(decoded.Inline)
	case "plain":
		return int64(decoded.Plain)
	case "narrow":
		return int64(decoded.Narrow)
	case "pointer":
		if decoded.Pointer == nil {
			return -1
		}
		return *decoded.Pointer
	case "nested.n":
		return int64(decoded.Nested.N)
	case "listed[1]":
		return int64(decoded.Listed[1])
	case "keyed.a":
		return int64(decoded.Keyed["a"])
	case "untagged":
		return int64(decoded.Untagged)
	}
	return -1
}

// Through Load, on a key of the real configuration: the runtime store's db
// written as a fraction is refused by its path, quoted is read, and the
// document's other keys decode as before.
func TestLoadHoldsTheRealConfigsIntegersToWholeDecimals(t *testing.T) {
	_, err := Load(writeConfig(t, strings.Replace(platformCacheConfigContents(""), "  db: 8\n", "  db: 8.5\n", 1)))
	if err == nil || !strings.Contains(err.Error(), "redis.db ") {
		t.Fatalf("Load() error = %v, want a refusal naming redis.db", err)
	}
	loaded, err := Load(writeConfig(t, strings.Replace(platformCacheConfigContents(""), "  db: 8\n", "  db: \"9\"\n", 1)))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.Redis.DB != 9 {
		t.Fatalf("redis.db = %d, want 9", loaded.Redis.DB)
	}
}
