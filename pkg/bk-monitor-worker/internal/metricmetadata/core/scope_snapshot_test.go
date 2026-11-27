// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package core

import (
	"bytes"
	"reflect"
	"testing"
)

func TestIDEncodingAndCorruption(t *testing.T) {
	ids := []uint32{1, 2, 256, 4294967295}
	expected := []byte{1, 0, 0, 0, 2, 0, 0, 0, 0, 1, 0, 0, 255, 255, 255, 255}
	b, err := EncodeIDs(ids)
	if err != nil || !bytes.Equal(b, expected) {
		t.Fatalf("%x %v", b, err)
	}
	decoded, err := DecodeIDs(expected, 4)
	if err != nil || !reflect.DeepEqual(ids, decoded) {
		t.Fatalf("%v %v", decoded, err)
	}
	for _, bad := range [][]uint32{{0}, {2, 1}, {1, 1}} {
		if _, err := EncodeIDs(bad); err == nil {
			t.Fatalf("accepted %v", bad)
		}
	}
	for _, bad := range []struct {
		b     []byte
		count int
	}{{[]byte{1}, 1}, {expected, 3}, {[]byte{0, 0, 0, 0}, 1}, {[]byte{2, 0, 0, 0, 1, 0, 0, 0}, 2}} {
		if _, err := DecodeIDs(bad.b, bad.count); err == nil {
			t.Fatalf("accepted %x", bad.b)
		}
	}
	if b, err := EncodeIDs(nil); err != nil || b == nil {
		t.Fatal("empty list must be a non-null BLOB")
	}
}

func TestReplacementAndRepeatedKeys(t *testing.T) {
	before := ScopeState{Metrics: map[string]MetricState{"cpu": {ValueID: 1}, "memory": {ValueID: 2}}}
	after := before.Clone()
	after.Metrics["memory"] = MetricState{ValueID: 3}
	if before.Metrics["memory"].ValueID != 2 {
		t.Fatal("clone mutated committed snapshot")
	}
	if d := ReferenceDelta(before, after); !reflect.DeepEqual(d, map[uint32]int{2: -1, 3: 1}) {
		t.Fatal(d)
	}
	if len(before.Metrics) != len(after.Metrics) {
		t.Fatal("replacement changed count")
	}
	r := Record{GroupID: 1, ScopeHash: "scope", FieldName: "cpu", Fingerprint: "A"}
	last := r
	last.Fingerprint = "B"
	other := r
	other.GroupID = 2
	got := KeepLast([]Record{r, other, last})
	if len(got) != 2 || got[0].GroupID != 2 || got[1].Fingerprint != "B" {
		t.Fatal(got)
	}
}
