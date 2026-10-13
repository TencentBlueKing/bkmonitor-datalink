// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package obchannel

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/pod"
)

// Receipts are read newest first and selected by time, namespace, object
// (a Pod's name matches its pod/container receipts), and request; pages
// carry the next offset while there is one, and the counts say how many
// matched and how many are kept.
func TestSREReceiptsAreSelectedAndPaged(t *testing.T) {
	client := redistest.Start(t).Client()
	at := time.Unix(1_700_000_000, 0).UTC()
	receipts := NewSREReceipts(client, "test.sre:{env}:receipts", func() time.Time { return at }, nil)
	for index, action := range []SREAction{
		{Op: "pod.exec", Namespace: "a", Name: "web-0/web", RequestID: "r1"},
		{Op: "pod.delete", Namespace: "a", Name: "web-0", RequestID: "r2"},
		{Op: "workload.scale", Namespace: "b", Name: "web", RequestID: "r3"},
		{Op: "pod.exec", Namespace: "a", Name: "web-10/web", RequestID: "r4"},
	} {
		action.At = at.Add(time.Duration(index) * time.Minute)
		receipts.Record(action)
	}
	list := func(filter SREReceiptFilter) (ids []string, page SREReceiptPage) {
		t.Helper()
		page, err := receipts.List(context.Background(), filter)
		if err != nil {
			t.Fatal(err)
		}
		for _, receipt := range page.Receipts {
			ids = append(ids, receipt.RequestID)
		}
		return ids, page
	}
	for _, c := range []struct {
		filter SREReceiptFilter
		want   string
	}{
		{SREReceiptFilter{}, "[r4 r3 r2 r1]"},
		{SREReceiptFilter{Namespace: "a", Name: "web-0"}, "[r2 r1]"},
		{SREReceiptFilter{RequestID: "r3"}, "[r3]"},
		{SREReceiptFilter{Since: at.Add(time.Minute), Until: at.Add(3 * time.Minute)}, "[r3 r2]"},
	} {
		if ids, page := list(c.filter); fmt.Sprint(ids) != c.want || page.Stored != 4 || page.Matched != len(ids) || page.NextOffset != 0 {
			t.Fatalf("filter %+v: %v %+v, want %s", c.filter, ids, page, c.want)
		}
	}
	ids, page := list(SREReceiptFilter{Limit: 3})
	if fmt.Sprint(ids) != "[r4 r3 r2]" || page.NextOffset != 3 || page.Matched != 4 {
		t.Fatalf("first page %v %+v", ids, page)
	}
	if ids, page = list(SREReceiptFilter{Limit: 3, Offset: 3}); fmt.Sprint(ids) != "[r1]" || page.NextOffset != 0 {
		t.Fatalf("second page %v %+v", ids, page)
	}
	if ttl := client.TTL(context.Background(), "test.sre:{env}:receipts").Val(); ttl <= SREReceiptRetention-time.Minute || ttl > SREReceiptRetention {
		t.Fatalf("ttl %v, want the retention", ttl)
	}
}

// Past the bound the oldest receipts go.
func TestSREReceiptsKeepTheNewest(t *testing.T) {
	client := redistest.Start(t).Client()
	receipts := NewSREReceipts(client, "test.sre:{env}:receipts", nil, nil)
	for index := 0; index < MaxSREReceipts+5; index++ {
		receipts.Record(SREAction{Op: "pod.delete", RequestID: fmt.Sprint(index), At: time.Unix(int64(1_700_000_000+index), 0)})
	}
	page, err := receipts.List(context.Background(), SREReceiptFilter{Limit: 1})
	if err != nil || page.Stored != MaxSREReceipts || page.Receipts[0].RequestID != fmt.Sprint(MaxSREReceipts+4) {
		t.Fatalf("%+v %v, want %d kept, the newest first", page.Stored, err, MaxSREReceipts)
	}
	if page, _ := receipts.List(context.Background(), SREReceiptFilter{RequestID: "4"}); page.Matched != 0 {
		t.Fatal("the oldest receipts were kept past the bound")
	}
	if kept := client.LLen(context.Background(), "test.sre:{env}:receipts").Val(); kept != MaxSREReceipts {
		t.Fatalf("the store holds %d receipts, want %d", kept, MaxSREReceipts)
	}
}

