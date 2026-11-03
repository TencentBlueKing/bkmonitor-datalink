// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package obchannel

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
)

const effectKey = "test.sre:{env}:receipts"

// No list can push another's receipts out: a burst of commands past the
// exec list's bound and a burst of reads past the access list's leave an
// image change and the first command where they were. Before, one list kept
// changes and node commands, and a session's node.exec runs filled it to its
// cap within six days, short of the retention.
func TestNoListCanEvictAnothersReceipts(t *testing.T) {
	client := redistest.Start(t).Client()
	receipts := NewSREReceipts(client, effectKey, nil, nil)
	at := time.Unix(1_700_000_000, 0).UTC()
	receipts.Record(SREAction{Op: OpSetImage, Namespace: "ns", Name: "web", RequestID: "image", At: at})
	receipts.Record(SREAction{Op: OpNodeExec, Kind: "Node", Name: "n1", RequestID: "first-command", At: at})
	for index := 0; index < MaxSREExecReceipts+50; index++ {
		receipts.Record(SREAction{Op: OpNodeEnded, Kind: "Node", Name: "n1", RequestID: fmt.Sprint("c", index), At: at.Add(time.Duration(index+1) * time.Second)})
	}
	for index := 0; index < MaxSREAccessReceipts+50; index++ {
		receipts.Record(SREAction{Op: OpNodeGet, Kind: "Node", Name: "n1", RequestID: fmt.Sprint("r", index), At: at.Add(time.Duration(index+1) * time.Second)})
	}
	page, err := receipts.List(context.Background(), SREReceiptFilter{RequestID: "image"})
	if err != nil || page.Matched != 1 || page.Receipts[0].Effect != SREEffectChange {
		t.Fatalf("%+v %v, want the image change kept", page, err)
	}
	if page.StoredByEffect[SREEffectExec] != MaxSREExecReceipts || page.StoredByEffect[SREEffectAccess] != MaxSREAccessReceipts ||
		page.StoredByEffect[SREEffectChange] != 1 || page.Stored != MaxSREExecReceipts+MaxSREAccessReceipts+1 {
		t.Fatalf("stored %d %v, want the exec and access lists at their bounds and the change kept", page.Stored, page.StoredByEffect)
	}
	// The exec list is at its bound from its own burst: the first command
	// went to it, not to the change list, and its own commands pushed it out.
	if first, _ := receipts.List(context.Background(), SREReceiptFilter{RequestID: "first-command"}); first.Matched != 0 {
		t.Fatalf("first command %+v, want it pushed out of the exec list by later commands", first.Receipts)
	}
}

// Each action goes to its own list: an action that may have changed state to
// the change list, a command to the exec list, a read to the access list;
// effect selects one.
func TestEachReceiptGoesToItsEffectsList(t *testing.T) {
	client := redistest.Start(t).Client()
	receipts := NewSREReceipts(client, effectKey, nil, nil)
	changes := []string{OpSetImage, OpRevert, "k8s.apply", "k8s.patch", "k8s.delete", "pod.delete", "workload.restart", "workload.scale", "configmap.edit_text"}
	commands := []string{"node.exec", "node.execution.cancel", "node.execution.ended", "pod.exec"}
	accesses := []string{"k8s.dry_run", "node.execution.get", "secret.get", "configmap.get", "k8s.list"}
	at := time.Unix(1_700_000_000, 0).UTC()
	all := append(append(append([]string{}, changes...), commands...), accesses...)
	for index, op := range all {
		receipts.Record(SREAction{Op: op, RequestID: op, Result: SREResultDone, At: at.Add(time.Duration(index) * time.Second)})
	}
	for effect, want := range map[string][]string{SREEffectChange: changes, SREEffectExec: commands, SREEffectAccess: accesses} {
		page, err := receipts.List(context.Background(), SREReceiptFilter{Effect: effect})
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, receipt := range page.Receipts {
			if receipt.Effect != effect {
				t.Errorf("%s receipt marked %q", receipt.Op, receipt.Effect)
			}
			got = append([]string{receipt.Op}, got...)
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("effect %s read %v, want %v", effect, got, want)
		}
	}
	ctx := context.Background()
	for _, key := range []string{effectKey, effectKey + ":change", effectKey + ":exec"} {
		if ttl := client.TTL(ctx, key).Val(); ttl <= SREReceiptRetention-time.Minute || ttl > SREReceiptRetention {
			t.Errorf("%s ttl %v, want the retention", key, ttl)
		}
	}
	page, _ := receipts.List(ctx, SREReceiptFilter{})
	if page.Matched != len(all) || page.Receipts[0].Op != "k8s.list" || page.Receipts[len(page.Receipts)-1].Op != OpSetImage {
		t.Fatalf("all lists read %d, newest %s, want all merged newest first", page.Matched, page.Receipts[0].Op)
	}
}

