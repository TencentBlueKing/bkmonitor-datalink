// Package obevidence provides bounded, read-only evidence for the authenticated
// OB channel. It owns no refresh loop, cache, executor or arbitrary Redis API.
package obevidence

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/cmdbcache"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/platformsettings"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/progress"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

const (
	MaxCommands          = 32
	MaxDocumentBytes     = 256 << 10
	MaxBytes             = 1 << 20
	ReadTimeout          = 2 * time.Second
	FamilySourceStrategy = "source_strategy"
	FamilyTargetGroup    = "target_group"
	FamilyDynamicConfig  = "dynamic_config"
	FamilyQueryProgress  = "query_progress"
	FamilyQueryCooldown  = "query_cooldown"
)

type Location struct {
	Role    string `json:"role"`
	Address string `json:"address"`
	Mode    string `json:"mode,omitempty"`
	DB      int    `json:"db"`
	Prefix  string `json:"prefix"`
	Key     string `json:"key,omitempty"`
}

// Client is an existing small diagnostic connection to this configured role.
// A nil client means not configured, never that the key was absent.
type RedisBinding struct {
	Client   redis.Cmdable
	Location Location
}
type Options struct {
	SourceStrategy, TargetGroup, DynamicConfig, QueryProgress, Published RedisBinding
	// QueryCooldown is the runtime store under the pool records' prefix: the
	// record a Query Group's owner keeps of its place in the demoted pool.
	QueryCooldown RedisBinding
	// CMDBCache is the platform's host cache, read here only for INFO.
	CMDBCache RedisBinding
	Catalog   *controlplane.RedisCatalogRepository
	Progress  *progress.Store
}
type Service struct{ options Options }

func New(options Options) *Service { return &Service{options: options} }

type StoreRequest struct {
	Family     string                   `json:"family"`
	StrategyID string                   `json:"strategy_id,omitempty"`
	GroupID    string                   `json:"group_id,omitempty"`
	QueryGroup string                   `json:"query_group,omitempty"`
	Fields     []platformsettings.Field `json:"fields,omitempty"`
}
type ConfigRequest struct {
	View         string `json:"view"`
	StrategyID   string `json:"strategy_id"`
	Tenant       string `json:"tenant,omitempty"`
	Business     string `json:"business,omitempty"`
	QueryGroup   string `json:"query_group,omitempty"`
	ObjectDigest string `json:"object_digest,omitempty"`
}
type Omission struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}
type Limits struct {
	// Dynamic-config child records share these request totals; they are not
	// independent costs and must not be summed.
	Scope                  string `json:"scope"`
	MaxCommands            int    `json:"max_commands"`
	MaxDocumentBytes       int    `json:"max_document_bytes"`
	DocumentReadLimitBytes int    `json:"document_read_limit_bytes"`
	MaxBytes               int    `json:"max_bytes"`
	DeadlineMS             int64  `json:"deadline_ms"`
	Commands               int    `json:"commands"`
	Bytes                  int    `json:"bytes"`
}
type Result struct {
	Status string `json:"status"`
	Source string `json:"source"`
	// ReadAt is the end of an attempted Redis read; nil means no read was
	// attempted. It is never the writer's time or proof of runtime adoption.
	ReadAt   *time.Time `json:"read_at"`
	Location Location   `json:"location"`
	Type     string     `json:"type"`
	TTLMS    *int64     `json:"ttl_ms"`
	Complete bool       `json:"complete"`
	Omitted  []Omission `json:"omitted"`
	Limits   Limits     `json:"limits"`
	Value    any        `json:"value"`
	Reason   string     `json:"reason,omitempty"`
}

func result(source string, binding RedisBinding, status string) Result {
	return Result{Status: status, Source: source, Location: binding.Location,
		Type: "unknown", Omitted: []Omission{}, Limits: Limits{Scope: "request", MaxCommands: MaxCommands,
			MaxDocumentBytes: MaxDocumentBytes, MaxBytes: MaxBytes, DeadlineMS: ReadTimeout.Milliseconds()}}
}
func invalid(source string, binding RedisBinding) Result {
	return result(source, binding, "invalid_input")
}
func positiveID(id string) bool {
	n, err := strconv.ParseUint(id, 10, 64)
	return err == nil && n > 0 && strconv.FormatUint(n, 10) == id
}
func identifier(id string) bool {
	if len(id) == 0 || len(id) > 512 {
		return false
	}
	for _, c := range id {
		if c <= ' ' || c == 127 {
			return false
		}
	}
	return true
}
func digestID(id string) bool { b, e := hex.DecodeString(id); return e == nil && len(b) == 32 }

