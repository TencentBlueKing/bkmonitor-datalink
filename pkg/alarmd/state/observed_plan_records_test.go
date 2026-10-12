// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package state

import (
	"errors"
	"testing"
)

// Each observer reader refuses a record of another schema rather than reading
// zeros out of it.
func TestAnObserverRefusesAnotherSchema(t *testing.T) {
	other := []byte(`{"schema":"alarmd-something-else","marker_revision":9}`)
	for name, read := range map[string]func([]byte) error{
		"gap marker":     func(raw []byte) error { _, _, err := ObservedGapMarker(raw); return err },
		"per-group head": func(raw []byte) error { _, _, err := ObservedNoDataHeader(raw); return err },
	} {
		if err := read(other); !errors.Is(err, ErrObservedRecordInvalid) {
			t.Fatalf("%s of another schema: %v, want refused", name, err)
		}
		if err := read([]byte("not json")); !errors.Is(err, ErrObservedRecordInvalid) {
			t.Fatalf("%s of bytes that are not a record: %v, want refused", name, err)
		}
	}
}
