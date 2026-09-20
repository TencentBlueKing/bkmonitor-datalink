// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package contract

import (
	"encoding/json"
	"testing"
)

// The whole-item no-data group's identity, as Python wrote it.
//
// This value was not computed from this implementation. It was read out of the
// running deployment's Redis, where the Python no-data checker had written it
// for a strategy with no history and no data - the dims_md5 of
// {"__NO_DATA_DIMENSION__": True}. A digest a port checks against its own
// output agrees with itself whatever it got wrong; only a value the other
// implementation produced can say the two agree.
const pythonWholeItemNoDataMD5 = "3e06a0b6d0560271cafee9f08a6da2d7"

func TestPythonObjectMD5MatchesTheIdentityPythonWrote(t *testing.T) {
	digest, err := PythonObjectMD5(map[string]json.RawMessage{
		NoDataDimensionTag: json.RawMessage(`true`),
	})
	if err != nil {
		t.Fatalf("PythonObjectMD5() error = %v", err)
	}
	if digest != pythonWholeItemNoDataMD5 {
		t.Fatalf("PythonObjectMD5() = %s, want %s: this is the identity Python wrote for the same "+
			"dimensions, so a difference is a difference in the hash, not in the value", digest, pythonWholeItemNoDataMD5)
	}
}

// count_md5 hashes str(value), so the encodings Python's str() flattens
// together arrive here already flattened. A caller holding a dimension value as
// text does not have to reconstruct the type it had in Python to agree with it.
func TestPythonObjectMD5FlattensWhatPythonStrFlattens(t *testing.T) {
	for name, pair := range map[string][2]json.RawMessage{
		"boolean and its text": {json.RawMessage(`true`), json.RawMessage(`"True"`)},
		"false and its text":   {json.RawMessage(`false`), json.RawMessage(`"False"`)},
		"integer and its text": {json.RawMessage(`5`), json.RawMessage(`"5"`)},
		"null and its text":    {json.RawMessage(`null`), json.RawMessage(`"None"`)},
	} {
		t.Run(name, func(t *testing.T) {
			first, err := PythonObjectMD5(map[string]json.RawMessage{"d": pair[0]})
			if err != nil {
				t.Fatalf("PythonObjectMD5(%s) error = %v", pair[0], err)
			}
			second, err := PythonObjectMD5(map[string]json.RawMessage{"d": pair[1]})
			if err != nil {
				t.Fatalf("PythonObjectMD5(%s) error = %v", pair[1], err)
			}
			if first != second {
				t.Fatalf("%s and %s hash to %s and %s; count_md5 hashes str(value) and would agree",
					pair[0], pair[1], first, second)
			}
		})
	}
}

// The key order a caller happens to build the map in is not part of the
// identity: count_md5 sorts the keys before it hashes them.
func TestPythonObjectMD5IsIndependentOfKeyOrder(t *testing.T) {
	first, err := PythonObjectMD5(map[string]json.RawMessage{
		"a": json.RawMessage(`"1"`), "b": json.RawMessage(`"2"`), NoDataDimensionTag: json.RawMessage(`true`),
	})
	if err != nil {
		t.Fatalf("PythonObjectMD5() error = %v", err)
	}
	second, err := PythonObjectMD5(map[string]json.RawMessage{
		NoDataDimensionTag: json.RawMessage(`true`), "b": json.RawMessage(`"2"`), "a": json.RawMessage(`"1"`),
	})
	if err != nil {
		t.Fatalf("PythonObjectMD5() error = %v", err)
	}
	if first != second {
		t.Fatalf("key order changed the identity: %s and %s", first, second)
	}
}

// The tag is part of the hash. A no-data anomaly and a threshold anomaly on the
// same series are different objects, and this is where that stops being a
// statement and becomes an identity.
func TestPythonObjectMD5SeparatesANoDataGroupFromItsSeries(t *testing.T) {
	series := map[string]json.RawMessage{"bk_target_ip": json.RawMessage(`"10.0.0.1"`)}
	group := map[string]json.RawMessage{
		"bk_target_ip": json.RawMessage(`"10.0.0.1"`), NoDataDimensionTag: json.RawMessage(`true`),
	}
	withoutTag, err := PythonObjectMD5(series)
	if err != nil {
		t.Fatalf("PythonObjectMD5() error = %v", err)
	}
	withTag, err := PythonObjectMD5(group)
	if err != nil {
		t.Fatalf("PythonObjectMD5() error = %v", err)
	}
	if withoutTag == withTag {
		t.Fatal("the no-data tag left the identity unchanged; the group and the series would dedupe together")
	}
}

func TestPythonObjectMD5RejectsAFieldThatIsNotOneJSONValue(t *testing.T) {
	for name, raw := range map[string]json.RawMessage{
		"empty":         json.RawMessage(``),
		"two values":    json.RawMessage(`1 2`),
		"not JSON":      json.RawMessage(`{`),
		"trailing text": json.RawMessage(`"a" b`),
	} {
		t.Run(name, func(t *testing.T) {
			if digest, err := PythonObjectMD5(map[string]json.RawMessage{"d": raw}); err == nil {
				t.Fatalf("PythonObjectMD5() = %s, want an error", digest)
			}
		})
	}
}
