package obevidence

import (
	"context"
	"errors"
	"sort"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/redisfailure"
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

// Plan record kinds: the gap marker and the per-group absence memory.
const (
	RecordGapMarker    = "gap_marker"
	RecordNoDataMemory = "no_data_memory"
)

// planRecordCommands is what one record costs: TYPE, PTTL, and a size and a
// header read by its kind (STRLEN and GETRANGE, or HLEN and HGET).
const planRecordCommands = 4

// A no-data memory's group keys are listed, not only counted: the first
// MaxGroupKeys of them in byte order, together at most MaxGroupKeyBytes. The
// whole hash is scanned to find them, groupKeyScanCount fields a page, within
// what is left of the request's command and byte budget.
const (
	MaxGroupKeys      = 50
	MaxGroupKeyBytes  = 16 << 10
	groupKeyScanCount = 1000
)

// Group key scan outcomes beside a Redis failure reason: every field seen, or
// the request budget that stopped the scan first.
const (
	GroupKeyScanComplete = "complete"
	GroupKeyScanCommands = "commands"
	GroupKeyScanBytes    = "bytes"
)

// Why a listing holds fewer keys than the scan saw.
const (
	GroupKeysCutByCount = "count"
	GroupKeysCutByBytes = "bytes"
)

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
	// GroupKeys lists a no-data memory's groups by name; absent for a record
	// that is not a hash.
	GroupKeys *GroupKeyListing `json:"group_keys,omitempty"`
	Reason    string           `json:"reason,omitempty"`
	// ReasonText is the error's own text beside a reason for a read the
	// server did not answer.
	ReasonText string `json:"reason_text,omitempty"`
}