func (service *Service) Store(ctx context.Context, request StoreRequest) Result {
	if service == nil {
		return result(request.Family, RedisBinding{}, "not_configured")
	}
	switch request.Family {
	case FamilySourceStrategy:
		binding := service.options.SourceStrategy
		if !positiveID(request.StrategyID) || request.GroupID != "" || request.QueryGroup != "" || len(request.Fields) > 0 {
			return invalid(request.Family, binding)
		}
		if binding.Client == nil {
			return result(request.Family, binding, "not_configured")
		}
		source, err := controlplane.NewLegacyRedisStrategySource(binding.Client, binding.Location.Prefix)
		if err != nil {
			return result(request.Family, binding, "not_configured")
		}
		key, err := source.ObservationDocumentKey(request.StrategyID)
		if err != nil {
			return invalid(request.Family, binding)
		}
		r, raw := readOne(ctx, request.Family, binding, key)
		if r.Status == "ok" {
			r.Value, r.Omitted, err = projectJSON(raw, sourcePolicy)
			if err != nil {
				r.Status = "invalid_document"
				r.Complete = false
			}
		}
		return r
	case FamilyTargetGroup:
		binding := service.options.TargetGroup
		if !identifier(request.GroupID) || request.StrategyID != "" || request.QueryGroup != "" || len(request.Fields) > 0 {
			return invalid(request.Family, binding)
		}
		if binding.Client == nil {
			return result(request.Family, binding, "not_configured")
		}
		reader, err := cmdbcache.NewGroupReader(binding.Client, binding.Location.Prefix)
		if err != nil {
			return result(request.Family, binding, "not_configured")
		}
		r, raw := readOne(ctx, request.Family, binding, reader.ObservationDocumentKey(request.GroupID))
		if r.Status == "ok" {
			r.Value, r.Omitted, err = projectJSON(raw, groupPolicy)
			if err != nil {
				r.Status = "invalid_document"
				r.Complete = false
			}
		}
		return r
	case FamilyDynamicConfig:
		return service.dynamicConfig(ctx, request)
	case FamilyQueryProgress:
		binding := service.options.QueryProgress
		if !identifier(request.QueryGroup) || request.StrategyID != "" || request.GroupID != "" || len(request.Fields) > 0 {
			return invalid(request.Family, binding)
		}
		if binding.Client == nil || service.options.Progress == nil {
			return result(request.Family, binding, "not_configured")
		}
		key, err := service.options.Progress.ObservationKey(execution.QueryGroupIdentity(request.QueryGroup))
		if err != nil {
			return result(request.Family, binding, "not_configured")
		}
		r, raw := readOne(ctx, request.Family, binding, key)
		if r.Status == "ok" {
			value, err := progress.DecodeObserved(raw)
			if err != nil {
				r.Status = "invalid_document"
				r.Complete = false
				return r
			}
			if value.Identity.QueryGroup != execution.QueryGroupIdentity(request.QueryGroup) {
				r.Status = "identity_mismatch"
				r.Complete = false
				return r
			}
			// The validated persisted model contains only execution facts, not
			// connection configuration or an untyped source document.
			r.Value = value
		}
		return r
	case FamilyQueryCooldown:
		return service.queryCooldown(ctx, request)
	default:
		return invalid(request.Family, RedisBinding{})
	}
}

// queryCooldown reads a Query Group's pool record as its owner wrote it. The
// pool rows object.list serves are the owner's memory; this is what a
// restart or a new owner would read back, and absent here means the next
// owner starts the Query Group outside the pool.
func (service *Service) queryCooldown(ctx context.Context, request StoreRequest) Result {
	binding := service.options.QueryCooldown
	if !identifier(request.QueryGroup) || request.StrategyID != "" || request.GroupID != "" || len(request.Fields) > 0 {
		return invalid(request.Family, binding)
	}
	if binding.Client == nil || binding.Location.Prefix == "" {
		return result(request.Family, binding, "not_configured")
	}
	queryGroup := execution.QueryGroupIdentity(request.QueryGroup)
	r, raw := readOne(ctx, request.Family, binding, scheduler.QueryCooldownKey(binding.Location.Prefix, queryGroup))
	if r.Status != "ok" {
		return r
	}
	var record scheduler.QueryCooldownRecord
	if json.Unmarshal(raw, &record) != nil {
		r.Status = "invalid_document"
		r.Complete = false
		return r
	}
	if record.QueryGroup != queryGroup {
		r.Status = "identity_mismatch"
		r.Complete = false
		return r
	}
	r.Value = record
	return r
}

