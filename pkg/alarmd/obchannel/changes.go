// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package obchannel

import (
	"context"
	"encoding/base64"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/cliauth"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/pod"
)

// changes.list answers "what changed before the detection went wrong": the
// changes made through this deployment's SRE channel - an image set or
// reverted, an object applied, patched or deleted, a Pod deleted, a workload
// restarted or scaled, a ConfigMap edited - and alarmd's own starts and
// stops. It reads records that already exist, the channel's change and exec
// receipts and the lifecycle record, and keeps none of its own.
//
// It is not every change in the cluster: what kubectl, helm or a pipeline
// did outside the channel never appears. Commands run through the channel
// (node.exec, pod.exec) are not rows - a command's receipt does not say
// whether it changed anything - but are counted by the hour, read in full
// by sre.receipts. Each source says how far back it reaches: the receipt
// lists keep a bounded number and expire a week after their last write, and
// the lifecycle record keeps its last 64 entries, so an empty answer is
// "nothing within what is kept", never "nothing happened".
const (
	OpChangesList = "changes.list"
	// ChangesDefaultWindow is the range read when none is given, and
	// ChangesMaxWindow the longest: the receipts' own retention.
	ChangesDefaultWindow = 24 * time.Hour
	ChangesMaxWindow     = SREReceiptRetention
	ChangesDefaultRows   = 50
	ChangesMaxRows       = 200
	// ChangesExecBucket is the width of one execution count.
	ChangesExecBucket = time.Hour
)

// The kinds of a change row, closed.
const (
	ChangeRelease  = "release"
	ChangeRollback = "rollback"
	ChangeWrite    = "write"
	ChangeStart    = "start"
	ChangeStop     = "stop"
)

// Who a change row is by, closed: an admin user by name, the deployment
// administrator (the key typed in), or not known - any receipt that names
// no operator.
const (
	OperatorUser            = "user"
	OperatorDeploymentAdmin = "deployment_admin"
	OperatorUnknown         = "unknown"
)

// changeKinds is the receipt operations that are change rows, by kind. A
// dry run changes nothing and is not one; a command is counted, not listed.
var changeKinds = map[string]string{
	OpSetImage: ChangeRelease, OpRevert: ChangeRollback,
	pod.OpApply: ChangeWrite, pod.OpDelete: ChangeWrite, pod.OpPatch: ChangeWrite, pod.OpDeletePod: ChangeWrite,
	pod.OpRestartWorkload: ChangeWrite, pod.OpScaleWorkload: ChangeWrite, pod.OpEditConfigMapText: ChangeWrite,
}

// opPodExec is pod.exec's receipt operation.
const opPodExec = "pod.exec"

// LifecycleEvent is one start or stop of an alarmd replica, as the
// lifecycle record keeps it.
type LifecycleEvent struct {
	Replica string
	Event   string
	At      time.Time
	Build   string
	Reason  string
}

// LifecycleRead is the lifecycle record read once: its entries, how many it
// keeps at most, how long it lives past its last write and how many entries
// did not decode.
type LifecycleRead struct {
	Events    []LifecycleEvent
	Cap       int
	Retention time.Duration
	Undecoded int
}

// ChangesOptions is what changes.list reads. A nil source is named as not
// read in the answer, never read as empty.
type ChangesOptions struct {
	Receipts  *SREReceipts
	Lifecycle func(context.Context) (LifecycleRead, error)
	Now       func() time.Time
}

// ChangeObject is what a change touched, by name only.
type ChangeObject struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
}

// ChangeRow is one change: a channel action on an object, or a replica's
// start or stop. It carries no session, request, script or text.
type ChangeRow struct {
	At     time.Time     `json:"at"`
	Kind   string        `json:"kind"`
	Op     string        `json:"op,omitempty"`
	Object *ChangeObject `json:"object,omitempty"`
	// Pod is the alarmd Pod that started or stopped (the lifecycle record's
	// replica, which is its Pod name).
	Pod string `json:"pod,omitempty"`
	// Operator is the admin user's name when OperatorKind is user.
	Operator     string `json:"operator,omitempty"`
	OperatorKind string `json:"operator_kind,omitempty"`
	Result       string `json:"result,omitempty"`
	// Build is the replica's build at a start or stop.
	Build      string `json:"build,omitempty"`
	StopReason string `json:"stop_reason,omitempty"`
}

