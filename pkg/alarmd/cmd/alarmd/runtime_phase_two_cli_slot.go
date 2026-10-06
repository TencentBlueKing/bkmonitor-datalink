package main

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/access"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/obchannel"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

const (
	cliSlotDocumentBytes = 1 << 20
	cliSlotReadBytes     = 4 << 20
	cliSlotReadCommands  = 64
	cliSlotRecordLimit   = 200
	cliSlotSampleLimit   = 50
)

// Every invocation owns its repository, compiler and read budget. No diagnostic
// request warms, invalidates or reports the production Worker's installed view.
func newCLISlotResolver(cfg config.Config, client redis.Cmdable) func(context.Context, execution.SlotIdentity) (obchannel.SlotPlan, error) {
	return func(ctx context.Context, slot execution.SlotIdentity) (obchannel.SlotPlan, error) {
		ctx, cancel := context.WithTimeout(ctx, obchannel.RequestTimeout)
		defer cancel()
		if client == nil {
			return obchannel.SlotPlan{}, obchannel.ErrSlotDependencyUnavailable
		}
		if slot.EvaluationTime <= 0 || fleet.ValidateQueryGroup(string(slot.QueryGroup)) != nil {
			return obchannel.SlotPlan{}, obchannel.ErrHistoricalContractUnavailable
		}
		reader := newCLISlotRedis(client)
		repository, err := controlplane.NewRedisCatalogRepository(reader, productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "catalog"), phaseTwoCatalogRetention(cfg))
		if err != nil {
			return obchannel.SlotPlan{}, obchannel.ErrHistoricalContractUnavailable
		}
		compiler, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), cfg.CompilerLimits())
		if err != nil {
			return obchannel.SlotPlan{}, obchannel.ErrHistoricalContractUnavailable
		}
		semantics, err := state.RuntimeStateSemantics()
		if err != nil {
			return obchannel.SlotPlan{}, obchannel.ErrHistoricalContractUnavailable
		}
		runtime, err := controlplane.NewRedisCatalogRuntime(repository, compiler, strategy.StateSemantics{
			StateSchemaVersion: semantics.StateSchemaVersion, CodecSemanticsVersion: semantics.CodecSemanticsVersion,
			IdentitySchemaDigest: semantics.IdentitySchemaDigest, SourceTimeSemanticsVersion: semantics.SourceTimeSemanticsVersion,
			HistoryCellSemanticsVersion: semantics.HistoryCellSemanticsVersion,
		}, cfg.PhaseTwo.Access.DownstreamExecutionReserve.Duration())
		if err != nil {
			return obchannel.SlotPlan{}, obchannel.ErrHistoricalContractUnavailable
		}
		schedule, err := runtime.ReadFrozenSchedule(ctx, slot.QueryGroup, slot.EvaluationTime)
		if err != nil {
			return obchannel.SlotPlan{}, cliSlotReadError(err, reader)
		}
		if schedule.Segment.ObjectDigest == "" {
			return obchannel.SlotPlan{}, obchannel.ErrHistoricalContractUnavailable
		}
		fact, err := runtime.FreezeObservedSlotContract(ctx, execution.FreezeSlotContractRequest{
			QueryGroup: slot.QueryGroup, EvaluationTime: slot.EvaluationTime, ScheduleRevision: schedule.Segment.ScheduleRevision,
			ScheduleSegmentStart: schedule.Segment.Start, DuePlans: schedule.DuePlanRefs(slot.EvaluationTime),
		})
		if err != nil {
			return obchannel.SlotPlan{}, cliSlotReadError(err, reader)
		}
		resolver, err := newProductionFrozenExecution(cliObservedCatalog{runtime}, cliObservedRepository{repository}, time.Now)
		if err != nil {
			return obchannel.SlotPlan{}, obchannel.ErrHistoricalContractUnavailable
		}
		frozen, err := resolver.ResolveFrozenPlan(ctx, fact.Contract)
		if err != nil {
			return obchannel.SlotPlan{}, cliSlotReadError(err, reader)
		}
		prepared, err := access.Prepare(fact.Contract, frozen, cfg.PhaseTwo.Access.MinReadyDelay.Duration())
		if err != nil {
			return obchannel.SlotPlan{}, obchannel.ErrHistoricalContractUnavailable
		}
		if err := ctx.Err(); err != nil {
			return obchannel.SlotPlan{}, obchannel.ErrSlotDependencyUnavailable
		}
		return obchannel.SlotPlan{Contract: fact.Contract, ObjectDigest: schedule.Segment.ObjectDigest, Prepared: prepared}, nil
	}
}

type cliObservedCatalog struct {
	*controlplane.RedisCatalogRuntime
}

func (c cliObservedCatalog) FreezeSlotContract(ctx context.Context, request execution.FreezeSlotContractRequest) (execution.FrozenSlotContractFact, error) {
	return c.FreezeObservedSlotContract(ctx, request)
}

type cliObservedRepository struct {
	*controlplane.RedisCatalogRepository
}

