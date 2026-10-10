package obevidence

import (
	"context"
	"errors"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
)

// FamilyGapMarker and FamilyNoDataMemory are a Plan's two generation-scoped
// records: the gap marker and the absence memory. Whether they survive from
// one round to the next was a question no read answered -- a write gives the
// key the one-day floor and the load renews it, and for a Plan whose period
// is longer than the floor that is a key gone before the next load -- so the
// reader had the code and a guess.
const (
	FamilyGapMarker    = "gap_marker"
	FamilyNoDataMemory = "no_data_memory"
)

// Plan record kinds: the gap marker, the per-group absence memory every
// build since the change of representation writes, and the whole-record
// one it only reads.
const (
	RecordGapMarker         = "gap_marker"
	RecordNoDataMemory      = "no_data_memory"
	RecordNoDataMemoryWhole = "no_data_memory_whole"
)

// planRecordCommands is what one record costs: TYPE, PTTL, and a size and a
// header read by its kind (STRLEN and GETRANGE, or HLEN and HGET).
const planRecordCommands = 4

// PlanRecords is one Plan's records as the store holds them: the Plan and
// the state generation and piece the keys are named for, from the published
// object, and each record read.
type PlanRecords struct {
	Plan            execution.PlanIdentity    `json:"plan"`
	StateGeneration execution.StateGeneration `json:"state_generation"`
	Shard           *execution.ShardRef       `json:"shard,omitempty"`
	// Status is generation_unknown for a Plan the object names without a
	// state generation -- an object written before the field -- whose keys
	// cannot be named from it; empty otherwise. A Plan that is running carries
	// one: the runtime refuses a Slot whose object and activation disagree.
	Status  string       `json:"status,omitempty"`
	Records []PlanRecord `json:"records"`
}

// PlanRecord is one key: whether it is there, its remaining life, its size and
// what its header says. Status is ok for a record read, missing for no key,
// and the reason otherwise; a missing record is an answer, not a failure.
type PlanRecord struct {
	Kind   string `json:"kind"`
	Key    string `json:"key"`
	Status string `json:"status"`
	Type   string `json:"type,omitempty"`
	// TTLMS is the key's remaining life in milliseconds, Redis's -1 for a key
	// with none, absent when there is no key.
	TTLMS *int64 `json:"ttl_ms,omitempty"`
	// Bytes is a string record's length; Fields a hash record's field count,
	// its header included.
	Bytes  *int64                    `json:"bytes,omitempty"`
	Fields *int64                    `json:"fields,omitempty"`
	Header *state.ObservedPlanRecord `json:"header,omitempty"`
	Reason string                    `json:"reason,omitempty"`
	// ReasonText is the error's own text beside a reason for a read the
	// server did not answer.
	ReasonText string `json:"reason_text,omitempty"`
}

type planRecordRead struct {
	kind string
	key  string
	// hash is the record's stored shape: a hash with a header field, or a
	// string.
	hash      bool
	plan      int
	matches   func(raw []byte) (state.ObservedPlanRecord, bool, error)
	typeCmd   *redis.StatusCmd
	ttlCmd    *redis.DurationCmd
	sizeCmd   *redis.IntCmd
	headerCmd *redis.StringCmd
}

