// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package ownership

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// MaxControlHashWrite bounds the fields one FencedHashUpdate sets and
// deletes together, so one script's arguments stay a bounded size. A caller
// with more writes them in several calls, each fenced.
const MaxControlHashWrite = 1000

// FencedHashRequest is one fenced write of a control hash: the fields to set
// and the fields to delete, under the lease the fence names. Like
// FencedCASRequest it names no instant: whether the lease is live is decided
// on Redis's clock inside the script.
type FencedHashRequest struct {
	Fence     execution.OwnerFence
	Namespace string
	Set       map[string][]byte
	Delete    []string
	// TTL renews the whole hash when positive.
	TTL time.Duration
}

// FencedHashUpdate applies the request's deletes and sets to the control
// hash of the fence's Query Group in one script, only while the fence still
// holds its lease: a replica that lost the lease - a Control Leader whose
// term another replica now holds - writes nothing and gets ErrStaleFence.
func (store *RedisStore) FencedHashUpdate(ctx context.Context, request FencedHashRequest) error {
	if store == nil || store.client == nil {
		return errors.New("alarmd ownership: initialized store is required")
	}
	if err := validateFence(request.Fence); err != nil {
		return err
	}
	if request.Namespace == "" || strings.ContainsAny(request.Namespace, "{} \t\r\n") || request.TTL < 0 ||
		len(request.Set)+len(request.Delete) > MaxControlHashWrite {
		return errors.New("alarmd ownership: invalid fenced hash request")
	}
	args := []interface{}{boolText(request.Fence.QueryGroup != ControlLeaderIdentity), request.Fence.OwnerID,
		request.Fence.OwnerEpoch, request.Fence.LeaseToken, request.TTL.Milliseconds(), len(request.Delete)}
	for _, field := range request.Delete {
		args = append(args, field)
	}
	for field, value := range request.Set {
		args = append(args, field, value)
	}
	result, err := fencedHashScript.Run(ctx, store.client, []string{
		store.assignmentKey(request.Fence.QueryGroup), store.ownershipKey(request.Fence.QueryGroup),
		store.controlKey(request.Fence.QueryGroup, request.Namespace),
	}, args...).Text()
	if err != nil {
		return err
	}
	switch result {
	case "APPLIED":
		return nil
	case "STALE_OWNER":
		return ErrStaleFence
	default:
		return errors.New("alarmd ownership: invalid fenced hash response")
	}
}

// ReadControlHash reads a control hash of the Query Group whole. No key is
// an empty hash.
func (store *RedisStore) ReadControlHash(ctx context.Context, queryGroup execution.QueryGroupIdentity, namespace string) (map[string]string, error) {
	if store == nil || store.client == nil || queryGroup == "" || namespace == "" || strings.ContainsAny(namespace, "{} \t\r\n") {
		return nil, errors.New("alarmd ownership: invalid control read")
	}
	fields, err := store.client.HGetAll(ctx, store.controlKey(queryGroup, namespace)).Result()
	if errors.Is(err, redis.Nil) {
		return map[string]string{}, nil
	}
	return fields, err
}

var fencedHashScript = redis.NewScript(FenceLua + `
local refusal = fence_refusal(KEYS[1], KEYS[2], ARGV[1], ARGV[2], ARGV[3], ARGV[4], '', redis_now_ms())
if refusal then return 'STALE_OWNER' end
local deletes = tonumber(ARGV[6])
local at = 7
for i = 1, deletes do
  redis.call('HDEL', KEYS[3], ARGV[at])
  at = at + 1
end
while at < #ARGV do
  redis.call('HSET', KEYS[3], ARGV[at], ARGV[at + 1])
  at = at + 2
end
local ttl_ms = tonumber(ARGV[5])
if ttl_ms > 0 and redis.call('EXISTS', KEYS[3]) == 1 then redis.call('PEXPIRE', KEYS[3], ttl_ms) end
return 'APPLIED'
`)