func (r cliObservedRepository) LoadSegmentQueryGroup(ctx context.Context, segment execution.ScheduleSegmentFact, at execution.EvaluationTime, _ func(context.Context) (controlplane.QueryGroup, error)) (controlplane.QueryGroup, error) {
	return r.LoadObservedSegmentQueryGroup(ctx, segment, at)
}

func newCLISlotEvidenceReader(cfg config.Config, client redis.Cmdable) func(context.Context, execution.SlotIdentity) (obchannel.SlotEvidence, error) {
	return func(ctx context.Context, slot execution.SlotIdentity) (obchannel.SlotEvidence, error) {
		ctx, cancel := context.WithTimeout(ctx, obchannel.RequestTimeout)
		defer cancel()
		out := obchannel.SlotEvidence{Records: []json.RawMessage{}, Samples: []json.RawMessage{}, Complete: true,
			Limitations: []string{"Only the newest 200 lifecycle records and 50 samples are searched. Retention is one hour after the list's last write; older rows may have been evicted. Missing records do not prove the Slot did not execute.", "Samples are selected provisional calculation facts, not a complete input archive or proof of output acknowledgement."}}
		if client == nil {
			out.Complete = false
			return out, obchannel.ErrSlotDependencyUnavailable
		}
		if slot.EvaluationTime <= 0 || fleet.ValidateQueryGroup(string(slot.QueryGroup)) != nil {
			out.Complete = false
			return out, obchannel.ErrHistoricalContractUnavailable
		}
		reader := newCLISlotRedis(client)
		records, samples, err := fleet.LoadRetainedDiagnostics(ctx, reader, productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "fleet"), string(slot.QueryGroup), cliSlotRecordLimit, cliSlotSampleLimit)
		if err != nil {
			out.Complete = false
			return out, cliSlotReadError(err, reader)
		}
		if len(records) == cliSlotRecordLimit || len(samples) == cliSlotSampleLimit {
			out.Complete = false
			out.Limitations = append(out.Limitations, "A retained-list read reached its search limit; additional older rows may exist.")
		}
		for _, raw := range records {
			var row struct {
				QueryGroup string `json:"query_group_key"`
				Slot       int64  `json:"evaluation_time"`
				Known      bool   `json:"slot_identity_known"`
			}
			if json.Unmarshal(raw, &row) != nil {
				out.Complete = false
				continue
			}
			if row.Known && row.QueryGroup == string(slot.QueryGroup) && row.Slot == int64(slot.EvaluationTime) {
				out.Records = append(out.Records, raw)
			}
		}
		for _, raw := range samples {
			var row struct {
				Kind       string `json:"kind"`
				QueryGroup string `json:"query_group"`
				Slot       int64  `json:"slot"`
			}
			if json.Unmarshal(raw, &row) != nil {
				out.Complete = false
				continue
			}
			if row.Kind == "series_sample" && row.QueryGroup == string(slot.QueryGroup) && row.Slot == int64(slot.EvaluationTime) {
				out.Samples = append(out.Samples, raw)
			}
		}
		if !out.Complete {
			out.Limitations = append(out.Limitations, "The retained read may be incomplete; malformed records, if any, were omitted.")
		}
		return out, nil
	}
}

// cliSlotRedis bounds the reads used by the retained Catalog path before any
// JSON/digest work. Embedded Cmdable is deliberately nil: only these read
// methods are available to this adapter, not the supplied client's writers.
// Catalog pipelines use immediate bounded reads, never a production pipeline.
type cliSlotRedis struct {
	redis.Cmdable
	client          redis.Cmdable
	commands, bytes int
	failure         error
}

func newCLISlotRedis(client redis.Cmdable) *cliSlotRedis { return &cliSlotRedis{client: client} }
func (r *cliSlotRedis) admit(ctx context.Context, count int) error {
	if r.failure != nil {
		return r.failure
	}
	if ctx.Err() != nil {
		r.failure = obchannel.ErrSlotDependencyUnavailable
		return r.failure
	}
	if r.commands+count > cliSlotReadCommands || r.bytes >= cliSlotReadBytes {
		r.failure = obchannel.ErrSlotBudgetExceeded
		return r.failure
	}
	r.commands += count
	return nil
}
func (r *cliSlotRedis) failed(err error) error {
	if err != nil && !errors.Is(err, redis.Nil) && r.failure == nil {
		r.failure = obchannel.ErrSlotDependencyUnavailable
	}
	return err
}
func (r *cliSlotRedis) Get(ctx context.Context, key string) *redis.StringCmd {
	cmd := redis.NewStringCmd(ctx, "get", key)
	if err := r.admit(ctx, 1); err != nil {
		cmd.SetErr(err)
		return cmd
	}
	limit := min(cliSlotDocumentBytes, cliSlotReadBytes-r.bytes)
	value, err := r.client.GetRange(ctx, key, 0, int64(limit)).Result()
	if err != nil {
		cmd.SetErr(r.failed(err))
		return cmd
	}
	if len(value) > limit {
		r.failure = obchannel.ErrSlotBudgetExceeded
		cmd.SetErr(r.failure)
		return cmd
	}
	r.bytes += len(value)
	if value == "" {
		present, err := r.Exists(ctx, key).Result()
		if err != nil {
			cmd.SetErr(err)
			return cmd
		}
		if present == 0 {
			cmd.SetErr(redis.Nil)
			return cmd
		}
	}
	cmd.SetVal(value)
	return cmd
}
func (r *cliSlotRedis) Exists(ctx context.Context, keys ...string) *redis.IntCmd {
	cmd := redis.NewIntCmd(ctx, "exists")
	if err := r.admit(ctx, 1); err != nil {
		cmd.SetErr(err)
		return cmd
	}
	v, err := r.client.Exists(ctx, keys...).Result()
	cmd.SetVal(v)
	cmd.SetErr(r.failed(err))
	return cmd
}
func (r *cliSlotRedis) StrLen(ctx context.Context, key string) *redis.IntCmd {
	cmd := redis.NewIntCmd(ctx, "strlen", key)
	if err := r.admit(ctx, 1); err != nil {
		cmd.SetErr(err)
		return cmd
	}
	v, err := r.client.StrLen(ctx, key).Result()
	cmd.SetVal(v)
	cmd.SetErr(r.failed(err))
	return cmd
}

