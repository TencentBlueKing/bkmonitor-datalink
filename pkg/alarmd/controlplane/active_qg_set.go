package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/go-redis/redis/v8"
)

const activeQueryGroupSetSchemaVersion = "alarmd-active-qg-set-v1"

type ActiveQueryGroupSetRef struct {
	SchemaVersion string `json:"schema_version"`
	Digest        string `json:"digest"`
	QGCount       uint64 `json:"qg_count"`
}

func (ref ActiveQueryGroupSetRef) validate() error {
	if ref.SchemaVersion != activeQueryGroupSetSchemaVersion || len(ref.Digest) != 64 {
		return errors.New("alarmd controlplane: invalid active Query Group set reference")
	}
	return nil
}

type activeQueryGroupSet struct {
	SchemaVersion string                         `json:"schema_version"`
	QueryGroups   []execution.QueryGroupIdentity `json:"query_groups"`
}

type ActiveQueryGroupSetConflictError struct{ Err error }

func (failure *ActiveQueryGroupSetConflictError) Error() string {
	return "alarmd controlplane: active Query Group set conflicts with its reference"
}

func (failure *ActiveQueryGroupSetConflictError) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.Err
}

func canonicalActiveQueryGroupSet(identities []execution.QueryGroupIdentity) (ActiveQueryGroupSetRef, []byte, error) {
	values := append([]execution.QueryGroupIdentity(nil), identities...)
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	for i, identity := range values {
		if identity == "" || (i > 0 && values[i-1] == identity) {
			return ActiveQueryGroupSetRef{}, nil, errors.New("alarmd controlplane: invalid active Query Group exact set")
		}
	}
	payload, err := json.Marshal(activeQueryGroupSet{SchemaVersion: activeQueryGroupSetSchemaVersion, QueryGroups: values})
	if err != nil {
		return ActiveQueryGroupSetRef{}, nil, err
	}
	digestBytes := sha256.Sum256(payload)
	ref := ActiveQueryGroupSetRef{SchemaVersion: activeQueryGroupSetSchemaVersion, Digest: hex.EncodeToString(digestBytes[:]), QGCount: uint64(len(values))}
	return ref, payload, nil
}

// persistAndVerifyActiveQGSet makes sure the set is stored under its digest
// and returns its reference. The key is named by the digest of its content,
// so a key of that name and length already there is the set: it is not sent
// again, which for a set that did not change is every publication (N15).
// The cutover scripts check the key and its length; they are no longer
// handed the set to compare, and the second return value - what they are
// handed - is the length.
func (repository *RedisCatalogRepository) persistAndVerifyActiveQGSet(ctx context.Context, identities []execution.QueryGroupIdentity) (ActiveQueryGroupSetRef, []byte, error) {
	encodeStarted := time.Now()
	ref, payload, err := canonicalActiveQueryGroupSet(identities)
	repository.observe(ctx, observability.Observation{Component: observability.ComponentControlPlane, Stage: observability.StageActiveQGSet,
		Result: observationResult(err), ActiveQGSet: &observability.ActiveQGSetFacts{Operation: "encode", Result: observationResultText(err), Duration: time.Since(encodeStarted)}})
	if err != nil {
		return ref, nil, err
	}
	redisStarted := time.Now()
	writeResult := "failure"
	defer func() {
		repository.observe(ctx, observability.Observation{Component: observability.ComponentControlPlane, Stage: observability.StageActiveQGSet,
			Result: observability.Result(writeResult), ActiveQGSet: &observability.ActiveQGSetFacts{Operation: "write", Result: writeResult, Duration: time.Since(redisStarted)}})
	}()
	key := repository.activeQGSetKey(ref.Digest)
	stored, err := repository.client.StrLen(ctx, key).Result()
	if err != nil {
		return ref, nil, activationDependencyIO(err)
	}
	if stored == 0 {
		created, err := repository.client.SetNX(ctx, key, payload, repository.ttl).Result()
		if err != nil {
			return ref, nil, activationDependencyIO(err)
		}
		if created {
			writeResult = "success"
			return ref, []byte(strconv.Itoa(len(payload))), nil
		}
		// Written by another process between the two reads: it is the same
		// digest, so it is the same set, if it is the same length.
		if stored, err = repository.client.StrLen(ctx, key).Result(); err != nil {
			return ref, nil, activationDependencyIO(err)
		}
	}
	if stored != int64(len(payload)) {
		return ref, nil, &ActiveQueryGroupSetConflictError{Err: errors.New("active Query Group set collision")}
	}
	writeResult = "success"
	return ref, []byte(strconv.Itoa(len(payload))), nil
}