// ExecutionBucket is how many commands ran through the channel in one hour.
type ExecutionBucket struct {
	Start    time.Time `json:"start"`
	NodeExec int       `json:"node_exec"`
	PodExec  int       `json:"pod_exec"`
}

// ChangesSourceReach is how far one source reaches: whether it was read and
// why not, how many entries it keeps against its cap, its oldest entry, and
// whether it is full - at its cap, so entries older than its oldest may have
// been pushed out.
type ChangesSourceReach struct {
	Read      bool       `json:"read"`
	Reason    string     `json:"reason,omitempty"`
	Kept      int        `json:"kept"`
	Cap       int        `json:"cap"`
	Full      bool       `json:"full"`
	OldestAt  *time.Time `json:"oldest_at,omitempty"`
	Undecoded int        `json:"undecoded,omitempty"`
	// RetentionSeconds is how long the source lives past its last write;
	// zero for one that does not expire on its own.
	RetentionSeconds int64 `json:"retention_seconds,omitempty"`
}

// ChangesCoverage is each source's reach. Changes is the change list, which
// gives the rows; Executions the exec list, which gives the node.exec and
// pod.exec counts; Lifecycle the starts and stops.
//
// A list below its cap still holds everything written within its retention,
// whose clock every write renews; only a full list can have let older
// entries go. EarliestReadableAt is therefore the latest oldest entry among
// the full sources, and absent when none is full.
type ChangesCoverage struct {
	Changes    ChangesSourceReach `json:"changes"`
	Executions ChangesSourceReach `json:"executions"`
	Lifecycle  ChangesSourceReach `json:"lifecycle"`
	// Trimmed names the full sources whose oldest entry is after since: the
	// range asked reaches past what they still hold, so rows or counts in it
	// may have been pushed out.
	Trimmed            []string   `json:"trimmed,omitempty"`
	EarliestReadableAt *time.Time `json:"earliest_readable_at,omitempty"`
}

// ChangesAnswer is one page of changes, newest first.
type ChangesAnswer struct {
	Since      time.Time         `json:"since"`
	Until      time.Time         `json:"until"`
	Rows       []ChangeRow       `json:"rows"`
	Matched    int               `json:"matched"`
	NextCursor string            `json:"next_cursor,omitempty"`
	Executions []ExecutionBucket `json:"executions"`
	// ExecutionsReadBy is the operation that lists the commands counted.
	ExecutionsReadBy string          `json:"executions_read_by"`
	Coverage         ChangesCoverage `json:"coverage"`
}

