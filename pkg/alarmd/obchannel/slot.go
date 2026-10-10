// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package obchannel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/access"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/access/uq"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// SlotOptions keeps diagnostics outside the production execution coordinator.
// Resolve must only read the historical Segment and its immutable objects.
type SlotOptions struct {
	Resolve  func(context.Context, execution.SlotIdentity) (SlotPlan, error)
	Evidence func(context.Context, execution.SlotIdentity) (SlotEvidence, error)
	UQ       *uq.DiagnosticClient
	// LatestPublication, when set, reads the newest publication, the one
	// strategy.get reports, so a Slot's older snapshot_revision is read
	// beside it rather than taken for a fault.
	LatestPublication func(context.Context) (SlotPublication, error)
}

// SlotPublication is a publication by its content revision and epoch.
type SlotPublication struct {
	SnapshotRevision execution.SnapshotRevision `json:"snapshot_revision"`
	PublicationEpoch uint64                     `json:"publication_epoch"`
	// SameAsSlot is whether it is the publication the Slot's Segment began
	// under.
	SameAsSlot bool `json:"same_as_slot"`
}

// slotSnapshotNote says why a Slot names an older publication than the
// latest: its snapshot_revision is the publication its schedule Segment
// began under, and a Segment is kept across publications while the Query
// Group's execution content -- its ObjectDigest -- is unchanged (see
// controlplane/content_cutover.go). Two readers of the first acceptance
// stopped on the difference and asked. It must not explain a change that
// did not take effect as normal: any change to the execution content, a
// threshold or the members included, cuts a new Segment.
const slotSnapshotNote = "slot.snapshot_revision is the publication this Slot's schedule Segment began under (schedule_segment_start). " +
	"A Segment is kept across publications while this Query Group's execution content (object_digest: its query, schedule, membership " +
	"and what its Plans evaluate) is unchanged, so a Slot can name an older publication than latest_publication. A change to any of that " +
	"starts a new Segment; a change to rendering only does not. Whether this Slot ran the same content as a publication is decided by " +
	"object_digest, not by snapshot_revision."

type SlotContext struct {
	QueryGroup       execution.QueryGroupIdentity `json:"query_group"`
	EvaluationTime   execution.EvaluationTime     `json:"evaluation_time"`
	SnapshotRevision execution.SnapshotRevision   `json:"snapshot_revision"`
	QueryRevision    execution.QueryRevision      `json:"query_revision"`
	ScheduleRevision execution.ScheduleRevision   `json:"schedule_revision"`
	SegmentStart     execution.EvaluationTime     `json:"schedule_segment_start"`
	ObjectDigest     execution.ObjectDigest       `json:"object_digest"`
	DuePlanSetDigest execution.DuePlanSetDigest   `json:"due_plan_set_digest"`
	ContractDigest   string                       `json:"contract_digest"`
	// ReadHoldMillis is the read hold the Slot was frozen with, and
	// ReadHoldBasis where it was read from (ReadHoldFromProgress, ...).
	ReadHoldMillis int64  `json:"read_hold_ms"`
	ReadHoldBasis  string `json:"read_hold_basis,omitempty"`
}

type SlotQueryInput struct {
	RequirementID execution.RequirementID `json:"requirement_id"`
	Dataset       execution.DatasetName   `json:"dataset"`
	Role          execution.InputRole     `json:"role"`
	Consumers     []execution.ConsumerRef `json:"consumers"`
}

type SlotQueryPlan struct {
	PhysicalQueryDigest execution.PhysicalQueryDigest `json:"physical_query_digest"`
	Inputs              []SlotQueryInput              `json:"inputs"`
	Request             uq.DiagnosticPreview          `json:"request"`
}

type SlotGetResult struct {
	Kind string      `json:"kind"`
	Slot SlotContext `json:"slot"`
	// LatestPublication is the newest publication beside the Slot's own,
	// and SnapshotNote why the two differ, present only when they do.
	LatestPublication       *SlotPublication `json:"latest_publication,omitempty"`
	SnapshotNote            string           `json:"snapshot_note,omitempty"`
	Queries                 []SlotQueryPlan  `json:"queries"`
	Retained                SlotEvidence     `json:"retained"`
	HistoricalInputComplete bool             `json:"historical_input_complete"`
}

