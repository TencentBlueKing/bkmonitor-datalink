// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package contract

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// keyOrderPool holds keys as written, each with the key it decodes to. Some
// are one key written two ways -- an escape and a literal -- which is the only
// way two members can compare equal; the rest differ in the ways a bytewise
// order has to get right: prefixes, the empty key, case, bytes above ASCII.
var keyOrderPool = []struct{ written, decoded string }{
	{`""`, ""},
	{`"a"`, "a"},
	{`"a"`, "a"},
	{`"ab"`, "ab"},
	{`"abc"`, "abc"},
	{`"A"`, "A"},
	{`"b"`, "b"},
	{`"é"`, "é"},
	{`"é"`, "é"},
	{`"业务"`, "业务"},
	{`"a b"`, "a b"},
	{`"tab\there"`, "tab\there"},
	{`"<tag>"`, "<tag>"},
	{`"z"`, "z"},
}

// objectWithSortSlice is the canonical object for members, ordered the way
// the stream ordered them before: sort.Slice over the decoded keys.
func objectWithSortSlice(keys []string, values []string) []byte {
	order := make([]int, len(keys))
	for index := range order {
		order[index] = index
	}
	sort.Slice(order, func(i, j int) bool { return keys[order[i]] < keys[order[j]] })
	out := []byte{'{'}
	for position, index := range order {
		if position > 0 {
			out = append(out, ',')
		}
		out = appendCanonicalStringV2(out, []byte(keys[index]))
		out = append(out, ':')
		out = append(out, values[index]...)
	}
	return append(out, '}')
}

// Objects of distinct keys, drawn in every order: the stream's canonical
// bytes are the bytes the sort.Slice ordering gave, and the established path
// agrees with both. Keys that compare equal are one key written twice, and
// every such object is refused whichever of the two the sort put first --
// by the stream, and by the established path under the same error. That is
// why the order among equal keys, the one thing a stable and an unstable
// sort may differ on, cannot reach an accepted result.
func TestCanonicalObjectKeysKeepTheirOrderAndRepeatsAreRefused(t *testing.T) {
	random := rand.New(rand.NewSource(20260928))
	distinct, repeated := 0, 0
	for iteration := 0; iteration < 3000; iteration++ {
		count := 1 + random.Intn(len(keyOrderPool))
		picks := random.Perm(len(keyOrderPool))[:count]
		members, keys, values := make([]string, count), make([]string, count), make([]string, count)
		seen, repeat := map[string]bool{}, false
		for position, pick := range picks {
			values[position] = []string{`1`, `"v"`, `null`, `[1,2]`, `{"n":true}`}[random.Intn(5)]
			members[position] = keyOrderPool[pick].written + ":" + values[position]
			keys[position] = keyOrderPool[pick].decoded
			repeat = repeat || seen[keys[position]]
			seen[keys[position]] = true
		}
		payload := "{" + strings.Join(members, ",") + "}"
		stream := &canonicalStream{src: []byte(payload)}
		out, ok := stream.value(nil, 0)
		if repeat {
			repeated++
			if ok {
				t.Fatalf("%s: a key written twice was accepted as %s", payload, out)
			}
		} else {
			distinct++
			if want := objectWithSortSlice(keys, values); !ok || !bytes.Equal(out, want) {
				t.Fatalf("%s: stream gave %s (ok %v), the sort.Slice order gives %s", payload, out, ok, want)
			}
		}
		canonicalStreamAgrees(t, json.RawMessage(payload))
	}
	if distinct < 500 || repeated < 500 {
		t.Fatalf("drew %d objects of distinct keys and %d with a repeat; both sides need to be exercised", distinct, repeated)
	}
}

// The sort allocates nothing: an object canonicalised a second time on the
// same stream reuses its level's buffers, and sort.Slice's swapper was the
// allocation left on that path.
func TestCanonicalObjectSortAllocatesNothing(t *testing.T) {
	payload := []byte(`{"z":1,"b":{"y":2,"a":[3]},"é":"v","a":null,"ab":true,"":0}`)
	stream := &canonicalStream{}
	dst := make([]byte, 0, 256)
	run := func() {
		stream.src, stream.pos = payload, 0
		var ok bool
		if dst, ok = stream.value(dst[:0], 0); !ok {
			t.Fatal("declined the payload")
		}
	}
	run()
	if allocations := testing.AllocsPerRun(200, run); allocations != 0 {
		t.Fatalf("canonicalising an object allocates %v times on a warm stream, want 0", allocations)
	}
}