// ChangesOperation is changes.list.
func ChangesOperation(options ChangesOptions) Operation {
	timestamp := Field{Type: "string", MinLength: 1, MaxLength: 64, Description: "RFC 3339 time"}
	positive, maxRows := int64(1), int64(ChangesMaxRows)
	return Operation{ID: OpChangesList,
		Summary: "变更记录：经本部署 SRE 通道做的变更（改镜像=发布、回退=回滚、apply/patch/delete、删 Pod、重启、扩缩、改 ConfigMap=写操作）与 alarmd 副本的启动、停止，最新的在前。" +
			"每行只有时刻、类别、操作、对象（kind/namespace/name）、操作者（管理员用户名、部署管理员或未知）、结果，启停另带 Pod、构建版本与停止原因；不含会话、请求、脚本与命令行。" +
			"不经通道的改动（kubectl、helm、流水线）不在其中；经通道的命令执行（node.exec、pod.exec）不列行，按小时计数，明细用 sre.receipts 读。" +
			"coverage 给出各来源能读到多早（最早一条、上限、是否已满、保留期）：空列表是\"保留范围内没有\"，不是\"没有变更\"。",
		EvidenceScope: "deployment",
		Fields: map[string]Field{"since": timestamp, "until": timestamp, "cursor": {Type: "string", MaxLength: 1024},
			"limit": {Type: "integer", Minimum: &positive, Maximum: &maxRows}},
		OutputSchema: SchemaOf(ChangesAnswer{}),
		Limits: map[string]any{"max_window_seconds": int64(ChangesMaxWindow / time.Second), "default_window_seconds": int64(ChangesDefaultWindow / time.Second),
			"max_rows": ChangesMaxRows, "redis_commands": 3, "execution_bucket_seconds": int64(ChangesExecBucket / time.Second)},
		Run: func(ctx context.Context, p Params) Outcome {
			answer, failure := listChanges(ctx, options, p)
			if failure != nil {
				return Outcome{Error: failure}
			}
			coverage := answer.Coverage
			complete := coverage.Changes.Read && coverage.Executions.Read && coverage.Lifecycle.Read && len(coverage.Trimmed) == 0
			out := Outcome{Value: answer, Complete: complete && answer.NextCursor == "",
				Summary: fmt.Sprintf("%d 条变更（本页 %d 条）", answer.Matched, len(answer.Rows))}
			if answer.NextCursor != "" {
				next := Params{"cursor": answer.NextCursor, "since": answer.Since.Format(time.RFC3339Nano), "until": answer.Until.Format(time.RFC3339Nano)}
				if value, present := p["limit"]; present {
					next["limit"] = value
				}
				out.Next = []Call{{Operation: OpChangesList, Params: next, Reason: "同一时间范围的下一页。"}}
			}
			for name, reach := range map[string]ChangesSourceReach{"changes": coverage.Changes, "executions": coverage.Executions, "lifecycle": coverage.Lifecycle} {
				if !reach.Read {
					out.Limitations = append(out.Limitations, name+" was not read ("+reach.Reason+"): its rows or counts are missing, not zero.")
				}
			}
			for _, name := range coverage.Trimmed {
				out.Limitations = append(out.Limitations, name+" is full and its oldest entry is after since: rows or counts before it may have been pushed out, missing rather than none.")
			}
			sort.Strings(out.Limitations)
			return out
		}}
}