type SlotQueryResult struct {
	Kind                string                        `json:"kind"`
	Slot                SlotContext                   `json:"slot"`
	PhysicalQueryDigest execution.PhysicalQueryDigest `json:"physical_query_digest"`
	QueriedAt           time.Time                     `json:"queried_at"`
	Query               uq.DiagnosticResult           `json:"query"`
}

const slotBoundary = "Reconstructed with this build from a retained contract. A query now can contain late data; it is not the original response or a replay of detection, state, progress or events. The answering process is not necessarily the historical producer."

func SlotOperations(options SlotOptions) []Operation {
	integer := func(description string, low, high int64) Field {
		return Field{Type: "integer", Description: description, Minimum: &low, Maximum: &high}
	}
	digest := func(description string) Field {
		return Field{Type: "string", Pattern: "^[a-f0-9]{64}$", Description: description, Source: "slot.get next_call"}
	}
	base := func() map[string]Field {
		return map[string]Field{
			"query_group":     {Type: "string", MinLength: 1, MaxLength: 256, Pattern: "^[A-Za-z0-9_.:-]+$", Description: "运行对象ID。", Source: "strategy.get 或 object.get"},
			"evaluation_time": integer("Slot Unix秒，不是查询发起时间或毫秒。", 1, 4102444800),
		}
	}
	limits := map[string]any{"timeout_ms": RequestTimeout.Milliseconds(), "response_bytes": MaxResponseBytes, "physical_queries_per_call": 1, "query_plans": 32,
		"uq_body_bytes": 4 << 20, "uq_series_bytes": 512 << 10, "uq_series": 1000, "uq_records": 20000, "returned_series": 10, "returned_points_total": 500,
		"selected_series_bytes": uq.DiagnosticMaxOutputBytes, "provider_metadata_bytes": 16 << 10,
		"contract_redis":                map[string]int{"commands": 64, "bytes": 4 << 20, "document_bytes": 1 << 20},
		"query_concurrency_per_process": 1, "retained_records_scan": 200, "retained_samples_scan": 50,
		"retained_redis": map[string]int{"commands": 64, "bytes": 4 << 20, "record_bytes": 4 << 10},
		"population":     "Series selection occurs after the original query and normalization; small output does not limit upstream computation."}
	availability := func() Availability {
		if options.Resolve != nil && options.UQ != nil {
			return Availability{Available: true}
		}
		return Availability{Reason: "Requires historical catalog reads and configured diagnostic UQ client."}
	}
	get := Operation{ID: "slot.get", Summary: "按历史Slot重建查询条件，关联已保留记录；不请求UQ、不执行检测。", EvidenceScope: "retained_contract_and_records", Targetable: true,
		Fields: base(), Required: []string{"query_group", "evaluation_time"}, OutputSchema: SchemaOf(SlotGetResult{}), Limits: limits, Availability: availability}
	get.Run = func(ctx context.Context, p Params) Outcome {
		plan, failure := resolveSlot(ctx, options, p)
		if failure != nil {
			return Outcome{Error: failure}
		}
		result := SlotGetResult{Kind: "reconstructed_from_contract", Slot: slotContext(plan), Queries: []SlotQueryPlan{}, Retained: SlotEvidence{Records: []json.RawMessage{}, Samples: []json.RawMessage{}}}
		out := Outcome{Complete: true, Limitations: []string{slotBoundary}}
		if plan.ReadHoldBasis == ReadHoldNoRecord {
			out.Limitations = append(out.Limitations, slotNoHoldRecord)
		}
		for _, query := range plan.Prepared.Queries {
			preview, err := options.UQ.Preview(query.Spec)
			if err != nil {
				return Outcome{Error: &Failure{Code: "query_plan_unavailable", Message: "Historical query cannot be rendered by this build."}}
			}
			item := SlotQueryPlan{PhysicalQueryDigest: query.Spec.Digest, Request: preview, Inputs: []SlotQueryInput{}}
			for _, requirement := range append(append([]execution.DataRequirement{}, query.Requirements...), query.ReadinessInvalidRequirements...) {
				input := SlotQueryInput{RequirementID: requirement.RequirementID, Dataset: requirement.DatasetName, Role: requirement.Role, Consumers: []execution.ConsumerRef{}}
				for _, consumer := range requirement.Consumers {
					input.Consumers = append(input.Consumers, consumer.Consumer)
				}
				item.Inputs = append(item.Inputs, input)
			}
			result.Queries = append(result.Queries, item)
			next := slotParams(result.Slot)
			next["contract_digest"], next["physical_query_digest"], next["request_digest"] = result.Slot.ContractDigest, string(query.Spec.Digest), preview.RequestDigest
			out.Next = append(out.Next, Call{Operation: "slot.query", Params: next, Reason: "选取此物理查询按原条件重查UQ；默认通过控制面定位当前owner，结果属于本次查询。"})
		}
		if options.LatestPublication != nil {
			if latest, err := options.LatestPublication(ctx); err == nil {
				latest.SameAsSlot = latest.SnapshotRevision == result.Slot.SnapshotRevision
				result.LatestPublication = &latest
				if !latest.SameAsSlot {
					result.SnapshotNote = slotSnapshotNote
				}
			} else {
				out.Limitations = append(out.Limitations, "The latest publication could not be read; slot.snapshot_revision is the publication the Slot's Segment began under, not necessarily the latest.")
			}
		}
		if options.Evidence != nil {
			evidence, err := options.Evidence(ctx, plan.Contract.Slot)
			result.Retained = evidence
			if err != nil {
				result.Retained.Complete = false
				result.Retained.Limitations = append(result.Retained.Limitations, slotFailure(err).Code)
			}
		} else {
			result.Retained.Limitations = []string{"Retained diagnostics are not configured."}
		}
		out.Complete = result.Retained.Complete
		out.Limitations = append(out.Limitations, result.Retained.Limitations...)
		out.Limitations = append(out.Limitations, "Retained lists have rolling limits and expiry; an empty list cannot distinguish uncaptured, evicted and expired evidence. Samples do not contain all original inputs.")
		out.Value = result
		return out
	}
	fields := base()
	fields["contract_digest"] = digest("slot.get返回的历史合同摘要。")
	fields["physical_query_digest"] = digest("选择slot.get返回的一个物理查询。")
	fields["request_digest"] = digest("slot.get预览的实际UQ请求摘要；执行端配置变化需重新预览。")
	fields["series_digest"] = digest("可选，规范化后的固定时序身份；从既有sample或查询结果取得。")
	series := fields["series_digest"]
	series.Source = "slot.get retained.samples[].series_digest 或 slot.query query.series[].series_digest"
	fields["series_digest"] = series
	fields["max_series"] = integer("返回时序上限，默认5；不限制UQ计算范围。", 1, 10)
	fields["max_points"] = integer("所有返回时序的总点数上限，默认100。", 1, 500)
	query := Operation{ID: "slot.query", Summary: "对指定历史Slot的一个物理查询执行有界UQ重查；结果为requery_now。", EvidenceScope: "current_worker_requery", Targetable: true, DefaultOwnerParam: "query_group",
		Fields: fields, Required: []string{"query_group", "evaluation_time", "contract_digest", "physical_query_digest", "request_digest"}, OutputSchema: SchemaOf(SlotQueryResult{}), Limits: limits, Availability: availability}
	query.Run = func(ctx context.Context, p Params) Outcome {
		plan, failure := resolveSlot(ctx, options, p)
		if failure != nil {
			return Outcome{Error: failure}
		}
		slot := slotContext(plan)
		changed := func(code, message string) Outcome {
			return Outcome{Error: &Failure{Code: code, Message: message}, Next: []Call{{Operation: "slot.get", Params: slotParams(slot), Reason: "在实际执行实例重新读取保留合同与请求预览；未执行UQ查询。"}}}
		}
		if slot.ContractDigest != p.String("contract_digest") {
			return changed("slot_contract_changed", "Retained Slot contract differs from the preview; no UQ query executed.")
		}
		var selected *access.PlannedQuery
		for i := range plan.Prepared.Queries {
			if string(plan.Prepared.Queries[i].Spec.Digest) == p.String("physical_query_digest") {
				selected = &plan.Prepared.Queries[i]
				break
			}
		}
		if selected == nil {
			return changed("query_reference_unknown", "Physical query is not part of this Slot contract; no UQ query executed.")
		}
		preview, err := options.UQ.Preview(selected.Spec)
		if err != nil {
			return changed("query_plan_unavailable", "Historical query cannot be rendered by this build.")
		}
		if preview.RequestDigest != p.String("request_digest") {
			return changed("query_request_changed", "Current worker request differs from the preview; no UQ query executed.")
		}
		at := time.Now().UTC()
		data, err := options.UQ.Query(ctx, selected.Spec, uq.DiagnosticSelection{SeriesDigest: p.String("series_digest"), MaxSeries: p.Int("max_series", 5), MaxPoints: p.Int("max_points", 100)})
		out := Outcome{Value: SlotQueryResult{Kind: "requery_now", Slot: slot, PhysicalQueryDigest: selected.Spec.Digest, QueriedAt: at, Query: data}, Complete: data.Complete, Limitations: append([]string{slotBoundary}, data.Limitations...)}
		if err != nil {
			code := "query_unavailable"
			var diagnostic *uq.DiagnosticError
			if errors.As(err, &diagnostic) {
				code = diagnostic.Code
			}
			out.Error = &Failure{Code: code, Message: "Diagnostic UQ query did not complete; inspect retained partial query evidence."}
		}
		return out
	}
	return []Operation{get, query}
}

