// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package obchannel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/redisatomic"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/pod"
)

const (
	// OpSREReceipts reads the receipts of the SRE channel's actions.
	OpSREReceipts = "sre.receipts"
	// SREReceiptRetention is how long each receipt list lives past its own
	// latest write.
	SREReceiptRetention = 7 * 24 * time.Hour
	// Receipts are kept in three lists by effect, so one kind cannot push
	// another out:
	//
	//   - change: an action that may have changed state - an image set or
	//     reverted, an object applied, patched or deleted, a Pod deleted, a
	//     workload restarted or scaled, a ConfigMap edited - that was not a
	//     dry run and was not refused before any effect;
	//   - exec: a command run through the channel (node.exec, its cancel and
	//     its end, pod.exec), which may have changed anything and whose
	//     receipt cannot say;
	//   - access: everything else - a read (an execution's output, a Secret
	//     or ConfigMap, a resource list), a dry run, and an attempt refused
	//     before any effect.
	//
	// The lists live in the deployment's shared Redis, so what bounds them is
	// their footprint there: each receipt is cut to its list's size when it
	// is written. The caps are sized from one deployment's receipts of
	// 2026-10-16 to 10-22, each list to hold the 7 days of retention at the busiest day
	// seen:
	//
	//   - change: 127 changes on the busiest day (dry runs included, which
	//     now go to access), 7 x 127 = 889, so 1024; 4 KiB each, 4 MiB at
	//     most, about 0.6 MB at the sizes seen (median 400-600 bytes);
	//   - exec: 172 node.exec runs on the busiest day, each with an exec and
	//     an end receipt (323 in all), and 531 pod.exec runs the same day:
	//     854 a day, 7 x 854 = 5978, so 8192; 1 KiB each (node receipts reach
	//     531 bytes, pod.exec 830), 8 MiB at most, about 5 MB at the sizes
	//     seen;
	//   - access: about 1350 a day, mostly node.execution.get polls, so 4096
	//     holds about 3 days; 1 KiB each, 4 MiB at most. Reads change
	//     nothing, and 3 days covers looking back over a recent incident;
	//     the reach shrinks as reads grow, and changes.list does not read
	//     this list.
	SREChangeReceiptBytes = 4 << 10
	SREExecReceiptBytes   = 1 << 10
	SREAccessReceiptBytes = 1 << 10
	MaxSREChangeReceipts  = 1024
	MaxSREExecReceipts    = 8192
	MaxSREAccessReceipts  = 4096
	// MaxSREReceipts is how many receipts are kept in all.
	MaxSREReceipts = MaxSREChangeReceipts + MaxSREExecReceipts + MaxSREAccessReceipts
	// SREEffectChange, SREEffectExec and SREEffectAccess name the lists.
	SREEffectChange = "change"
	SREEffectExec   = "exec"
	SREEffectAccess = "access"
	// sreReceiptWriteTimeout bounds one write: a store that does not answer
	// does not hold the action's answer up for longer.
	sreReceiptWriteTimeout = 500 * time.Millisecond
	// maxSREReceiptsPage is the most receipts one read returns.
	maxSREReceiptsPage = 100
)

// SREExec is what a pod.exec receipt says about the run, without its
// output: how it was supervised, how it ended, and how its supervisor
// failed if it did.
type SREExec struct {
	RemoteState            string `json:"remote_state"`
	Supervision            string `json:"supervision,omitempty"`
	UnsupervisedReason     string `json:"unsupervised_reason,omitempty"`
	ExitCode               *int   `json:"exit_code,omitempty"`
	DeadlineRaced          bool   `json:"deadline_raced,omitempty"`
	StreamCut              bool   `json:"stream_cut,omitempty"`
	Truncated              bool   `json:"truncated,omitempty"`
	ScriptDigest           string `json:"script_digest,omitempty"`
	DeclaredTimeoutMS      int64  `json:"declared_timeout_ms,omitempty"`
	EnforcedTimeoutSeconds int64  `json:"enforced_timeout_seconds,omitempty"`
	BootstrapExitCode      *int   `json:"bootstrap_exit_code,omitempty"`
	BootstrapClass         string `json:"bootstrap_class,omitempty"`
	StartFailure           string `json:"start_failure,omitempty"`
}

// SREReceipts keeps the SRE channel's receipts in the shared store, three
// lists for the deployment (changes under key+":change", commands under
// key+":exec", accesses under key): every replica writes its actions and any replica answers
// sre.receipts. A write that fails is counted and does not hold the
// action up; the sre_action line is then the only record.
type SREReceipts struct {
	client  redis.Cmdable
	key     string
	now     func() time.Time
	written func(ok bool)
}

// NewSREReceipts keeps receipts under key; written hears each write's
// outcome and may be nil.
func NewSREReceipts(client redis.Cmdable, key string, now func() time.Time, written func(ok bool)) *SREReceipts {
	if now == nil {
		now = time.Now
	}
	return &SREReceipts{client: client, key: key, now: now, written: written}
}