// activeSetCache is the last set read, by digest: a set's content never
// changes under its digest, so a replica reads each set once.
type activeSetCache struct {
	mu     sync.Mutex
	digest string
	groups []execution.QueryGroupIdentity
}

func decodeActiveQGSet(payload []byte) (ActiveQueryGroupSetRef, []execution.QueryGroupIdentity, error) {
	var set activeQueryGroupSet
	if err := json.Unmarshal(payload, &set); err != nil {
		return ActiveQueryGroupSetRef{}, nil, err
	}
	ref, canonical, err := canonicalActiveQueryGroupSet(set.QueryGroups)
	if err != nil || string(canonical) != string(payload) {
		return ActiveQueryGroupSetRef{}, nil, errors.New("alarmd controlplane: corrupt active Query Group set")
	}
	return ref, set.QueryGroups, nil
}

func (repository *RedisCatalogRepository) LoadActiveQueryGroupSet(ctx context.Context, ref ActiveQueryGroupSetRef) ([]execution.QueryGroupIdentity, error) {
	started := time.Now()
	result := "failure"
	defer func() {
		repository.observe(ctx, observability.Observation{Component: observability.ComponentControlPlane, Stage: observability.StageActiveQGSet,
			Result: observability.Result(result), ActiveQGSet: &observability.ActiveQGSetFacts{Operation: "read", Result: result, Duration: time.Since(started)}})
	}()
	if err := ref.validate(); err != nil {
		return nil, &ActiveQueryGroupSetConflictError{Err: err}
	}
	// Asked of the store every time, cached or not: a set that is gone must
	// read as gone - the round that finds it missing is what restores it -
	// and its length costs a few bytes where the set costs all of them.
	stored, err := repository.client.StrLen(ctx, repository.activeQGSetKey(ref.Digest)).Result()
	if err != nil {
		return nil, activationDependencyIO(err)
	}
	cache := &repository.activeSets
	if stored == 0 {
		cache.mu.Lock()
		cache.digest, cache.groups = "", nil
		cache.mu.Unlock()
		return nil, ErrSnapshotUnavailable
	}
	cache.mu.Lock()
	if cache.digest == ref.Digest {
		groups := slices.Clone(cache.groups)
		cache.mu.Unlock()
		result = "success"
		return groups, nil
	}
	cache.mu.Unlock()
	payload, err := repository.client.Get(ctx, repository.activeQGSetKey(ref.Digest)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrSnapshotUnavailable
	}
	if err != nil {
		return nil, activationDependencyIO(err)
	}
	actual, groups, err := decodeActiveQGSet(payload)
	if err != nil || actual != ref {
		return nil, &ActiveQueryGroupSetConflictError{Err: errors.New("corrupt active Query Group set")}
	}
	cache.mu.Lock()
	cache.digest, cache.groups = ref.Digest, slices.Clone(groups)
	cache.mu.Unlock()
	result = "success"
	return groups, nil
}

func observationResult(err error) observability.Result {
	if err == nil {
		return observability.ResultSuccess
	}
	return observability.ResultFailed
}

func observationResultText(err error) string {
	if err == nil {
		return "success"
	}
	return "failure"
}
