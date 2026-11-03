// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package roles

import (
	"reflect"
	"strings"
	"testing"
)

func TestResolveDefaultAndExplicitSelections(t *testing.T) {
	for _, test := range []struct {
		selected Set
		want     Set
	}{
		{nil, Set{Control, Worker, Channel}},
		{Set{Channel}, Set{Channel}},
		{Set{Channel, Control}, Set{Channel, Control}},
		{Set{Worker, Channel, Control}, Set{Worker, Channel, Control}},
	} {
		got, err := Resolve(test.selected)
		if err != nil || !reflect.DeepEqual(got, test.want) {
			t.Fatalf("Resolve(%v) = %v, %v; want %v", test.selected, got, err, test.want)
		}
		for _, role := range registry {
			if got.Has(role) != test.want.Has(role) {
				t.Fatalf("%v.Has(%q) differs from requested roles", got, role)
			}
		}
		got[0] = "changed"
		if test.selected != nil && !reflect.DeepEqual(test.selected, test.want) {
			t.Fatal("resolved roles share the caller's backing slice")
		}
	}
	got, _ := Resolve(nil)
	if got.String() != "control,worker,channel" {
		t.Fatalf("default registry changed through a returned set: %s", got)
	}
}

func TestResolveRejectsInvalidSelectionsByName(t *testing.T) {
	for _, test := range []struct {
		selected Set
		message  string
	}{
		{Set{}, "at least one"},
		{Set{"executor"}, `unknown role "executor"`},
		{Set{" control"}, `unknown role " control"`},
		{Set{Channel, Channel}, `duplicate role "channel"`},
		{Set{Control, ""}, `unknown role ""`},
	} {
		if _, err := Resolve(test.selected); err == nil || !strings.Contains(err.Error(), test.message) {
			t.Fatalf("Resolve(%v) error = %v, want %q", test.selected, err, test.message)
		}
	}
}

func TestParseCLISelections(t *testing.T) {
	got, err := Parse(" channel, worker ")
	if err != nil || !reflect.DeepEqual(got, Set{Channel, Worker}) {
		t.Fatalf("Parse() = %v, %v", got, err)
	}
	for _, value := range []string{"", " ", "worker,", ",control", "channel,,control", "worker, worker", "unknown"} {
		if _, err := Parse(value); err == nil || !strings.Contains(err.Error(), "roles") {
			t.Fatalf("Parse(%q) error = %v, want named rejection", value, err)
		}
	}
}