// sreReceiptWrite adds a receipt, cuts the list to its bound and renews its
// life, as one script rather than a MULTI/EXEC (see redisatomic for why).
// One key, so it is in one slot on any deployment. ARGV[3] is seconds, as
// EXPIRE takes them.
var sreReceiptWrite = redisatomic.New(`
redis.call('LPUSH', KEYS[1], ARGV[1])
redis.call('LTRIM', KEYS[1], 0, tonumber(ARGV[2]))
redis.call('EXPIRE', KEYS[1], tonumber(ARGV[3]))
return 1
`)

// sreEffect is the list an operation's receipts go to when it takes effect.
func sreEffect(op string) string {
	switch op {
	case OpSetImage, OpRevert, pod.OpApply, pod.OpDelete, pod.OpPatch, pod.OpDeletePod, pod.OpRestartWorkload, pod.OpScaleWorkload, pod.OpEditConfigMapText:
		return SREEffectChange
	case OpNodeExec, OpNodeCancel, OpNodeEnded, opPodExec:
		return SREEffectExec
	}
	return SREEffectAccess
}

// receiptEffect is the list one receipt goes to: its operation's, unless it
// cannot have changed anything - a dry run, or an attempt refused before any
// effect (by validation or a busy provider, by the apiserver's
// authorization, or because its target was replaced since it was read) -
// which is an access. A failure stays where it is: the apiserver or the
// transport failing does not say nothing was written.
func receiptEffect(action SREAction) string {
	switch {
	case action.DryRun, action.Result == SREResultRefused, action.Result == SREResultForbidden, action.Result == SREResultTargetChanged:
		return SREEffectAccess
	}
	return sreEffect(action.Op)
}

// sreLists is each list with its receipt size and cap, in the order a read
// merges them.
var sreLists = []struct {
	effect     string
	size, kept int
}{
	{SREEffectChange, SREChangeReceiptBytes, MaxSREChangeReceipts},
	{SREEffectExec, SREExecReceiptBytes, MaxSREExecReceipts},
	{SREEffectAccess, SREAccessReceiptBytes, MaxSREAccessReceipts},
}

// sreListOf is one list's size and cap; an unknown effect is the access list.
func sreListOf(effect string) (size, kept int) {
	for _, list := range sreLists {
		if list.effect == effect {
			return list.size, list.kept
		}
	}
	return SREAccessReceiptBytes, MaxSREAccessReceipts
}

func (s *SREReceipts) keyOf(effect string) string {
	switch effect {
	case SREEffectChange, SREEffectExec:
		return s.key + ":" + effect
	}
	return s.key
}

// Record writes one action's receipt, the newest first, within its own
// bound, and says whether it landed.
func (s *SREReceipts) Record(action SREAction) {
	if s == nil {
		return
	}
	err := s.record(action)
	if s.written != nil {
		s.written(err == nil)
	}
}

func (s *SREReceipts) record(action SREAction) error {
	if action.At.IsZero() {
		action.At = s.now().UTC()
	}
	effect := receiptEffect(action)
	size, kept := sreListOf(effect)
	body, err := fitReceipt(action, size)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), sreReceiptWriteTimeout)
	defer cancel()
	return sreReceiptWrite.Run(ctx, s.client, []string{s.keyOf(effect)}, body, kept-1, int64(SREReceiptRetention/time.Second))
}

// fitReceipt encodes a receipt within size bytes. Past it, the object list
// gives up names from its end for a "+N more", and then the free texts are
// halved, longest first, until it fits.
func fitReceipt(action SREAction, size int) ([]byte, error) {
	body, err := json.Marshal(action)
	if err != nil || len(body) <= size {
		return body, err
	}
	names, more := strings.Split(action.Name, ","), 0
	if last := names[len(names)-1]; len(names) > 1 && strings.HasPrefix(last, "+") && strings.HasSuffix(last, " more") {
		if _, scanned := fmt.Sscanf(last, "+%d more", &more); scanned == nil {
			names = names[:len(names)-1]
		}
	}
	for len(names) > 1 {
		names, more = names[:len(names)-1], more+1
		action.Name = strings.Join(names, ",") + fmt.Sprintf(",+%d more", more)
		if body, err = json.Marshal(action); err != nil || len(body) <= size {
			return body, err
		}
	}
	texts := []*string{&action.Name, &action.Detail, &action.Namespace, &action.UID, &action.Operator}
	if action.Exec != nil {
		exec := *action.Exec
		action.Exec = &exec
		texts = append(texts, &exec.StartFailure, &exec.UnsupervisedReason, &exec.Supervision, &exec.BootstrapClass)
	}
	for len(body) > size {
		longest := texts[0]
		for _, text := range texts[1:] {
			if len(*text) > len(*longest) {
				longest = text
			}
		}
		if *longest == "" {
			break
		}
		*longest = halveUTF8(*longest)
		if body, err = json.Marshal(action); err != nil {
			return nil, err
		}
	}
	return body, nil
}

// halveUTF8 is the first half of text, cut at a character boundary, marked
// as cut; a text of one character or less becomes empty.
func halveUTF8(text string) string {
	cut := len(text) / 2
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	if cut <= len("…") {
		return ""
	}
	return strings.TrimSuffix(text[:cut], "…") + "…"
}

