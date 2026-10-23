// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.
package node

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/go-redis/redis/v8"
)

// No MULTI/EXEC: one Lua reply cannot desynchronize the shared Redis pool.
var createScript = redis.NewScript(`
if redis.call('EXISTS',KEYS[1]) == 1 then return 0 end
redis.call('HSET',KEYS[1],'record',ARGV[1],'digest',ARGV[2],'cancel','0')
redis.call('EXPIRE',KEYS[1],172800)
redis.call('ZADD',KEYS[2],ARGV[3],ARGV[4])
return 1`)
var replaceScript = redis.NewScript(`
if redis.call('HGET',KEYS[1],'record') ~= ARGV[1] then return 0 end
redis.call('HSET',KEYS[1],'record',ARGV[2]); return 1`)
var cancelScript = redis.NewScript(`
if redis.call('EXISTS',KEYS[1]) == 0 then return 0 end
redis.call('HSET',KEYS[1],'cancel','1'); return 1`)
var dispatchScript = redis.NewScript(`
if redis.call('HGET',KEYS[1],'record') ~= ARGV[1] then return 0 end
if redis.call('HGET',KEYS[1],'cancel') == '1' then return 0 end
redis.call('HSET',KEYS[1],'record',ARGV[2]); return 1`)

func (p *Provider) key(id string) string { return p.options.KeyPrefix + "node:" + id }
func (p *Provider) index() string        { return p.options.KeyPrefix + "node-active" }
func (p *Provider) read(ctx context.Context, id string) (Record, string, bool, error) {
	values, err := p.options.Redis.HGetAll(ctx, p.key(id)).Result()
	if err != nil {
		return Record{}, "", false, err
	}
	if len(values) == 0 {
		return Record{}, "", false, fail("record_expired", "execution record is absent or expired; do not resubmit with a new ID")
	}
	var r Record
	if err = json.Unmarshal([]byte(values["record"]), &r); err != nil {
		return r, "", false, err
	}
	return r, values["digest"], values["cancel"] == "1", nil
}
func (p *Provider) replace(ctx context.Context, before, after Record, dispatch bool) error {
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	script := replaceScript
	if dispatch {
		script = dispatchScript
	}
	n, err := script.Run(ctx, p.options.Redis, []string{p.key(before.ExecutionID)}, string(a), string(b)).Int()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("execution changed or cancellation requested")
	}
	return nil
}
