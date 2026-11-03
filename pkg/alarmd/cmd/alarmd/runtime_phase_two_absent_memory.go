package main

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/absentalerts"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// absentMemoryNamespace is the Control Leader's control hash the absent
// close keeps its candidates in: one field per candidate, the strategy id
// and its tenant, and what absentalerts.Absence holds except TermReads.
const absentMemoryNamespace = "absent_close_memory"

// absentMemoryTTL renews the hash on every save. A leader saves every round
// (absentCloseInterval), so only a deployment with no leader for a day
// loses it, and then the candidates start over.
const absentMemoryTTL = 24 * time.Hour

// absentMemory keeps the absent close's candidates across leader terms: a
// release restarts the leader, and without it every candidate's reads start
// over (about 37 minutes from disable to close). Remember
// writes only under this term's Control Leader lease.
type absentMemory interface {
	Recall(context.Context) (map[absentalerts.Key]absentalerts.Absence, int, error)
	Remember(ctx context.Context, set map[absentalerts.Key]absentalerts.Absence, del []absentalerts.Key) error
}

type controlHashStore interface {
	FencedHashUpdate(context.Context, ownership.FencedHashRequest) error
	ReadControlHash(context.Context, execution.QueryGroupIdentity, string) (map[string]string, error)
}

type controlFenceSource interface {
	ControlFence() (execution.OwnerFence, bool)
}

// errNoControlFence is a save without the lease in hand: this replica is
// not the leader any more, as a refused fence says.
var errNoControlFence = errors.New("alarmd absent close: no Control Leader lease in hand")

type redisAbsentMemory struct {
	store controlHashStore
	fence controlFenceSource
}

type storedAbsence struct {
	Since       int64  `json:"since"`
	Observation string `json:"observation"`
	Reads       int    `json:"reads"`
	Confirmed   bool   `json:"confirmed,omitempty"`
	MarkTenant  string `json:"mark_tenant,omitempty"`
	MarkState   string `json:"mark_state,omitempty"`
	MarkRev     int64  `json:"mark_revision,omitempty"`
	MarkReads   int    `json:"mark_reads,omitempty"`
}

// absentMemoryField is a candidate's field: the strategy id, which is a
// canonical integer, then the tenant.
func absentMemoryField(key absentalerts.Key) string { return key.StrategyID + "@" + key.TenantID }

func absentMemoryKey(field string) (absentalerts.Key, bool) {
	id, tenant, ok := strings.Cut(field, "@")
	if !ok || id == "" {
		return absentalerts.Key{}, false
	}
	return absentalerts.Key{TenantID: tenant, StrategyID: id}, true
}

// Recall reads every kept candidate, and how many fields did not decode
// (skipped: such a candidate starts over).
func (memory *redisAbsentMemory) Recall(ctx context.Context) (map[absentalerts.Key]absentalerts.Absence, int, error) {
	fields, err := memory.store.ReadControlHash(ctx, ownership.ControlLeaderIdentity, absentMemoryNamespace)
	if err != nil {
		return nil, 0, err
	}
	absences := make(map[absentalerts.Key]absentalerts.Absence, len(fields))
	invalid := 0
	for field, value := range fields {
		key, ok := absentMemoryKey(field)
		var stored storedAbsence
		if !ok || json.Unmarshal([]byte(value), &stored) != nil || stored.Since <= 0 || stored.Reads <= 0 {
			invalid++
			continue
		}
		absence := absentalerts.Absence{Since: time.Unix(stored.Since, 0).UTC(), Observation: stored.Observation,
			Reads: stored.Reads, Confirmed: stored.Confirmed, MarkReads: stored.MarkReads}
		if stored.MarkState != "" {
			absence.Mark = absentalerts.Mark{TenantID: stored.MarkTenant, State: stored.MarkState, Revision: stored.MarkRev}
		}
		absences[key] = absence
	}
	return absences, invalid, nil
}

// Remember writes set and removes del, in bounded fenced writes. A refused fence
// is ownership.ErrStaleFence, and so is having no lease in hand; nothing
// after it is written.
func (memory *redisAbsentMemory) Remember(ctx context.Context, set map[absentalerts.Key]absentalerts.Absence, del []absentalerts.Key) error {
	fence, ok := memory.fence.ControlFence()
	if !ok {
		return errors.Join(ownership.ErrStaleFence, errNoControlFence)
	}
	keys := make([]absentalerts.Key, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return absentalerts.LessKey(keys[i], keys[j]) })
	request := ownership.FencedHashRequest{Fence: fence, Namespace: absentMemoryNamespace, Set: map[string][]byte{}, TTL: absentMemoryTTL}
	flush := func() error {
		if len(request.Set)+len(request.Delete) == 0 {
			return nil
		}
		err := memory.store.FencedHashUpdate(ctx, request)
		request.Set, request.Delete = map[string][]byte{}, nil
		return err
	}
	for _, key := range del {
		request.Delete = append(request.Delete, absentMemoryField(key))
		if len(request.Delete) >= ownership.MaxControlHashWrite {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	for _, key := range keys {
		absence := set[key]
		encoded, err := json.Marshal(storedAbsence{Since: absence.Since.Unix(), Observation: absence.Observation, Reads: absence.Reads,
			Confirmed: absence.Confirmed, MarkTenant: absence.Mark.TenantID, MarkState: absence.Mark.State, MarkRev: absence.Mark.Revision,
			MarkReads: absence.MarkReads})
		if err != nil {
			return err
		}
		request.Set[absentMemoryField(key)] = encoded
		if len(request.Set)+len(request.Delete) >= ownership.MaxControlHashWrite {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	return flush()
}