func resolveSlot(ctx context.Context, options SlotOptions, p Params) (SlotPlan, *Failure) {
	if options.Resolve == nil || options.UQ == nil {
		return SlotPlan{}, &Failure{Code: "operation_unavailable", Message: "Slot diagnostics are not configured."}
	}
	slot := execution.SlotIdentity{QueryGroup: execution.QueryGroupIdentity(p.String("query_group")), EvaluationTime: execution.EvaluationTime(p.Int("evaluation_time", 0))}
	plan, err := options.Resolve(ctx, slot)
	if err != nil {
		return SlotPlan{}, slotFailure(err)
	}
	if plan.Contract.Validate() != nil || plan.Contract.Slot != slot || plan.ObjectDigest == "" || len(plan.Prepared.Queries) == 0 {
		return SlotPlan{}, slotFailure(ErrHistoricalContractUnavailable)
	}
	if len(plan.Prepared.Queries) > 32 {
		return SlotPlan{}, slotFailure(ErrSlotBudgetExceeded)
	}
	return plan, nil
}

// slotNoHoldRecord says what a read hold of zero read from no record rests
// on.
const slotNoHoldRecord = "slot.read_hold_ms is 0 because its Query Group keeps no read hold record: one whose hold has only ever been zero keeps none, and a record lowered to zero lasts 7 days. A record evicted from Redis reads the same; evicted_keys on the state Redis says whether any were."