// A write the store refuses is counted as such, and does not hold the
// action up for longer than its bound.
func TestASREReceiptWriteThatFailsIsCounted(t *testing.T) {
	client := redistest.Start(t).Client()
	var outcomes []bool
	receipts := NewSREReceipts(client, "test.sre:{env}:receipts", nil, func(ok bool) { outcomes = append(outcomes, ok) })
	receipts.Record(SREAction{Op: "pod.delete"})
	_ = client.Close()
	started := time.Now()
	receipts.Record(SREAction{Op: "pod.delete"})
	if fmt.Sprint(outcomes) != "[true false]" || time.Since(started) > 2*sreReceiptWriteTimeout {
		t.Fatalf("outcomes %v after %v", outcomes, time.Since(started))
	}
}

// Through the channel: an execution's receipt is read back by its request
// with its run's facts - how it was supervised and ended - and without its
// stdin; with no store the operation says why it is unavailable.
func TestSREReceiptsReadAnExecutionBack(t *testing.T) {
	client := redistest.Start(t).Client()
	receipts := NewSREReceipts(client, "test.sre:{env}:receipts", nil, nil)
	controller := true
	podClient := &postObservationClient{target: corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "fixture", Name: "web-0", UID: "pod-uid", Labels: map[string]string{"app": "web"}, OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "StatefulSet", Name: "web", UID: "workload-uid", Controller: &controller}}},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "web", Image: "web:v1"}}},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{{Name: "web", ContainerID: "containerd://fixture", ImageID: "sha256:fixture", Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}}},
	}}
	provider, err := pod.New(pod.Options{Client: podClient, Manager: &auditManager{}, Executor: &postObservationExecutor{}, DefaultNamespace: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	channel, err := New(Options{Auth: postObservationAuth{}, EnvironmentID: "test", Replica: "replica-1", Operations: SREOperations(SREOptions{Pods: provider, Receipts: receipts, Audit: receipts.Record})})
	if err != nil {
		t.Fatal(err)
	}
	target := map[string]any{"namespace": "fixture", "pod": "web-0", "uid": "pod-uid", "container": "web", "container_id": "containerd://fixture", "image_id": "sha256:fixture"}
	_, response := call(t, channel, envelope(channel, "invoke", "pod.exec", Params{"target": target, "argv": []any{"/bin/sh"}, "stdin": "secret-script-text"}))
	if response.Error != nil {
		t.Fatalf("exec: %+v", response.Error)
	}
	status, read := call(t, channel, envelope(channel, "invoke", OpSREReceipts, Params{"request_id": response.Meta.RequestID}))
	encoded, _ := json.Marshal(read.Result)
	var page SREReceiptPage
	if err := json.Unmarshal(encoded, &page); status != 200 || err != nil || page.Matched != 1 {
		t.Fatalf("read: HTTP %d %s %v", status, encoded, err)
	}
	exec := page.Receipts[0]
	if exec.Op != "pod.exec" || exec.Exec == nil || exec.Exec.RemoteState != pod.RemoteCompleted || exec.Exec.Supervision != pod.SupervisionShGroup ||
		exec.Exec.ExitCode == nil || *exec.Exec.ExitCode != 19 || exec.At.IsZero() || strings.Contains(string(encoded), "secret-script-text") {
		t.Fatalf("receipt %s", encoded)
	}
	if _, read := call(t, channel, envelope(channel, "invoke", OpSREReceipts, Params{"since": "yesterday"})); read.Error == nil || read.Error.Code != "sre_receipts_invalid_time" {
		t.Fatalf("an invalid time: %+v", read.Error)
	}
	for _, op := range SREOperations(SREOptions{ReceiptsReason: "receipts_unavailable: fixture"}) {
		if op.ID == OpSREReceipts {
			if availability := op.Availability(); availability.Available || availability.Reason != "receipts_unavailable: fixture" {
				t.Fatalf("availability %+v", availability)
			}
			return
		}
	}
	t.Fatal("sre.receipts is missing")
}