// planRecords reads a Plan's gap marker or absence memory by the Plan as the
// published object names it: the object gives the state generation and the
// piece the keys carry, so the reader brings what strategy.get shows and
// nothing it would have to derive.
func (service *Service) planRecords(ctx context.Context, request StoreRequest) Result {
	binding := service.options.Published
	if !identifier(request.QueryGroup) || !digestID(request.ObjectDigest) || !positiveID(request.StrategyID) ||
		request.GroupID != "" || len(request.Fields) > 0 ||
		(request.Tenant != "" && !identifier(request.Tenant)) || (request.Business != "" && !identifier(request.Business)) {
		return invalid(request.Family, binding)
	}
	if binding.Client == nil || service.options.Catalog == nil || binding.Location.Prefix == "" {
		return result(request.Family, binding, "not_configured")
	}
	objectKey, err := service.options.Catalog.ObservationQueryGroupKey(execution.ObjectDigest(request.ObjectDigest))
	if err != nil {
		return invalid(request.Family, binding)
	}
	r, raw := readOne(ctx, request.Family, binding, objectKey)
	// Read at the object first: a Plan the object does not name has no keys
	// to read, and the status says the object was where it stopped.
	if r.Status != "ok" {
		r.Reason = "published_object"
		r.Complete = false
		return r
	}
	object, err := controlplane.DecodeObservedQueryGroup(raw, execution.ObjectDigest(request.ObjectDigest))
	if err != nil {
		r.Status, r.Complete = "object_corrupt", false
		return r
	}
	if string(object.Identity) != request.QueryGroup {
		r.Status, r.Complete = "identity_mismatch", false
		return r
	}
	var plans []PlanRecords
	var reads []planRecordRead
	for _, plan := range object.Plans {
		if plan.Identity.StrategyID != request.StrategyID || (request.Tenant != "" && plan.Identity.TenantID != request.Tenant) ||
			(request.Business != "" && plan.Identity.BusinessID != request.Business) {
			continue
		}
		index := len(plans)
		plans = append(plans, PlanRecords{Plan: plan.Identity, StateGeneration: plan.StateGeneration, Shard: plan.Shard})
		if plan.StateGeneration == "" {
			plans[index].Status = "generation_unknown"
			continue
		}
		planReads, readErr := planRecordReads(request.Family, binding.Location.Prefix, plan, index)
		if readErr != nil {
			r.Status, r.Complete = "invalid_document", false
			return r
		}
		reads = append(reads, planReads...)
	}
	if len(plans) == 0 {
		r.Status, r.Complete = "plan_not_in_object", false
		return r
	}
	// The object's read and this one share the request's budget.
	commands := r.Limits.Commands + 2 + planRecordCommands*len(reads)
	if commands > MaxCommands {
		r.Status, r.Complete, r.Reason = "budget_exceeded", false, "plans"
		return r
	}
	r.Location.Key = ""
	r.Type = "plan_records"
	r.TTLMS = nil
	complete := true
	if len(reads) > 0 {
		r.Limits.Commands = commands
		answered, bytesRead := readPlanRecords(ctx, binding, reads, plans)
		r.Limits.Bytes += bytesRead
		complete = answered
	}
	r.Status, r.Complete = "ok", complete
	if !complete {
		r.Status = "dependency_unavailable"
	}
	// A Plan whose keys could not be named leaves the answer incomplete.
	for _, read := range plans {
		if read.Status != "" {
			r.Complete = false
		}
	}
	r.Value = plans
	return r
}

// planRecordReads is the keys one Plan's records are under, with the check a
// read header must pass: it names this Plan, generation and piece.
func planRecordReads(family, prefix string, plan controlplane.QueryGroupPlanObject, index int) ([]planRecordRead, error) {
	shard := execution.ShardOf(plan.Shard)
	if family == FamilyGapMarker {
		identity := execution.PlanGapIdentity{Plan: plan.Identity, StateGeneration: plan.StateGeneration, Shard: shard}
		key, err := state.PlanGapKeyV2(prefix, identity)
		if err != nil {
			return nil, err
		}
		return []planRecordRead{{kind: RecordGapMarker, key: key, plan: index, matches: func(raw []byte) (state.ObservedPlanRecord, bool, error) {
			header, named, err := state.ObservedGapMarker(raw)
			return header, named == identity, err
		}}}, nil
	}
	identity := execution.PlanNoDataIdentity{Plan: plan.Identity, StateGeneration: plan.StateGeneration, Shard: shard}
	hashKey, err := state.PlanNoDataHashKeyV2(prefix, identity)
	if err != nil {
		return nil, err
	}
	wholeKey, err := state.PlanNoDataKeyV2(prefix, identity)
	if err != nil {
		return nil, err
	}
	return []planRecordRead{
		{kind: RecordNoDataMemory, key: hashKey, hash: true, plan: index, matches: func(raw []byte) (state.ObservedPlanRecord, bool, error) {
			header, named, err := state.ObservedNoDataHeader(raw)
			return header, named == identity, err
		}},
		{kind: RecordNoDataMemoryWhole, key: wholeKey, plan: index, matches: func(raw []byte) (state.ObservedPlanRecord, bool, error) {
			header, named, err := state.ObservedNoDataWhole(raw)
			return header, named == identity, err
		}},
	}, nil
}