// The change list keeps only what may have changed state: a dry run, and an
// attempt refused before any effect - by validation, by the apiserver's
// authorization, or because its target was replaced since it was read - is
// an access, whatever its operation. A failure stays a change, since a
// failing apiserver or transport does not say nothing was written, and so
// does an unknown end.
func TestAnAttemptThatCannotHaveChangedAnythingIsAnAccess(t *testing.T) {
	client := redistest.Start(t).Client()
	receipts := NewSREReceipts(client, effectKey, nil, nil)
	at := time.Unix(1_700_000_000, 0).UTC()
	cases := []struct {
		action SREAction
		want   string
	}{
		{SREAction{Op: OpSetImage, Result: SREResultRefused}, SREEffectAccess},
		{SREAction{Op: "k8s.apply", Result: SREResultForbidden}, SREEffectAccess},
		{SREAction{Op: "workload.restart", Result: SREResultTargetChanged}, SREEffectAccess},
		{SREAction{Op: "configmap.edit_text", Result: SREResultDone, DryRun: true}, SREEffectAccess},
		{SREAction{Op: OpNodeExec, Result: SREResultRefused}, SREEffectAccess},
		{SREAction{Op: OpSetImage, Result: SREResultFailed}, SREEffectChange},
		{SREAction{Op: "k8s.delete", Result: SREResultUnknown}, SREEffectChange},
		{SREAction{Op: "configmap.edit_text", Result: SREResultDone}, SREEffectChange},
		{SREAction{Op: "pod.exec", Result: SREResultFailed}, SREEffectExec},
	}
	for index, c := range cases {
		c.action.RequestID, c.action.At = fmt.Sprint(index), at.Add(time.Duration(index)*time.Second)
		receipts.Record(c.action)
		page, err := receipts.List(context.Background(), SREReceiptFilter{RequestID: c.action.RequestID})
		if err != nil || page.Matched != 1 || page.Receipts[0].Effect != c.want {
			t.Errorf("%s %s dry_run=%v went to %+v, want %s", c.action.Op, c.action.Result, c.action.DryRun, page.Receipts, c.want)
		}
	}
}

// A change naming more objects than its size holds gives names up for a
// "+N more" that still counts every object, and fits.
func TestAnOversizedChangeIsShortenedToItsSize(t *testing.T) {
	client := redistest.Start(t).Client()
	receipts := NewSREReceipts(client, effectKey, nil, nil)
	names := make([]string, 20)
	for index := range names {
		names[index] = "ConfigMap/ns/" + strings.Repeat("n", 240) + fmt.Sprint(index)
	}
	receipts.Record(SREAction{Op: "k8s.apply", Name: strings.Join(names, ",") + ",+30 more", RequestID: "big", Detail: "created 50"})
	stored := client.LRange(context.Background(), effectKey+":change", 0, 0).Val()
	if len(stored) != 1 || len(stored[0]) > SREChangeReceiptBytes {
		t.Fatalf("stored %d bytes, want at most %d", len(stored[0]), SREChangeReceiptBytes)
	}
	page, _ := receipts.List(context.Background(), SREReceiptFilter{RequestID: "big"})
	name := page.Receipts[0].Name
	kept := strings.Split(name, ",")
	var more int
	if _, err := fmt.Sscanf(kept[len(kept)-1], "+%d more", &more); err != nil || len(kept)-1+more != 50 || page.Receipts[0].Detail != "created 50" {
		t.Fatalf("name %q detail %q, want the kept names and a +N more counting all 50", name, page.Receipts[0].Detail)
	}
}

