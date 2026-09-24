package controlplane

import (
	"encoding/json"
	"errors"
	"strconv"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// ObservationDocumentKey exposes the production key builder, not Redis syntax.
func (source *LegacyRedisStrategySource) ObservationDocumentKey(id string) (string, error) {
	parsed, err := strconv.ParseUint(id, 10, 64)
	if source == nil || err != nil || parsed == 0 || strconv.FormatUint(parsed, 10) != id {
		return "", ErrObservationUnstable
	}
	return source.strategyKeyStem + id, nil
}

// ObservationQueryGroupKey names one immutable object for a bounded reader.
func (repository *RedisCatalogRepository) ObservationQueryGroupKey(digest execution.ObjectDigest) (string, error) {
	if repository == nil || digest == "" {
		return "", errors.New("catalog object digest required")
	}
	return repository.queryGroupObjectKey(digest), nil
}

// DecodeObservedQueryGroup uses the same canonical digest and contract checks as
// the production object reader. The caller must bound bytes before calling it.
func DecodeObservedQueryGroup(payload []byte, digest execution.ObjectDigest) (QueryGroupObject, error) {
	name, err := queryGroupObjectDomain(payload)
	if err != nil {
		return QueryGroupObject{}, ErrCatalogObjectCorrupt
	}
	hashed, err := contract.DeriveCanonicalDigestV2OverCanonical(name, payload)
	if err != nil || hashed != string(digest) {
		return QueryGroupObject{}, ErrCatalogObjectCorrupt
	}
	var object QueryGroupObject
	if err := json.Unmarshal(payload, &object); err != nil || !knownQueryGroupObjectVersion(object.ContractVersion) || object.Identity == "" {
		return QueryGroupObject{}, ErrCatalogObjectCorrupt
	}
	return object, nil
}
