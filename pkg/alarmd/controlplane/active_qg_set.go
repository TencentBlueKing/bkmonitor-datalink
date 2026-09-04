package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
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
	created, err := repository.client.SetNX(ctx, key, payload, repository.ttl).Result()
	if err != nil {
		return ref, nil, activationDependencyIO(err)
	}
	stored, err := repository.client.Get(ctx, key).Bytes()
	if err != nil {
		return ref, nil, activationDependencyIO(err)
	}
	if !created && string(stored) != string(payload) {
		return ref, nil, &ActiveQueryGroupSetConflictError{Err: errors.New("active Query Group set collision")}
	}
	loadedRef, _, err := decodeActiveQGSet(stored)
	if err != nil || loadedRef != ref {
		return ref, nil, &ActiveQueryGroupSetConflictError{Err: errors.New("active Query Group set verification failed")}
	}
	writeResult = "success"
	return ref, payload, nil
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
