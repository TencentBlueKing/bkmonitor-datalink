package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
)

var ErrLegacySourceIncomplete = errors.New("alarmd controlplane: legacy Redis source incomplete")

// ErrActiveStrategyIDInvalid marks an active set refused because one element
// is not a canonical positive integer. It wraps ErrLegacySourceIncomplete,
// which is what every caller already checks; the extra identity is for the
// reader who has to find out which element, and the error text names it.
var ErrActiveStrategyIDInvalid = errors.New("alarmd controlplane: active strategy identity is not a canonical positive integer")

// invalidActiveStrategyIDText bounds how much of a refused element the error
// repeats. The element comes from the store and could be anything; the
// reader needs enough to find it, not all of it.
const invalidActiveStrategyIDText = 64

type legacyRedisCommands interface {
	Get(context.Context, string) *redis.StringCmd
	MGet(context.Context, ...string) *redis.SliceCmd
}

// LegacyRedisStrategySource adapts only the Python StrategyCacheManager String
// contract: <prefix>.strategy_ids, <prefix>.strategy_<id> and, as its change
// signal, <prefix>.last_updated. Legacy DTOs do not escape this adapter.
type LegacyRedisStrategySource struct {
	client          legacyRedisCommands
	strategyIDsKey  string
	strategyKeyStem string
	lastUpdatedKey  string
}

func NewLegacyRedisStrategySource(client redis.Cmdable, cachePrefix string) (*LegacyRedisStrategySource, error) {
	if client == nil || cachePrefix == "" || strings.ContainsAny(cachePrefix, "{} \t\r\n") {
		return nil, errors.New("alarmd controlplane: invalid legacy Redis strategy source")
	}
	return &LegacyRedisStrategySource{
		client: client, strategyIDsKey: cachePrefix + ".strategy_ids",
		strategyKeyStem: cachePrefix + ".strategy_",
		lastUpdatedKey:  cachePrefix + ".last_updated",
	}, nil
}

func (source *LegacyRedisStrategySource) ActiveStrategyIDs(ctx context.Context) ([]string, error) {
	if source == nil || source.client == nil {
		return nil, errors.New("alarmd controlplane: legacy Redis strategy source is required")
	}
	payload, err := source.client.Get(ctx, source.strategyIDsKey).Bytes()
	if errors.Is(err, redis.Nil) || (err == nil && len(payload) == 0) {
		return nil, ErrLegacySourceIncomplete
	}
	if err != nil {
		return nil, fmt.Errorf("alarmd controlplane: read legacy strategy active set: %w", err)
	}
	var rawIDs []json.RawMessage
	if err := json.Unmarshal(payload, &rawIDs); err != nil {
		return nil, fmt.Errorf("%w: decode active strategy set: %v", ErrLegacySourceIncomplete, err)
	}
	ids := make([]string, 0, len(rawIDs))
	for _, rawID := range rawIDs {
		text := string(rawID)
		id, err := strconv.ParseUint(text, 10, 64)
		if err != nil || id == 0 || strconv.FormatUint(id, 10) != text {
			if len(text) > invalidActiveStrategyIDText {
				text = text[:invalidActiveStrategyIDText] + "..."
			}
			return nil, fmt.Errorf("%w: %w: element %d of %d is %q",
				ErrLegacySourceIncomplete, ErrActiveStrategyIDInvalid, len(ids), len(rawIDs), text)
		}
		ids = append(ids, strconv.FormatUint(id, 10))
	}
	return ids, nil
}

