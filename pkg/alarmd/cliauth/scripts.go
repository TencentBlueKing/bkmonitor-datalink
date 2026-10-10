// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package cliauth

// Each script judges and writes deadlines against Redis TIME. The grant and
// session keys share the deployment hash tag, including on Redis Cluster.
const issueScript = `
redis.replicate_commands()
local clock = redis.call('TIME')
local now = tonumber(clock[1]) * 1000 + math.floor(tonumber(clock[2]) / 1000)
local record = cjson.decode(ARGV[1])
record.expires_at_ms = now + tonumber(ARGV[2])
-- The grant belongs to the environment's current revocation epoch: a
-- revocation before its exchange voids it too.
record.epoch = tonumber(redis.call('GET', KEYS[2]) or '0')
local encoded = cjson.encode(record)
if not redis.call('SET', KEYS[1], encoded, 'PX', ARGV[2], 'NX') then
  return {2}
end
return {1, encoded, 0}
`

const exchangeScript = `
redis.replicate_commands()
local clock = redis.call('TIME')
local now = tonumber(clock[1]) * 1000 + math.floor(tonumber(clock[2]) / 1000)
local raw = redis.call('GET', KEYS[1])
if not raw then return {0} end
local record = cjson.decode(raw)
if record.environment_id ~= ARGV[1] or record.scope ~= ARGV[3] or record.expires_at_ms <= now then
  return {0}
end
-- A grant made for a loopback login answers only the verifier's holder. A
-- wrong or missing verifier leaves the grant for its holder to exchange.
local epoch = tonumber(redis.call('GET', KEYS[5]) or '0')
if (tonumber(record.epoch) or 0) ~= epoch then return {0} end
local bound = 0
if record.code_challenge and record.code_challenge ~= '' then
  if record.code_challenge ~= ARGV[9] then return {0} end
  bound = 1
elseif ARGV[9] ~= '' then
  -- A verifier offered for a grant bound to none: the loopback login takes
  -- only a grant its own challenge was bound to. Left for its holder.
  return {3}
end
record.code_challenge = nil
record.session_id = ARGV[2]
record.expires_at_ms = now + tonumber(ARGV[4])
record.epoch = epoch
-- The pairing is made with the session when the bound allows it. A full
-- bound still exchanges: the session is returned and the pairing is named
-- as refused, so a device without renewal is a reading, not a failed login.
redis.call('ZREMRANGEBYSCORE', KEYS[4], '-inf', now - tonumber(ARGV[6]))
local paired = 0
if tonumber(redis.call('ZCARD', KEYS[4])) < tonumber(ARGV[7]) then
  record.pairing_id = ARGV[5]
  paired = 1
end
local encoded = cjson.encode(record)
-- A failed session creation must leave the grant available. Consumption only
-- follows a successful SET; both operations run in the same atomic script.
if not redis.call('SET', KEYS[2], encoded, 'PX', ARGV[4], 'NX') then
  return {2}
end
redis.call('DEL', KEYS[1])
if paired == 1 then
  local pairing = cjson.encode({pairing_id = ARGV[5], environment_id = ARGV[1], scope = ARGV[3], epoch = epoch,
    admin_binding = ARGV[8], created_at_ms = now, last_used_at_ms = now})
  redis.call('SET', KEYS[3], pairing, 'PX', ARGV[6])
  redis.call('ZADD', KEYS[4], now, ARGV[5])
end
return {1, encoded, 0, paired, bound}
`

const sessionScript = `
redis.replicate_commands()
local clock = redis.call('TIME')
local now = tonumber(clock[1]) * 1000 + math.floor(tonumber(clock[2]) / 1000)
local raw = redis.call('GET', KEYS[1])
if not raw then return {0} end
local record = cjson.decode(raw)
if record.environment_id ~= ARGV[1] or record.scope ~= ARGV[4] or record.expires_at_ms <= now then
  return {0}
end
-- A session from before the environment's last revocation is revoked with it.
if (tonumber(record.epoch) or 0) ~= tonumber(redis.call('GET', KEYS[2]) or '0') then return {0} end
if ARGV[2] ~= '' and record.session_id ~= ARGV[2] then return {0} end
local renewed = 0
if ARGV[3] == 'delete' then
  redis.call('DEL', KEYS[1])
elseif ARGV[3] == 'renew' and record.expires_at_ms - now <= tonumber(ARGV[6]) then
  record.expires_at_ms = now + tonumber(ARGV[5])
  raw = cjson.encode(record)
  redis.call('SET', KEYS[1], raw, 'PX', ARGV[5], 'XX')
  renewed = 1
end
return {1, raw, renewed}
`

