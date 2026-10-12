// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"encoding/json"
	"math/rand"
	"reflect"
	"runtime"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// marshalledRetainedSize is the size as it was measured before: the whole
// document marshalled, and its length taken.
func marshalledRetainedSize(bindings []execution.NamedInputBinding, delivery execution.SeriesDelivery) (uint64, error) {
	compact := make([]execution.NamedInputBinding, len(bindings))
	copy(compact, bindings)
	for index := range compact {
		compact[index].Dataset = nil
		compact[index].View = nil
	}
	encoded, err := json.Marshal(struct {
		Bindings []execution.NamedInputBinding
		Delivery execution.SeriesDelivery
	}{Bindings: compact, Delivery: delivery})
	if err != nil {
		return 0, err
	}
	return uint64(len(encoded)) + delivery.Bytes, nil
}

// retainedSizeTexts are the strings whose encoding differs between the
// settings an encoder can have: HTML characters Marshal escapes, the line
// separators it escapes too, quotes and controls, and text outside ASCII.
var retainedSizeTexts = []string{"", "plain", "<tag>&amp;", "a>b", "业务", "line sep", "para sep", `quo"te`, "tab\there", "\x01ctl", "é"}

// fillRetained sets every field reachable from value to something drawn from
// random: strings from the texts above, numbers, slices and maps of up to two
// entries, pointers allocated or left nil. A raw JSON field gets a JSON
// string, so the document can always be encoded.
func fillRetained(random *rand.Rand, value reflect.Value, depth int) {
	if depth > 5 {
		return
	}
	if value.Type() == reflect.TypeOf(json.RawMessage(nil)) {
		encoded, _ := json.Marshal(retainedSizeTexts[random.Intn(len(retainedSizeTexts))])
		value.Set(reflect.ValueOf(json.RawMessage(encoded)))
		return
	}
	switch value.Kind() {
	case reflect.String:
		value.SetString(retainedSizeTexts[random.Intn(len(retainedSizeTexts))])
	case reflect.Bool:
		value.SetBool(random.Intn(2) == 0)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		value.SetInt(int64(random.Intn(1 << 20)))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		value.SetUint(uint64(random.Intn(1 << 20)))
	case reflect.Float32, reflect.Float64:
		value.SetFloat(random.Float64() * 1000)
	case reflect.Pointer:
		if random.Intn(2) == 0 {
			return
		}
		value.Set(reflect.New(value.Type().Elem()))
		fillRetained(random, value.Elem(), depth+1)
	case reflect.Slice:
		count := random.Intn(3)
		value.Set(reflect.MakeSlice(value.Type(), count, count))
		for index := 0; index < count; index++ {
			fillRetained(random, value.Index(index), depth+1)
		}
	case reflect.Map:
		count := random.Intn(3)
		value.Set(reflect.MakeMapWithSize(value.Type(), count))
		for index := 0; index < count; index++ {
			key, element := reflect.New(value.Type().Key()).Elem(), reflect.New(value.Type().Elem()).Elem()
			fillRetained(random, key, depth+1)
			fillRetained(random, element, depth+1)
			value.SetMapIndex(key, element)
		}
	case reflect.Struct:
		for index := 0; index < value.NumField(); index++ {
			if value.Type().Field(index).IsExported() {
				fillRetained(random, value.Field(index), depth+1)
			}
		}
	}
}

