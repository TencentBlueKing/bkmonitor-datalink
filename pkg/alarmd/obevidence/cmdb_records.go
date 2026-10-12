// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package obevidence

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/cmdbcache"
)

// The platform's CMDB records, one at a time: a host by its id or by its
// "ip|cloud" field, a service instance by its id. The index loads the two
// hashes whole and takes every record it cannot decode as absent, counting
// it and naming the first by its field (the cmdb_cache writer evidence); a
// record read here says whether the cache has the host at all, what the
// writer wrote, and whether alarmd can read it.
const (
	FamilyCMDBHost            = "cmdb_host"
	FamilyCMDBServiceInstance = "cmdb_service_instance"
)

// CMDBRecord is one record as read and as alarmd decodes it. Record is the
// payload as written - JSON when it parses, its text otherwise - and Decoded
// what a load would hold for it, or DecodeError the reason a load skips it.
type CMDBRecord struct {
	Field       string `json:"field"`
	Record      any    `json:"record"`
	Decoded     any    `json:"decoded,omitempty"`
	DecodeError string `json:"decode_error,omitempty"`
}

// cmdbHostField reports whether a host is named the way the writer keys the
// host hash: its id, or "ip|cloud" with a parseable address and a cloud id.
func cmdbHostField(field string) bool {
	if positiveID(field) {
		return true
	}
	address, cloud, found := strings.Cut(field, "|")
	return found && net.ParseIP(address) != nil && (cloud == "0" || positiveID(cloud))
}

// cmdbRecord answers the two CMDB families.
func (service *Service) cmdbRecord(ctx context.Context, request StoreRequest) Result {
	binding := service.options.CMDBCache
	host := request.Family == FamilyCMDBHost
	field := request.ServiceInstance
	if host {
		field = request.Host
	}
	valid := host && cmdbHostField(request.Host) && request.ServiceInstance == "" ||
		!host && positiveID(request.ServiceInstance) && request.Host == ""
	if !valid || request.StrategyID != "" || request.GroupID != "" || request.QueryGroup != "" || len(request.Fields) > 0 ||
		request.ObjectDigest != "" || request.Tenant != "" || request.Business != "" {
		return invalid(request.Family, binding)
	}
	if binding.Client == nil {
		return result(request.Family, binding, "not_configured")
	}
	reader, err := cmdbcache.NewReader(binding.Client, binding.Location.Prefix)
	if err != nil {
		return result(request.Family, binding, "not_configured")
	}
	key := reader.ServiceInstanceCacheKey()
	if host {
		key = reader.HostCacheKey()
	}
	r, raw := readHashField(ctx, request.Family, binding, key, field)
	if r.Status != "ok" {
		return r
	}
	record := CMDBRecord{Field: field, Record: string(raw)}
	if json.Valid(raw) {
		record.Record = json.RawMessage(raw)
	}
	if host {
		facts, err := cmdbcache.DecodeHostRecord(string(raw))
		if err != nil {
			record.DecodeError = err.Error()
		} else {
			record.Decoded = map[string]any{"host_id": facts.HostID, "ip": facts.IP, "cloud_id": facts.CloudID,
				"business_id": facts.BusinessID, "topo_nodes": facts.TopoNodes}
		}
	} else {
		facts, _, err := cmdbcache.DecodeServiceInstanceRecord(field, string(raw))
		if err != nil {
			record.DecodeError = err.Error()
		} else {
			record.Decoded = map[string]any{"id": facts.ID, "host_id": facts.HostID, "ip": facts.IP,
				"cloud_id": facts.CloudID, "topo_nodes": facts.TopoNodes}
		}
	}
	r.Value = record
	return r
}

// readHashField is one bounded read of one field of a hash. The key's TYPE
// and PTTL, whether the field exists and its length come in one
// transaction; the value follows only when it fits the document limit,
// since a hash field has no range read and its size has to be asked first.
// A key that does not exist and a field the hash does not hold are both
// missing, told apart by the reason: the first is the whole cache gone, the
// second one record the writer did not write.
func readHashField(ctx context.Context, source string, binding RedisBinding, key, field string) (Result, []byte) {
	r := result(source, binding, "dependency_unavailable")
	r.Location.Key = key
	if binding.Client == nil {
		r.Status = "not_configured"
		return r, nil
	}
	ctx, cancel := context.WithTimeout(ctx, ReadTimeout)
	defer cancel()
	r.Limits.DocumentReadLimitBytes = MaxDocumentBytes
	r.Limits.Commands = 6 // MULTI, TYPE, PTTL, HEXISTS, HSTRLEN, EXEC
	var kind *redis.StatusCmd
	var ttl *redis.DurationCmd
	var exists *redis.BoolCmd
	var length *redis.Cmd
	_, txErr := binding.Client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		kind = pipe.Type(ctx, key)
		ttl = pipe.PTTL(ctx, key)
		exists = pipe.HExists(ctx, key, field)
		// go-redis v8 has no helper for HSTRLEN (Redis 3.2 and later).
		length = pipe.Do(ctx, "HSTRLEN", key, field)
		return nil
	})
	at := time.Now().UTC()
	r.ReadAt = &at
	if kind == nil || kind.Err() != nil || ttl.Err() != nil {
		r.Reason, r.ReasonText = failureReason(txErr, kind, ttl)
		binding.failed(r.Reason)
		return r, nil
	}
	r.Type = kind.Val()
	ttlMS := ttl.Val().Milliseconds()
	if ttl.Val() < 0 {
		ttlMS = int64(ttl.Val())
	}
	r.TTLMS = &ttlMS
	switch r.Type {
	case "none":
		r.Status, r.Reason, r.Complete = "missing", "key_absent", true
		return r, nil
	case "hash":
	default:
		r.Status = "wrong_type"
		return r, nil
	}
	if exists.Err() != nil || length.Err() != nil {
		r.Reason, r.ReasonText = failureReason(txErr, exists, length)
		binding.failed(r.Reason)
		return r, nil
	}
	size, sizeErr := length.Int64()
	if sizeErr != nil {
		r.Reason, r.ReasonText = failureReason(nil, redis.NewStringResult("", sizeErr))
		binding.failed(r.Reason)
		return r, nil
	}
	if !exists.Val() {
		r.Status, r.Reason, r.Complete = "missing", "field_absent", true
		return r, nil
	}
	if size == 0 {
		r.Status, r.Complete = "empty", true
		return r, nil
	}
	if size > int64(MaxDocumentBytes) {
		r.Status, r.Reason = "budget_exceeded", "document_bytes"
		return r, nil
	}
	r.Limits.Commands++
	value, err := binding.Client.HGet(ctx, key, field).Result()
	switch {
	case err == redis.Nil:
		// Gone between the two reads: as missing as it would have been.
		r.Status, r.Reason, r.Complete = "missing", "field_absent", true
		return r, nil
	case err != nil:
		r.Reason, r.ReasonText = failureReason(nil, redis.NewStringResult("", err))
		binding.failed(r.Reason)
		return r, nil
	}
	r.Limits.Bytes = len(value)
	// Grown past the limit between the length and the read: refused as it
	// would have been, rather than handed on whole.
	if len(value) > MaxDocumentBytes {
		r.Status, r.Reason = "budget_exceeded", "document_bytes"
		return r, nil
	}
	r.Status, r.Complete = "ok", true
	return r, []byte(value)
}