// SREReceiptFilter selects receipts: by time (since inclusive, until
// exclusive; zero is open), by namespace, by object name (the name, or a
// Pod's name before its /container), by request; Offset and Limit page the
// matches, newest first.
type SREReceiptFilter struct {
	Since, Until    time.Time
	Namespace, Name string
	RequestID       string
	ExecutionID     string
	// Effect selects one list, change, exec or access; empty reads all.
	Effect        string
	Offset, Limit int
}

// SREReceiptPage is one page of matching receipts, newest first: Matched is
// all that match, Stored all kept, NextOffset the next page's offset while
// there is one.
type SREReceiptPage struct {
	Receipts []SREAction `json:"receipts"`
	Matched  int         `json:"matched"`
	Stored   int         `json:"stored"`
	// StoredByEffect is what each list keeps.
	StoredByEffect map[string]int `json:"stored_by_effect"`
	NextOffset     int            `json:"next_offset,omitempty"`
}

// List reads the kept receipts and returns the page that matches.
func (s *SREReceipts) List(ctx context.Context, filter SREReceiptFilter) (SREReceiptPage, error) {
	page := SREReceiptPage{Receipts: []SREAction{}, StoredByEffect: map[string]int{}}
	limit := filter.Limit
	if limit <= 0 || limit > maxSREReceiptsPage {
		limit = maxSREReceiptsPage
	}
	var matches []SREAction
	for _, list := range sreLists {
		raw, err := s.client.LRange(ctx, s.keyOf(list.effect), 0, int64(list.kept-1)).Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return page, err
		}
		page.Stored += len(raw)
		page.StoredByEffect[list.effect] = len(raw)
		if filter.Effect != "" && filter.Effect != list.effect {
			continue
		}
		for _, entry := range raw {
			var action SREAction
			if json.Unmarshal([]byte(entry), &action) != nil || !filter.matches(action) {
				continue
			}
			action.Effect = list.effect
			matches = append(matches, action)
		}
	}
	// Written newest first; two replicas' writes can cross, so order by
	// the receipts' own times.
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].At.After(matches[j].At) })
	page.Matched = len(matches)
	if filter.Offset < len(matches) {
		end := min(len(matches), filter.Offset+limit)
		page.Receipts = matches[filter.Offset:end]
		if end < len(matches) {
			page.NextOffset = end
		}
	}
	return page, nil
}

// Read is every receipt one list keeps, in one read, newest first by the
// receipts' own times, with how many entries did not decode. changes.list
// reads each list once this way rather than paging List, which reads both
// lists whole on every page.
func (s *SREReceipts) Read(ctx context.Context, effect string) ([]SREAction, int, error) {
	_, kept := sreListOf(effect)
	raw, err := s.client.LRange(ctx, s.keyOf(effect), 0, int64(kept-1)).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, 0, err
	}
	receipts, undecoded := make([]SREAction, 0, len(raw)), 0
	for _, entry := range raw {
		var action SREAction
		if json.Unmarshal([]byte(entry), &action) != nil {
			undecoded++
			continue
		}
		action.Effect = effect
		receipts = append(receipts, action)
	}
	sort.SliceStable(receipts, func(i, j int) bool { return receipts[i].At.After(receipts[j].At) })
	return receipts, undecoded, nil
}

// ReadStamps is Read with each receipt decoded into its operation, time and
// result only: changes.list counts the exec list's commands and needs no
// more, and the exec list is the largest one (MaxSREExecReceipts).
func (s *SREReceipts) ReadStamps(ctx context.Context, effect string) ([]SREAction, int, error) {
	_, kept := sreListOf(effect)
	raw, err := s.client.LRange(ctx, s.keyOf(effect), 0, int64(kept-1)).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, 0, err
	}
	stamps, undecoded := make([]SREAction, 0, len(raw)), 0
	for _, entry := range raw {
		var stamp struct {
			Op     string    `json:"op"`
			At     time.Time `json:"at"`
			Result string    `json:"result"`
		}
		if json.Unmarshal([]byte(entry), &stamp) != nil {
			undecoded++
			continue
		}
		stamps = append(stamps, SREAction{Op: stamp.Op, At: stamp.At, Result: stamp.Result, Effect: effect})
	}
	sort.SliceStable(stamps, func(i, j int) bool { return stamps[i].At.After(stamps[j].At) })
	return stamps, undecoded, nil
}

func (f SREReceiptFilter) matches(action SREAction) bool {
	if !f.Since.IsZero() && action.At.Before(f.Since) {
		return false
	}
	if !f.Until.IsZero() && !action.At.Before(f.Until) {
		return false
	}
	if f.Namespace != "" && action.Namespace != f.Namespace {
		return false
	}
	if f.Name != "" && action.Name != f.Name && !strings.HasPrefix(action.Name, f.Name+"/") {
		return false
	}
	return (f.RequestID == "" || action.RequestID == f.RequestID) && (f.ExecutionID == "" || action.ExecutionID == f.ExecutionID)
}