// readPlanRecords is one bounded transaction over every record: presence,
// life, size and header of each read together. It says whether every read
// was answered and how many header bytes came back.
func readPlanRecords(ctx context.Context, binding RedisBinding, reads []planRecordRead, plans []PlanRecords) (bool, int) {
	ctx, cancel := context.WithTimeout(ctx, ReadTimeout)
	defer cancel()
	limit := min(MaxDocumentBytes, MaxBytes/len(reads)-1)
	_, txErr := binding.Client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		for i := range reads {
			read := &reads[i]
			read.typeCmd = pipe.Type(ctx, read.key)
			read.ttlCmd = pipe.PTTL(ctx, read.key)
			if read.hash {
				read.sizeCmd = pipe.HLen(ctx, read.key)
				read.headerCmd = pipe.HGet(ctx, read.key, state.NoDataHeaderField)
			} else {
				read.sizeCmd = pipe.StrLen(ctx, read.key)
				read.headerCmd = pipe.GetRange(ctx, read.key, 0, int64(limit))
			}
		}
		return nil
	})
	complete, bytesRead, unanswered := true, 0, ""
	for _, read := range reads {
		record := PlanRecord{Kind: read.kind, Key: read.key, Status: "dependency_unavailable"}
		if read.typeCmd.Err() != nil || read.ttlCmd.Err() != nil {
			record.Reason, record.ReasonText = failureReason(txErr, read.typeCmd, read.ttlCmd)
			unanswered, complete = record.Reason, false
			plans[read.plan].Records = append(plans[read.plan].Records, record)
			continue
		}
		record.Type = read.typeCmd.Val()
		if record.Type == "none" {
			record.Status = "missing"
			plans[read.plan].Records = append(plans[read.plan].Records, record)
			continue
		}
		ttl := read.ttlCmd.Val()
		ttlMS := ttl.Milliseconds()
		if ttl < 0 {
			ttlMS = int64(ttl)
		}
		record.TTLMS = &ttlMS
		if (read.hash && record.Type != "hash") || (!read.hash && record.Type != "string") {
			record.Status = "wrong_type"
			plans[read.plan].Records = append(plans[read.plan].Records, record)
			continue
		}
		header := read.headerCmd
		if read.sizeCmd.Err() != nil || (header.Err() != nil && !errors.Is(header.Err(), redis.Nil)) {
			record.Reason, record.ReasonText = failureReason(txErr, read.sizeCmd, header)
			unanswered, complete = record.Reason, false
			plans[read.plan].Records = append(plans[read.plan].Records, record)
			continue
		}
		size := read.sizeCmd.Val()
		if read.hash {
			record.Fields = &size
		} else {
			record.Bytes = &size
		}
		raw := []byte(header.Val())
		bytesRead += len(raw)
		switch observed, named, err := read.matches(raw); {
		case !read.hash && len(raw) > limit:
			record.Status, record.Reason = "budget_exceeded", "document_bytes"
		case err != nil:
			record.Status = "invalid_document"
		case !named:
			record.Status = "identity_mismatch"
		default:
			record.Status, record.Header = "ok", &observed
		}
		plans[read.plan].Records = append(plans[read.plan].Records, record)
	}
	binding.failed(unanswered)
	return complete, bytesRead
}