func listChanges(ctx context.Context, options ChangesOptions, p Params) (ChangesAnswer, *Failure) {
	now := time.Now
	if options.Now != nil {
		now = options.Now
	}
	until, since := now().UTC(), time.Time{}
	for key, into := range map[string]*time.Time{"since": &since, "until": &until} {
		if value := p.String(key); value != "" {
			at, err := time.Parse(time.RFC3339Nano, value)
			if err != nil {
				return ChangesAnswer{}, &Failure{Code: "changes_invalid_time", Message: key + " is not an RFC 3339 time"}
			}
			*into = at.UTC()
		}
	}
	if since.IsZero() {
		since = until.Add(-ChangesDefaultWindow)
	}
	if !since.Before(until) || until.Sub(since) > ChangesMaxWindow {
		return ChangesAnswer{}, &Failure{Code: "changes_invalid_range", Message: "since must be before until, at most 7 days apart"}
	}
	limit := p.Int("limit", ChangesDefaultRows)
	if limit <= 0 || limit > ChangesMaxRows {
		limit = ChangesDefaultRows
	}
	after, err := decodeChangesCursor(p.String("cursor"))
	if err != nil {
		return ChangesAnswer{}, &Failure{Code: "changes_invalid_cursor", Message: "cursor is not one this operation gave"}
	}

	answer := ChangesAnswer{Since: since, Until: until, Rows: []ChangeRow{}, Executions: []ExecutionBucket{}, ExecutionsReadBy: OpSREReceipts}
	answer.Coverage.Changes = ChangesSourceReach{Cap: MaxSREChangeReceipts, RetentionSeconds: int64(SREReceiptRetention / time.Second)}
	answer.Coverage.Executions = ChangesSourceReach{Cap: MaxSREExecReceipts, RetentionSeconds: int64(SREReceiptRetention / time.Second)}
	answer.Coverage.Lifecycle = ChangesSourceReach{}
	inRange := func(at time.Time) bool { return !at.Before(since) && at.Before(until) }
	var rows []ChangeRow
	buckets := map[time.Time]*ExecutionBucket{}
	bucket := func(at time.Time) *ExecutionBucket {
		start := at.UTC().Truncate(ChangesExecBucket)
		if buckets[start] == nil {
			buckets[start] = &ExecutionBucket{Start: start}
		}
		return buckets[start]
	}

	read := func(effect string, light bool, reach *ChangesSourceReach, visit func(SREAction)) {
		if options.Receipts == nil {
			reach.Reason = "receipts_unavailable"
			return
		}
		readList := options.Receipts.Read
		if light {
			readList = options.Receipts.ReadStamps
		}
		receipts, undecoded, err := readList(ctx, effect)
		if err != nil {
			reach.Reason = "store_unavailable"
			return
		}
		reach.Read, reach.Kept, reach.Undecoded = true, len(receipts)+undecoded, undecoded
		reach.Full = reach.Kept >= reach.Cap
		if len(receipts) > 0 {
			oldest := receipts[len(receipts)-1].At.UTC()
			reach.OldestAt = &oldest
		}
		for _, receipt := range receipts {
			if inRange(receipt.At) {
				visit(receipt)
			}
		}
	}
	// The change list gives the rows.
	read(SREEffectChange, false, &answer.Coverage.Changes, func(receipt SREAction) {
		kind, ok := changeKinds[receipt.Op]
		if !ok || receipt.DryRun {
			return
		}
		row := ChangeRow{At: receipt.At.UTC(), Kind: kind, Op: receipt.Op, Result: receipt.Result,
			Object: &ChangeObject{Kind: receipt.Kind, Namespace: receipt.Namespace, Name: receipt.Name}}
		row.Operator, row.OperatorKind = operatorOf(receipt)
		rows = append(rows, row)
	})
	// The exec list gives the counts: one per node.exec or pod.exec that
	// ran. A node.exec's cancel and end receipts are the same command and
	// are not counted again.
	read(SREEffectExec, true, &answer.Coverage.Executions, func(receipt SREAction) {
		if !started(receipt) {
			return
		}
		switch receipt.Op {
		case OpNodeExec:
			bucket(receipt.At).NodeExec++
		case opPodExec:
			bucket(receipt.At).PodExec++
		}
	})

	if options.Lifecycle == nil {
		answer.Coverage.Lifecycle.Reason = "lifecycle_unavailable"
	} else if record, err := options.Lifecycle(ctx); err != nil {
		answer.Coverage.Lifecycle.Reason = "store_unavailable"
	} else {
		reach := &answer.Coverage.Lifecycle
		reach.Read, reach.Cap, reach.Undecoded = true, record.Cap, record.Undecoded
		reach.RetentionSeconds = int64(record.Retention / time.Second)
		reach.Kept = len(record.Events) + record.Undecoded
		reach.Full = record.Cap > 0 && reach.Kept >= record.Cap
		for _, event := range record.Events {
			at := event.At.UTC()
			if reach.OldestAt == nil || at.Before(*reach.OldestAt) {
				reach.OldestAt = &at
			}
			if !inRange(event.At) || (event.Event != ChangeStart && event.Event != ChangeStop) {
				continue
			}
			row := ChangeRow{At: at, Kind: event.Event, Pod: event.Replica, Build: event.Build}
			if event.Event == ChangeStop {
				row.StopReason = event.Reason
			}
			rows = append(rows, row)
		}
	}

	for _, source := range []struct {
		name  string
		reach ChangesSourceReach
	}{{"changes", answer.Coverage.Changes}, {"executions", answer.Coverage.Executions}, {"lifecycle", answer.Coverage.Lifecycle}} {
		reach := source.reach
		if !reach.Read || !reach.Full || reach.OldestAt == nil {
			continue
		}
		if answer.Coverage.EarliestReadableAt == nil || reach.OldestAt.After(*answer.Coverage.EarliestReadableAt) {
			at := *reach.OldestAt
			answer.Coverage.EarliestReadableAt = &at
		}
		if reach.OldestAt.After(since) {
			answer.Coverage.Trimmed = append(answer.Coverage.Trimmed, source.name)
		}
	}
	for _, counted := range buckets {
		answer.Executions = append(answer.Executions, *counted)
	}
	sort.Slice(answer.Executions, func(i, j int) bool { return answer.Executions[i].Start.After(answer.Executions[j].Start) })

	// Newest first; rows of the same instant in a fixed order by their key,
	// so a cursor that names the last row given - its instant and key -
	// resumes right after it whatever is written meanwhile at newer times. A
	// row written later at that very instant with a key before the cursor's
	// is not given; two rows identical in every listed field at one instant
	// read as one position.
	sort.SliceStable(rows, func(i, j int) bool {
		if !rows[i].At.Equal(rows[j].At) {
			return rows[i].At.After(rows[j].At)
		}
		return rowKey(rows[i]) < rowKey(rows[j])
	})
	answer.Matched = len(rows)
	start := 0
	if after != nil {
		for start < len(rows) && (rows[start].At.After(after.at) || rows[start].At.Equal(after.at) && cursorKey(rows[start]) <= after.key) {
			start++
		}
	}
	end := min(len(rows), start+limit)
	answer.Rows = append(answer.Rows, rows[start:end]...)
	if end < len(rows) {
		answer.NextCursor = encodeChangesCursor(rows[end-1].At, cursorKey(rows[end-1]))
	}
	return answer, nil
}