// The size counted as it is encoded is the size the marshalled document had,
// for every binding and delivery a random corpus draws: the texts the
// encoder escapes, datasets and views set or not, empty and absent
// collections, no bindings at all. It is also what the encoder refuses,
// where the document was refused.
func TestTheRetainedSizeIsTheMarshalledDocumentsSize(t *testing.T) {
	random := rand.New(rand.NewSource(20260928))
	checked, refused := 0, 0
	for index := 0; index < 3000; index++ {
		bindings := make([]execution.NamedInputBinding, random.Intn(4))
		for position := range bindings {
			fillRetained(random, reflect.ValueOf(&bindings[position]).Elem(), 0)
		}
		var delivery execution.SeriesDelivery
		fillRetained(random, reflect.ValueOf(&delivery).Elem(), 0)
		want, wantErr := marshalledRetainedSize(bindings, delivery)
		got, err := streamedRetainedSize(bindings, delivery)
		if (err == nil) != (wantErr == nil) {
			t.Fatalf("case %d: counted error %v, marshalled error %v", index, err, wantErr)
		}
		if err != nil {
			refused++
			continue
		}
		if got != want {
			t.Fatalf("case %d: counted size %d, marshalled size %d", index, got, want)
		}
		checked++
	}
	if checked < 2500 {
		t.Fatalf("only %d of 3000 cases could be encoded (%d refused); the corpus does not exercise the size", checked, refused)
	}
	if got, err := streamedRetainedSize(nil, execution.SeriesDelivery{}); err != nil || got != mustMarshalledSize(t, nil, execution.SeriesDelivery{}) {
		t.Fatalf("no bindings: %d, %v", got, err)
	}
}

func mustMarshalledSize(t *testing.T, bindings []execution.NamedInputBinding, delivery execution.SeriesDelivery) uint64 {
	t.Helper()
	size, err := marshalledRetainedSize(bindings, delivery)
	if err != nil {
		t.Fatal(err)
	}
	return size
}

// allocatedBytesPerCall is what call allocates on average, in bytes.
func allocatedBytesPerCall(call func()) uint64 {
	const calls = 2000
	call()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range calls {
		call()
	}
	runtime.ReadMemStats(&after)
	return (after.TotalAlloc - before.TotalAlloc) / calls
}

// Counting keeps nothing of the document: it allocates at least the
// document's length less than marshalling did, which held all of it. The
// number of allocations is the same -- the encoder is one object where the
// document was one -- so it is the bytes that are held to it.
func TestTheRetainedSizeDoesNotKeepTheDocument(t *testing.T) {
	if raceEnabled {
		// Both arms encode through the JSON encoder's pooled buffer. Under the
		// race detector the pool drops a quarter of what is put back, calls
		// grow a new buffer at random, and 14 runs in 150 read a difference
		// below the document's length; plain runs never do.
		t.Skip("allocation is not measurable through a pooled buffer under the race detector")
	}
	random := rand.New(rand.NewSource(7))
	bindings := make([]execution.NamedInputBinding, 3)
	for position := range bindings {
		fillRetained(random, reflect.ValueOf(&bindings[position]).Elem(), 0)
	}
	delivery := execution.SeriesDelivery{PhysicalQuery: "q", QueryRevision: "r", Series: 3, Records: 60, Bytes: 4096, Digest: "d"}
	size, err := streamedRetainedSize(bindings, delivery)
	if err != nil {
		t.Fatalf("the drawn bindings cannot be encoded: %v", err)
	}
	document := size - delivery.Bytes
	counted := allocatedBytesPerCall(func() { _, _ = streamedRetainedSize(bindings, delivery) })
	marshalled := allocatedBytesPerCall(func() { _, _ = marshalledRetainedSize(bindings, delivery) })
	// The encoder itself is the allowance: under a hundred bytes.
	if counted+128 > marshalled || marshalled-counted+128 < document {
		t.Fatalf("counting allocates %d bytes per call, marshalling %d, for a %d-byte document: the document is still being kept", counted, marshalled, document)
	}
}

func BenchmarkRetainedSize(b *testing.B) {
	random := rand.New(rand.NewSource(7))
	bindings := make([]execution.NamedInputBinding, 3)
	for position := range bindings {
		fillRetained(random, reflect.ValueOf(&bindings[position]).Elem(), 0)
	}
	delivery := execution.SeriesDelivery{PhysicalQuery: "q", QueryRevision: "r", Series: 3, Records: 60, Bytes: 4096, Digest: "d"}
	for _, arm := range []struct {
		name    string
		measure func([]execution.NamedInputBinding, execution.SeriesDelivery) (uint64, error)
	}{{"marshalled", marshalledRetainedSize}, {"counted", streamedRetainedSize}} {
		b.Run(arm.name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if _, err := arm.measure(bindings, delivery); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