type cliSlotPipeline struct {
	redis.Pipeliner
	reader   *cliSlotRedis
	commands []redis.Cmder
}

func (p *cliSlotPipeline) Get(ctx context.Context, key string) *redis.StringCmd {
	c := p.reader.Get(ctx, key)
	p.commands = append(p.commands, c)
	return c
}
func (p *cliSlotPipeline) StrLen(ctx context.Context, key string) *redis.IntCmd {
	c := p.reader.StrLen(ctx, key)
	p.commands = append(p.commands, c)
	return c
}
func (r *cliSlotRedis) Pipelined(ctx context.Context, fn func(redis.Pipeliner) error) ([]redis.Cmder, error) {
	// Unknown operations queue on a real, never-executed pipeline. They are
	// refused below; they cannot accidentally issue a write.
	p := &cliSlotPipeline{Pipeliner: r.client.Pipeline(), reader: r}
	defer p.Close()
	if err := fn(p); err != nil {
		return nil, err
	}
	if p.Len() != 0 {
		return nil, obchannel.ErrSlotDependencyUnavailable
	}
	for _, cmd := range p.commands {
		if cmd.Err() != nil {
			return p.commands, cmd.Err()
		}
	}
	return p.commands, nil
}

// The read-only script bounds list bytes before Redis sends them. Lifecycle and
// sample producers both have a 4 KiB record contract. No key is changed/renewed.
const cliSlotListRead = `local rows=redis.call('LRANGE',KEYS[1],ARGV[1],ARGV[2])
local n=0
for _,v in ipairs(rows) do
  n=n+string.len(v)
  if string.len(v)>tonumber(ARGV[4]) or n>tonumber(ARGV[3]) then return {0} end
end
return {1,rows}`

func (r *cliSlotRedis) LRange(ctx context.Context, key string, start, stop int64) *redis.StringSliceCmd {
	cmd := redis.NewStringSliceCmd(ctx, "lrange", key, start, stop)
	if err := r.admit(ctx, 2); err != nil {
		cmd.SetErr(err)
		return cmd
	}
	if start != 0 || stop < 0 || stop >= cliSlotRecordLimit {
		cmd.SetErr(obchannel.ErrSlotBudgetExceeded)
		return cmd
	}
	value, err := r.client.Eval(ctx, cliSlotListRead, []string{key}, start, stop, min(cliSlotDocumentBytes, cliSlotReadBytes-r.bytes), observability.SeriesSampleMaxBytes).Slice()
	if err != nil {
		cmd.SetErr(r.failed(err))
		return cmd
	}
	if len(value) == 1 {
		r.failure = obchannel.ErrSlotBudgetExceeded
		cmd.SetErr(r.failure)
		return cmd
	}
	if len(value) != 2 {
		cmd.SetErr(obchannel.ErrSlotDependencyUnavailable)
		return cmd
	}
	rows, ok := value[1].([]interface{})
	if !ok {
		cmd.SetErr(obchannel.ErrSlotDependencyUnavailable)
		return cmd
	}
	result := make([]string, 0, len(rows))
	for _, row := range rows {
		v, ok := row.(string)
		if !ok {
			cmd.SetErr(obchannel.ErrSlotDependencyUnavailable)
			return cmd
		}
		r.bytes += len(v)
		result = append(result, v)
	}
	cmd.SetVal(result)
	return cmd
}

func cliSlotReadError(err error, reader *cliSlotRedis) error {
	if reader.failure != nil {
		return reader.failure
	}
	if errors.Is(err, obchannel.ErrSlotBudgetExceeded) {
		return obchannel.ErrSlotBudgetExceeded
	}
	if errors.Is(err, obchannel.ErrSlotDependencyUnavailable) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return obchannel.ErrSlotDependencyUnavailable
	}
	return obchannel.ErrHistoricalContractUnavailable
}
