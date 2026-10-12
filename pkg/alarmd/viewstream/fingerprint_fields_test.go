// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package viewstream_test

import (
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream"
)

// decision-016 batch 4, section 11 item 1: the entry previews the record's
// timeline_record_revision, and that number is in the view digest. The
// placement round and the cutover stamp it onto a record without moving
// record_revision, so a desired set whose only change is that number is a
// change of the view it is in. The publisher must send it: a view left at
// the old number answers check 3 with timeline_unsaid or timeline_stale
// against a lease that brings the new one, and the Query Group is not
// executed until something else in the fleet's desired set moves, where
// section 2 bounds the view's lag by the renewal interval and the
// propagation delay.
func TestADesiredSetWhoseOnlyChangeIsATimelineRevisionIsPublished(t *testing.T) {
	before := desiredAt(publicationA, map[string]string{"qg-1": "w1"},
		map[string]viewstream.Content{"qg-1": content("obj-1", "s1")})
	after := desiredAt(publicationA, map[string]string{"qg-1": "w1"},
		map[string]viewstream.Content{"qg-1": content("obj-1", "s1")})
	stamped := after.Assignments["qg-1"]
	stamped.TimelineRecordRevision = 9
	after.Assignments["qg-1"] = stamped

	publisher, err := viewstream.NewPublisher(7, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := publisher.Publish(before); err != nil {
		t.Fatal(err)
	}
	published, err := publisher.Publish(after)
	if err != nil {
		t.Fatal(err)
	}
	if !published.Changed || published.Version.Revision != 2 || !reflect.DeepEqual(published.Affected, []string{"w1"}) {
		t.Fatalf("publishing the stamped record = %+v, want revision 2 with w1 affected", published)
	}
	view, ok := publisher.Snapshot("w1")
	if !ok || len(view.Entries) != 1 || view.Entries[0].Assignment.TimelineRecordRevision != 9 {
		t.Fatalf("w1 view after the stamp = %+v (ok=%t), want qg-1 previewing timeline revision 9", view.Entries, ok)
	}
}

// The fingerprint is what the publisher skips a round on, and every field
// of an Assignment is in the view a Worker installs, so every one of them
// must move it. Driven by the type rather than by a list, so a field added
// to the record's preview later is covered without anyone remembering to
// add a row; a field of a kind this case cannot move fails it until taught.
func TestEveryAssignmentFieldMovesTheFingerprint(t *testing.T) {
	base := func() viewstream.Desired {
		return desiredAt(publicationA, map[string]string{"qg-1": "w1", "qg-2": "w2"},
			map[string]viewstream.Content{"qg-1": content("obj-1", "s1"), "qg-2": content("obj-2", "s2")})
	}
	fields := reflect.TypeOf(viewstream.Assignment{})
	for index := 0; index < fields.NumField(); index++ {
		field := fields.Field(index)
		mutated := base()
		assignment := mutated.Assignments["qg-1"]
		value := reflect.ValueOf(&assignment).Elem().Field(index)
		switch value.Kind() {
		case reflect.String:
			value.SetString(value.String() + "-moved")
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			value.SetUint(value.Uint() + 1)
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			value.SetInt(value.Int() + 1)
		default:
			t.Fatalf("Assignment.%s is a %s, which this case does not know how to move", field.Name, value.Kind())
		}
		mutated.Assignments["qg-1"] = assignment
		before, after := base(), mutated
		workers := append(before.Workers(), after.Workers()...)
		movedProjection := false
		for _, worker := range workers {
			was, err := before.Project(worker)
			if err != nil {
				t.Fatal(err)
			}
			is, err := after.Project(worker)
			if err != nil {
				t.Fatal(err)
			}
			if was.Version.Digest != is.Version.Digest {
				movedProjection = true
			}
		}
		if !movedProjection {
			t.Fatalf("Assignment.%s: moving it moved no projection; the case proves nothing", field.Name)
		}
		if before.Fingerprint() == after.Fingerprint() {
			t.Errorf("Assignment.%s: a projection moved and the fingerprint did not; the publisher would skip a real change", field.Name)
		}
	}
}