// started says a command receipt is of a command that ran: one refused or
// forbidden before it started is not counted as an execution.
func started(receipt SREAction) bool {
	return receipt.Result != SREResultRefused && receipt.Result != SREResultForbidden
}

// operatorOf is who a receipt names: the admin user, the deployment
// administrator, or unknown when it names neither.
func operatorOf(receipt SREAction) (string, string) {
	switch {
	case receipt.Operator != "":
		return receipt.Operator, OperatorUser
	case receipt.IssuedVia == cliauth.IssuedViaAdminKey:
		return "", OperatorDeploymentAdmin
	}
	return "", OperatorUnknown
}

func rowKey(row ChangeRow) string {
	parts := []string{row.Kind, row.Op, row.Pod, row.Result, row.OperatorKind, row.Operator, row.Build, row.StopReason}
	if row.Object != nil {
		parts = append(parts, row.Object.Namespace, row.Object.Kind, row.Object.Name)
	}
	return strings.Join(parts, "\x00")
}

type changesCursor struct {
	at  time.Time
	key string
}

// maxChangesCursorKey bounds the key a cursor carries. A row's key is cut to
// it on both sides of the comparison, so rows that share a cut key at one
// instant read as one position; keys are far shorter in practice.
const maxChangesCursorKey = 512

func cursorKey(row ChangeRow) string {
	key := rowKey(row)
	if len(key) > maxChangesCursorKey {
		key = key[:maxChangesCursorKey]
	}
	return key
}

func encodeChangesCursor(at time.Time, key string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(at.UnixNano(), 10) + ":" + key))
}

func decodeChangesCursor(text string) (*changesCursor, error) {
	if text == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(text)
	if err != nil {
		return nil, err
	}
	nanos, key, found := strings.Cut(string(raw), ":")
	if !found || len(key) > maxChangesCursorKey {
		return nil, fmt.Errorf("cursor")
	}
	at, err := strconv.ParseInt(nanos, 10, 64)
	if err != nil {
		return nil, err
	}
	return &changesCursor{at: time.Unix(0, at).UTC(), key: key}, nil
}