func (source *LegacyRedisStrategySource) Strategies(ctx context.Context, ids []string) ([]SourceStrategy, error) {
	if source == nil || source.client == nil {
		return nil, errors.New("alarmd controlplane: legacy Redis strategy source is required")
	}
	if len(ids) == 0 {
		return []SourceStrategy{}, nil
	}
	keys := make([]string, len(ids))
	for index, id := range ids {
		parsed, err := strconv.ParseUint(id, 10, 64)
		if err != nil || parsed == 0 || strconv.FormatUint(parsed, 10) != id {
			return nil, ErrObservationUnstable
		}
		keys[index] = source.strategyKeyStem + id
	}
	values, err := source.client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("alarmd controlplane: read legacy strategy objects: %w", err)
	}
	if len(values) != len(ids) {
		return nil, ErrObservationUnstable
	}
	strategies := make([]SourceStrategy, 0, len(ids))
	for index, value := range values {
		payload, ok := legacyRedisBytes(value)
		if !ok || len(payload) == 0 {
			strategies = append(strategies, SourceStrategy{SourceID: ids[index], SourceDisposition: &ObjectDisposition{
				SourceID: ids[index], Scope: "STRATEGY", Disposition: DispositionSourceIncomplete,
				Reason: "SOURCE_OBJECT_INCOMPLETE",
			}})
			continue
		}
		strategy := SourceStrategy{SourceID: ids[index], Document: append(json.RawMessage(nil), payload...)}
		var identityDTO struct {
			ID         int64           `json:"id"`
			BusinessID int64           `json:"bk_biz_id"`
			TenantID   json.RawMessage `json:"bk_tenant_id"`
			SpaceUID   json.RawMessage `json:"space_uid"`
		}
		if err := json.Unmarshal(payload, &identityDTO); err != nil {
			strategy.SourceDisposition = &ObjectDisposition{
				SourceID: ids[index], Scope: "STRATEGY", Disposition: DispositionConfigRejected,
				Reason: "STRATEGY_DOCUMENT_INVALID",
			}
			strategies = append(strategies, strategy)
			continue
		}
		if identityDTO.ID <= 0 || strconv.FormatInt(identityDTO.ID, 10) != ids[index] {
			strategy.SourceDisposition = &ObjectDisposition{
				SourceID: ids[index], Scope: "STRATEGY", Disposition: DispositionConfigRejected,
				Reason: "STRATEGY_IDENTITY_INVALID",
			}
			strategies = append(strategies, strategy)
			continue
		}
		if identityDTO.BusinessID == 0 {
			strategy.SourceDisposition = &ObjectDisposition{
				SourceID: ids[index], Scope: "STRATEGY", Disposition: DispositionConfigRejected,
				Reason: "STRATEGY_BUSINESS_IDENTITY_INVALID",
			}
			strategies = append(strategies, strategy)
			continue
		}
		tenantID, tenantOK := decodeRequiredIdentityString(identityDTO.TenantID)
		spaceUID, spaceOK := decodeRequiredIdentityString(identityDTO.SpaceUID)
		if !tenantOK || !spaceOK {
			strategy.SourceDisposition = &ObjectDisposition{
				SourceID: ids[index], Scope: "STRATEGY", Disposition: DispositionSourceIncomplete,
				Reason: "SOURCE_IDENTITY_UNAVAILABLE",
			}
			strategies = append(strategies, strategy)
			continue
		}
		strategy.Identity = SourceIdentity{TenantID: tenantID, BusinessID: strconv.FormatInt(identityDTO.BusinessID, 10), SpaceScope: spaceUID}
		strategies = append(strategies, strategy)
	}
	return strategies, nil
}

// ChangeSignal reads <prefix>.last_updated. The cache manager's incremental
// refresh writes it, as the integer second the run started, after it has
// written every strategy document of a run that found changes, and returns
// before touching it when a run finds none; its full refresh never writes it.
// So an unchanged value means no strategy was saved since the previous read,
// and says nothing about content the manager derives from other tables and
// rewrites in place. A marker that is absent or cannot be read is reported as
// absent: the round then reads everything, as it did before the marker was
// consulted.
func (source *LegacyRedisStrategySource) ChangeSignal(ctx context.Context) (SourceChangeSignal, error) {
	if source == nil || source.client == nil {
		return SourceChangeSignal{}, errors.New("alarmd controlplane: legacy Redis strategy source is required")
	}
	payload, err := source.client.Get(ctx, source.lastUpdatedKey).Result()
	if errors.Is(err, redis.Nil) {
		return SourceChangeSignal{}, nil
	}
	if err != nil {
		return SourceChangeSignal{}, fmt.Errorf("alarmd controlplane: read legacy strategy change signal: %w", err)
	}
	seconds, parseErr := strconv.ParseInt(strings.TrimSpace(payload), 10, 64)
	if parseErr != nil || seconds <= 0 {
		return SourceChangeSignal{}, nil
	}
	return SourceChangeSignal{Present: true, Value: payload, WrittenAt: time.Unix(seconds, 0)}, nil
}

func decodeRequiredIdentityString(payload json.RawMessage) (string, bool) {
	if len(payload) == 0 {
		return "", false
	}
	var value string
	if err := json.Unmarshal(payload, &value); err != nil || value == "" || strings.TrimSpace(value) != value {
		return "", false
	}
	return value, true
}

func legacyRedisBytes(value interface{}) ([]byte, bool) {
	switch typed := value.(type) {
	case string:
		return []byte(typed), true
	case []byte:
		return append([]byte(nil), typed...), true
	default:
		return nil, false
	}
}
