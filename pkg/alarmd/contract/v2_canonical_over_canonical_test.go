// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package contract

import "testing"

func TestDeriveCanonicalDigestV2OverCanonicalAgreesWithValueDigest(t *testing.T) {
	value := map[string]any{"b": []any{1, "x", nil}, "a": map[string]any{"z": 1.5, "y": "é"}}
	canonical, err := CanonicalJSONV2(value)
	if err != nil {
		t.Fatal(err)
	}
	fromValue, err := DeriveCanonicalDigestV2("alarmd-test-v1", value)
	if err != nil {
		t.Fatal(err)
	}
	fromBytes, err := DeriveCanonicalDigestV2OverCanonical("alarmd-test-v1", canonical)
	if err != nil {
		t.Fatal(err)
	}
	if fromValue != fromBytes {
		t.Fatalf("digest over canonical bytes %s differs from digest over value %s", fromBytes, fromValue)
	}
	tampered := append([]byte(nil), canonical...)
	tampered[len(tampered)-2] ^= 0x01
	fromTampered, err := DeriveCanonicalDigestV2OverCanonical("alarmd-test-v1", tampered)
	if err != nil {
		t.Fatal(err)
	}
	if fromTampered == fromValue {
		t.Fatal("a changed byte must change the digest")
	}
	if _, err := DeriveCanonicalDigestV2OverCanonical("", canonical); err == nil {
		t.Fatal("an empty domain must be refused")
	}
}