// GroupKeyListing is the first group keys of a no-data memory in byte order.
// Keys are the first of every group only when Scan is complete; otherwise
// they are the first of the ScannedFields fields the scan saw before Scan's
// bound or failure, not of the record.
type GroupKeyListing struct {
	Keys []string `json:"keys"`
	// Total is how many groups the record holds: its field count without the
	// header field.
	Total int64 `json:"total"`
	// Limit and LimitBytes are the listing's own bounds; CutBy says which one
	// left out keys the scan saw, empty when it listed all of them.
	Limit      int    `json:"limit"`
	LimitBytes int    `json:"limit_bytes"`
	CutBy      string `json:"cut_by,omitempty"`
	Scan       string `json:"scan"`
	ScanText   string `json:"scan_text,omitempty"`
	// ScannedFields counts the fields the scan returned, the header and any
	// repeat a scan may return included.
	ScannedFields int64 `json:"scanned_fields"`
	// UnknownFields counts fields that are neither the header nor a group,
	// which the runtime reads as a corrupt record.
	UnknownFields int `json:"unknown_fields,omitempty"`
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
	commands := r.Limits.Commands + planRecordCommands*len(reads)
	if commands > MaxCommands {
		r.Status, r.Complete, r.Reason = "too_many_keys", false, "plans"
		return r
	}
	r.Location.Key = ""
	r.Type = "plan_records"
	r.TTLMS = nil
	complete := true
	if len(reads) > 0 {
		answered, commandsUsed, bytesRead := readPlanRecords(ctx, binding, reads, plans, MaxCommands-commands, MaxBytes-r.Limits.Bytes)
		r.Limits.Commands = commands + commandsUsed
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
	return []planRecordRead{
		{kind: RecordNoDataMemory, key: hashKey, hash: true, plan: index, matches: func(raw []byte) (state.ObservedPlanRecord, bool, error) {
			header, named, err := state.ObservedNoDataHeader(raw)
			return header, named == identity, err
		}},
	}, nil
}

// readPlanRecords is one bounded pipeline over every record: presence, life,
// size and header of each read in one round trip, not as a point-in-time
// snapshot (Limits.Consistency; readMany says why it is not a MULTI). It says whether every read
// was answered, and the commands and bytes it spent past the pipeline's own
// commands: header bytes, and the group key scans within commandsLeft and
// bytesLeft.
func readPlanRecords(ctx context.Context, binding RedisBinding, reads []planRecordRead, plans []PlanRecords, commandsLeft, bytesLeft int) (bool, int, int) {
	ctx, cancel := context.WithTimeout(ctx, ReadTimeout)
	defer cancel()
	limit := min(MaxDocumentBytes, MaxBytes/len(reads)-1)
	_, pipeErr := binding.Client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
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
	complete, commandsUsed, bytesRead, unanswered := true, 0, 0, ""
	for _, read := range reads {
		record := PlanRecord{Kind: read.kind, Key: read.key, Status: "dependency_unavailable"}
		if read.typeCmd.Err() != nil || read.ttlCmd.Err() != nil {
			record.Reason, record.ReasonText = failureReason(pipeErr, read.typeCmd, read.ttlCmd)
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
			record.Reason, record.ReasonText = failureReason(pipeErr, read.sizeCmd, header)
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
		if read.hash {
			groups := size
			if !errors.Is(header.Err(), redis.Nil) {
				groups--
			}
			listing, used, scanned := scanGroupKeys(ctx, binding, read.key, groups, commandsLeft-commandsUsed, bytesLeft-bytesRead)
			commandsUsed += used
			bytesRead += scanned
			record.GroupKeys = &listing
		}
		switch observed, named, err := read.matches(raw); {
		case !read.hash && len(raw) > limit:
			record.Status, record.Reason = "document_too_large", "document_bytes"
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
	return complete, commandsUsed, bytesRead
}

// scanGroupKeys pages through one no-data memory's fields with HSCAN and
// keeps the first MaxGroupKeys group keys in byte order. It stops before a
// page the command budget has no room for, and after the page that spends
// the last of the byte budget. It returns the listing, the commands it sent
// and the bytes the pages carried.
func scanGroupKeys(ctx context.Context, binding RedisBinding, key string, total int64, commandsLeft, bytesLeft int) (GroupKeyListing, int, int) {
	listing := GroupKeyListing{Keys: []string{}, Total: total, Limit: MaxGroupKeys, LimitBytes: MaxGroupKeyBytes}
	var first []string
	cut := false
	commands, bytesRead := 0, 0
	cursor := uint64(0)
	for {
		if commands >= commandsLeft {
			listing.Scan = GroupKeyScanCommands
			break
		}
		if bytesRead >= bytesLeft {
			listing.Scan = GroupKeyScanBytes
			break
		}
		commands++
		page, next, err := binding.Client.HScan(ctx, key, cursor, "", groupKeyScanCount).Result()
		if err != nil {
			listing.Scan, listing.ScanText = redisfailure.Reason(err), redisfailure.Detail(err)
			binding.failed(listing.Scan)
			break
		}
		for i := 0; i+1 < len(page); i += 2 {
			field := page[i]
			bytesRead += len(field) + len(page[i+1])
			listing.ScannedFields++
			if field == state.NoDataHeaderField {
				continue
			}
			group, ok := state.ObservedNoDataGroupKey(field)
			if !ok {
				listing.UnknownFields++
				continue
			}
			at := sort.SearchStrings(first, group)
			if at < len(first) && first[at] == group {
				continue
			}
			if at >= MaxGroupKeys {
				cut = true
				continue
			}
			first = append(first, "")
			copy(first[at+1:], first[at:])
			first[at] = group
			if len(first) > MaxGroupKeys {
				first = first[:MaxGroupKeys]
				cut = true
			}
		}
		cursor = next
		if cursor == 0 {
			listing.Scan = GroupKeyScanComplete
			break
		}
	}
	if cut {
		listing.CutBy = GroupKeysCutByCount
	}
	listed := 0
	for _, group := range first {
		if listed+len(group) > MaxGroupKeyBytes {
			listing.CutBy = GroupKeysCutByBytes
			break
		}
		listed += len(group)
		listing.Keys = append(listing.Keys, group)
	}
	return listing, commands, bytesRead
}