func (service *Service) StrategyConfig(ctx context.Context, request ConfigRequest) Result {
	if service == nil {
		return result("strategy_config", RedisBinding{}, "not_configured")
	}
	if !positiveID(request.StrategyID) {
		return invalid("strategy_config", RedisBinding{})
	}
	if request.View == "source" {
		if request.ObjectDigest != "" || request.QueryGroup != "" || request.Tenant != "" || request.Business != "" {
			return invalid("source_strategy", service.options.SourceStrategy)
		}
		return service.Store(ctx, StoreRequest{Family: FamilySourceStrategy, StrategyID: request.StrategyID})
	}
	binding := service.options.Published
	if request.View != "published" || !digestID(request.ObjectDigest) || !identifier(request.QueryGroup) {
		return invalid("published_object", binding)
	}
	if binding.Client == nil || service.options.Catalog == nil {
		return result("published_object", binding, "not_configured")
	}
	key, err := service.options.Catalog.ObservationQueryGroupKey(execution.ObjectDigest(request.ObjectDigest))
	if err != nil {
		return invalid("published_object", binding)
	}
	r, raw := readOne(ctx, "published_object", binding, key)
	if r.Status != "ok" {
		return r
	}
	object, err := controlplane.DecodeObservedQueryGroup(raw, execution.ObjectDigest(request.ObjectDigest))
	if err != nil {
		r.Status = "object_corrupt"
		r.Complete = false
		return r
	}
	if string(object.Identity) != request.QueryGroup {
		r.Status = "identity_mismatch"
		r.Complete = false
		return r
	}
	var document map[string]json.RawMessage
	var rawPlans []json.RawMessage
	if json.Unmarshal(raw, &document) != nil || json.Unmarshal(document["plans"], &rawPlans) != nil || len(rawPlans) != len(object.Plans) {
		r.Status = "invalid_document"
		r.Complete = false
		return r
	}
	plans := make([]json.RawMessage, 0, len(object.Plans))
	for index, plan := range object.Plans {
		if plan.Identity.StrategyID == request.StrategyID && (request.Tenant == "" || plan.Identity.TenantID == request.Tenant) && (request.Business == "" || plan.Identity.BusinessID == request.Business) {
			plans = append(plans, rawPlans[index])
		}
	}
	if len(plans) == 0 {
		r.Status = "plan_not_in_object"
		r.Complete = false
		return r
	}
	document["plans"], _ = json.Marshal(plans)
	encoded, err := json.Marshal(document)
	if err != nil {
		r.Status = "invalid_document"
		r.Complete = false
		return r
	}
	r.Value, r.Omitted, err = projectJSON(encoded, publishedPolicy)
	if err != nil {
		r.Status = "invalid_document"
		r.Complete = false
	}
	return r
}

func (service *Service) dynamicConfig(ctx context.Context, request StoreRequest) Result {
	binding := service.options.DynamicConfig
	if request.StrategyID != "" || request.GroupID != "" || request.QueryGroup != "" || len(request.Fields) > len(platformsettings.Fields) {
		return invalid(request.Family, binding)
	}
	fields := request.Fields
	if len(fields) == 0 {
		fields = platformsettings.Fields
	}
	seen := map[platformsettings.Field]bool{}
	for _, field := range fields {
		if !platformsettings.ValidField(field) || seen[field] {
			return invalid(request.Family, binding)
		}
		seen[field] = true
	}
	if binding.Client == nil || platformsettings.ValidateKeyPrefix(binding.Location.Prefix) != nil {
		return result(request.Family, binding, "not_configured")
	}
	keys := []string{platformsettings.RevisionKey(binding.Location.Prefix)}
	for _, field := range fields {
		keys = append(keys, platformsettings.ConfigKey(binding.Location.Prefix, platformsettings.Tenant, field.DBKey()))
	}
	results, raws := readMany(ctx, request.Family, binding, keys)
	r := result(request.Family, binding, "ok")
	r.Type = "collection"
	r.Complete = true
	r.Limits = results[0].Limits
	r.ReadAt = results[0].ReadAt
	revision := results[0]
	if revision.Status == "ok" {
		revision.Value = string(raws[0])
	} else if revision.Status == "empty" {
		// The publishing protocol refuses an empty revision; it differs
		// from a revision key that has never existed.
		revision.Status = "invalid_document"
		revision.Complete = false
	}
	values := map[platformsettings.Field]Result{}
	for i, field := range fields {
		entry := results[i+1]
		if entry.Status == "ok" {
			var value any
			if json.Unmarshal(raws[i+1], &value) != nil || !validSetting(field, value) {
				entry.Status = "invalid_document"
				entry.Complete = false
			} else {
				entry.Value = value
			}
		}
		if !entry.Complete {
			r.Complete = false
		}
		values[field] = entry
	}
	if !revision.Complete {
		r.Complete = false
	}
	if !r.Complete {
		r.Status = "partial"
	}
	r.Value = map[string]any{"revision": revision, "fields": values, "tenant": platformsettings.Tenant, "publication_present": revision.Status == "ok", "applied_to_runtime": "not_proven_by_this_read"}
	return r
}

func validSetting(field platformsettings.Field, value any) bool {
	if field == platformsettings.FieldIsAccessBKData {
		_, ok := value.(bool)
		return ok
	}
	if value == nil {
		return true
	}
	items, ok := value.([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		if _, ok := item.(string); !ok {
			return false
		}
	}
	return true
}