// A command receipt whose texts alone pass its size is cut at character
// boundaries, marked, and fits.
func TestAnOversizedCommandIsCutAtCharacters(t *testing.T) {
	client := redistest.Start(t).Client()
	receipts := NewSREReceipts(client, effectKey, nil, nil)
	long := strings.Repeat("容", 300)
	receipts.Record(SREAction{Op: "pod.exec", Namespace: "ns", Name: long + "/c", RequestID: "cut",
		Exec: &SREExec{RemoteState: "remote_completed", StartFailure: long}})
	stored := client.LRange(context.Background(), effectKey+":exec", 0, 0).Val()
	if len(stored) != 1 || len(stored[0]) > SREExecReceiptBytes {
		t.Fatalf("stored %d bytes, want at most %d", len(stored[0]), SREExecReceiptBytes)
	}
	page, _ := receipts.List(context.Background(), SREReceiptFilter{RequestID: "cut"})
	receipt := page.Receipts[0]
	for _, text := range []string{receipt.Name, receipt.Exec.StartFailure} {
		// A cut inside a character would be encoded as U+FFFD.
		if !utf8.ValidString(text) || strings.ContainsRune(text, utf8.RuneError) {
			t.Fatalf("text %q was cut inside a character", text)
		}
	}
	if !strings.HasSuffix(receipt.Name, "…") || receipt.Exec.RemoteState != "remote_completed" || receipt.Namespace != "ns" {
		t.Fatalf("receipt %+v, want the long texts cut at characters and marked, the rest kept", receipt)
	}
}

// Three full lists of receipts at their sizes hold at most 16 MiB of the
// shared Redis: 1024 changes of 4 KiB, 8192 commands of 1 KiB and 4096
// accesses of 1 KiB.
func TestThreeFullListsStayWithinTheirRedisFootprint(t *testing.T) {
	client := redistest.Start(t).Client()
	receipts := NewSREReceipts(client, effectKey, nil, nil)
	big := strings.Repeat("x", 8<<10)
	for _, burst := range []struct {
		op    string
		count int
	}{{"k8s.apply", MaxSREChangeReceipts + 3}, {"pod.exec", MaxSREExecReceipts + 3}, {"secret.get", MaxSREAccessReceipts + 3}} {
		for index := 0; index < burst.count; index++ {
			receipts.Record(SREAction{Op: burst.op, Name: big, Detail: big})
		}
	}
	ctx := context.Background()
	total := 0
	lengths := map[string]int64{}
	for _, key := range []string{effectKey, effectKey + ":change", effectKey + ":exec"} {
		for _, entry := range client.LRange(ctx, key, 0, -1).Val() {
			total += len(entry)
		}
		lengths[key] = client.LLen(ctx, key).Val()
	}
	if footprint := 16 << 20; total > footprint || lengths[effectKey] != 4096 || lengths[effectKey+":change"] != 1024 || lengths[effectKey+":exec"] != 8192 {
		t.Fatalf("lists hold %d bytes in %v receipts, want at most %d in 4096 accesses, 1024 changes and 8192 commands", total, lengths, footprint)
	}
}

// Cutting a receipt to its size leaves the caller's action as it was: the
// audit line and the receipt share the run's facts.
func TestCuttingAReceiptLeavesTheCallersActionAlone(t *testing.T) {
	client := redistest.Start(t).Client()
	long := strings.Repeat("f", 2<<10)
	exec := &SREExec{RemoteState: "remote_completed", StartFailure: long}
	NewSREReceipts(client, effectKey, nil, nil).Record(SREAction{Op: "pod.exec", Name: "p/c", Exec: exec})
	if exec.StartFailure != long {
		t.Fatalf("the caller's start failure became %d bytes, want it untouched", len(exec.StartFailure))
	}
}