// refreshScript spends one renewal credential for a new one and a new
// session. The old credential is gone whatever the outcome but a store
// fault: a credential of another environment, scope, revocation epoch or
// administrator key is deleted on sight, and one that was already spent is
// simply absent.
//
// Returns {0} absent or revoked, {4} the administrator key changed since the
// pairing, {2} a key collision, {6, sealed} spent within the grace with the
// sealed answer of that renewal, {1, session, 0, pairing_id} renewed.
const refreshScript = `
redis.replicate_commands()
local clock = redis.call('TIME')
local now = tonumber(clock[1]) * 1000 + math.floor(tonumber(clock[2]) / 1000)
local raw = redis.call('GET', KEYS[1])
if not raw then
  -- Spent moments ago: the answer to that renewal, sealed with the spent
  -- credential, is returned again so a reply lost on the way is not a lost
  -- pairing.
  local sealed = redis.call('GET', KEYS[6])
  if sealed then return {6, sealed} end
  return {0}
end
local pairing = cjson.decode(raw)
local epoch = tonumber(redis.call('GET', KEYS[5]) or '0')
if pairing.environment_id ~= ARGV[1] or pairing.scope ~= ARGV[2] or (tonumber(pairing.epoch) or 0) ~= epoch then
  redis.call('DEL', KEYS[1])
  redis.call('ZREM', KEYS[4], pairing.pairing_id or '')
  return {0}
end
if pairing.admin_binding ~= ARGV[3] then
  redis.call('DEL', KEYS[1])
  redis.call('ZREM', KEYS[4], pairing.pairing_id)
  return {4}
end
pairing.last_used_at_ms = now
local session = cjson.encode({session_id = ARGV[4], environment_id = ARGV[1], scope = ARGV[2], epoch = epoch,
  pairing_id = pairing.pairing_id, expires_at_ms = now + tonumber(ARGV[5])})
if redis.call('EXISTS', KEYS[2]) == 1 or redis.call('EXISTS', KEYS[3]) == 1 then return {2} end
redis.call('DEL', KEYS[1])
redis.call('SET', KEYS[6], ARGV[7], 'PX', ARGV[8])
redis.call('SET', KEYS[2], cjson.encode(pairing), 'PX', ARGV[6])
redis.call('SET', KEYS[3], session, 'PX', ARGV[5])
redis.call('ZADD', KEYS[4], now, pairing.pairing_id)
return {1, session, 0, pairing.pairing_id}
`

// upgradeScript pairs a live session that has no pairing: a session
// exchanged before this build, or one whose exchange found the bound full.
// Returns {0} session gone, {5} already paired, {3} bound full,
// {1, session, 0, pairing_id} paired.
const upgradeScript = `
redis.replicate_commands()
local clock = redis.call('TIME')
local now = tonumber(clock[1]) * 1000 + math.floor(tonumber(clock[2]) / 1000)
local raw = redis.call('GET', KEYS[1])
if not raw then return {0} end
local record = cjson.decode(raw)
local epoch = tonumber(redis.call('GET', KEYS[4]) or '0')
if record.environment_id ~= ARGV[1] or record.scope ~= ARGV[2] or record.expires_at_ms <= now or (tonumber(record.epoch) or 0) ~= epoch then
  return {0}
end
if record.pairing_id and record.pairing_id ~= '' then return {5} end
redis.call('ZREMRANGEBYSCORE', KEYS[3], '-inf', now - tonumber(ARGV[5]))
if tonumber(redis.call('ZCARD', KEYS[3])) >= tonumber(ARGV[6]) then return {3} end
if redis.call('EXISTS', KEYS[2]) == 1 then return {2} end
record.pairing_id = ARGV[3]
record.epoch = epoch
local encoded = cjson.encode(record)
redis.call('SET', KEYS[1], encoded, 'PX', record.expires_at_ms - now, 'XX')
redis.call('SET', KEYS[2], cjson.encode({pairing_id = ARGV[3], environment_id = ARGV[1], scope = ARGV[2], epoch = epoch,
  admin_binding = ARGV[4], created_at_ms = now, last_used_at_ms = now}), 'PX', ARGV[5])
redis.call('ZADD', KEYS[3], now, ARGV[3])
return {1, encoded, 0, ARGV[3]}
`

// forgetScript revokes one renewal credential, its holder's logout.
const forgetScript = `
local raw = redis.call('GET', KEYS[1])
if not raw then return {0} end
local pairing = cjson.decode(raw)
if pairing.environment_id ~= ARGV[1] then return {0} end
redis.call('DEL', KEYS[1])
redis.call('ZREM', KEYS[2], pairing.pairing_id)
return {1}
`

// revokeAllScript moves the environment to a new revocation epoch: every
// session and renewal credential of an earlier epoch fails its next check.
const revokeAllScript = `
local epoch = redis.call('INCR', KEYS[1])
local pairings = redis.call('ZCARD', KEYS[2])
redis.call('DEL', KEYS[2])
return {1, epoch, pairings}
`

// activeScript counts the pairings used within the idle lifetime.
const activeScript = `
redis.replicate_commands()
local clock = redis.call('TIME')
local now = tonumber(clock[1]) * 1000 + math.floor(tonumber(clock[2]) / 1000)
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now - tonumber(ARGV[1]))
return {1, redis.call('ZCARD', KEYS[1])}
`