func slotFailure(err error) *Failure {
	switch {
	case errors.Is(err, ErrHistoricalReadHoldUnknown):
		return &Failure{Code: "historical_read_hold_unknown", Message: "The read hold this Slot was frozen with is not known: its record did not decode or no longer reaches back to it, or there is none and the Slot is older than a record's 7-day lifetime; its contract is not rebuilt with another."}
	case errors.Is(err, ErrHistoricalContractUnavailable):
		return &Failure{Code: "historical_contract_unavailable", Message: "Retained historical Segment, object or exact due plans are unavailable; current strategy is not substituted."}
	case errors.Is(err, ErrSlotBudgetExceeded):
		return &Failure{Code: "budget_exceeded", Message: "Historical evidence exceeds the diagnostic read budget."}
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return &Failure{Code: "request_timeout", Message: "Slot evidence read context ended."}
	default:
		return &Failure{Code: "dependency_unavailable", Message: "Historical evidence dependency could not be read."}
	}
}

func slotContext(plan SlotPlan) SlotContext {
	ref := plan.Contract
	raw, _ := json.Marshal(struct {
		Contract     execution.FrozenExecutionContractRef
		ObjectDigest execution.ObjectDigest
	}{ref, plan.ObjectDigest})
	digest := sha256.Sum256(raw)
	return SlotContext{ref.Slot.QueryGroup, ref.Slot.EvaluationTime, ref.SnapshotRevision, ref.QueryRevision, ref.ScheduleRevision, ref.ScheduleSegmentStart, plan.ObjectDigest, ref.DuePlanSetDigest, hex.EncodeToString(digest[:]),
		ref.ReadHoldMillis, plan.ReadHoldBasis}
}

func slotParams(slot SlotContext) Params {
	return Params{"query_group": string(slot.QueryGroup), "evaluation_time": int64(slot.EvaluationTime)}
}
