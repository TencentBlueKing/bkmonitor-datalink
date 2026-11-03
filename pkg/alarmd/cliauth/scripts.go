// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package cliauth

// Each script judges and writes deadlines against Redis TIME. The grant and
// session keys share the deployment hash tag, including on Redis Cluster.

// revokedLua is whether a credential issued at issued to operator was
// revoked with them: the roster dropped the operator at or after it. A
// credential without an operator -- the authorization page's, or one from
// before 0.4 -- is never revoked this way.
const revokedLua = `
local function revoked(key, operator, issued)
  if not operator or operator == '' then return false end
  local at = redis.call('ZSCORE', key, operator)
  return at ~= false and at ~= nil and (tonumber(issued) or 0) <= tonumber(at)
end
`

// expirePairingsLua drops the pairings unused for the idle lifetime from the
// bound's set and from the operators' index together, so the index never
// names a pairing the bound no longer counts.
const expirePairingsLua = `
local function expirePairings(pairings, named, now, idle)
  local expired = redis.call('ZRANGEBYSCORE', pairings, '-inf', now - idle)
  for i = 1, #expired, 500 do
    local last = math.min(i + 499, #expired)
    redis.call('ZREM', pairings, unpack(expired, i, last))
    redis.call('HDEL', named, unpack(expired, i, last))
  end
end
`

const clockLua = `
redis.replicate_commands()
local clock = redis.call('TIME')
local now = tonumber(clock[1]) * 1000 + math.floor(tonumber(clock[2]) / 1000)
`

// issueScript stores a grant. One issued to an operator needs the roster to
// have arrived and to name them: {7} no roster yet, {8} not on it.
const issueScript = clockLua + `
local record = cjson.decode(ARGV[1])
record.expires_at_ms = now + tonumber(ARGV[2])
record.issued_at_ms = now
if record.operator and record.operator ~= '' then
  if redis.call('EXISTS', KEYS[3]) == 0 then return {7} end
  if redis.call('SISMEMBER', KEYS[4], record.operator) == 0 then return {8} end
end
-- The grant belongs to the environment's current revocation epoch: a
-- revocation before its exchange voids it too.
record.epoch = tonumber(redis.call('GET', KEYS[2]) or '0')
local encoded = cjson.encode(record)
if not redis.call('SET', KEYS[1], encoded, 'PX', ARGV[2], 'NX') then
  return {2}
end
return {1, encoded, 0}
`

const exchangeScript = clockLua + revokedLua + expirePairingsLua + `
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
-- An operator dropped from the roster after the grant was issued.
if revoked(KEYS[6], record.operator, record.issued_at_ms) then return {0} end
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
record.issued_at_ms = now
record.epoch = epoch
-- The pairing is made with the session when the bound allows it. A full
-- bound still exchanges: the session is returned and the pairing is named
-- as refused, so a device without renewal is a reading, not a failed login.
expirePairings(KEYS[4], KEYS[7], now, tonumber(ARGV[6]))
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
  local pairing = cjson.encode({pairing_id = ARGV[5], environment_id = ARGV[1], scope = record.scope, epoch = epoch,
    admin_binding = ARGV[8], created_at_ms = now, last_used_at_ms = now,
    operator = record.operator, issued_via = record.issued_via, public_base_url = record.public_base_url})
  redis.call('SET', KEYS[3], pairing, 'PX', ARGV[6])
  redis.call('ZADD', KEYS[4], now, ARGV[5])
  if record.operator and record.operator ~= '' then
    redis.call('HSET', KEYS[7], ARGV[5], record.operator)
  end
end
return {1, encoded, 0, paired, bound}
`

const sessionScript = clockLua + revokedLua + `
local raw = redis.call('GET', KEYS[1])
if not raw then return {0} end
local record = cjson.decode(raw)
if record.environment_id ~= ARGV[1] or record.scope ~= ARGV[4] or record.expires_at_ms <= now then
  return {0}
end
-- A session from before the environment's last revocation is revoked with it.
if (tonumber(record.epoch) or 0) ~= tonumber(redis.call('GET', KEYS[2]) or '0') then return {0} end
-- And one issued to an operator the roster dropped since.
if revoked(KEYS[3], record.operator, record.issued_at_ms) then return {0} end
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
// fault: a credential of another environment, scope, revocation epoch,
// administrator key or revoked operator is deleted on sight, and one that
// was already spent is simply absent.
//
// Returns {0} absent or revoked, {4} the administrator key changed since the
// pairing, {2} a key collision, {6, sealed} spent within the grace with the
// sealed answer of that renewal, {1, session, 0, pairing_id} renewed.
const refreshScript = clockLua + revokedLua + `
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
local function drop()
  redis.call('DEL', KEYS[1])
  redis.call('ZREM', KEYS[4], pairing.pairing_id or '')
  redis.call('HDEL', KEYS[8], pairing.pairing_id or '')
end
if pairing.environment_id ~= ARGV[1] or pairing.scope ~= ARGV[2] or (tonumber(pairing.epoch) or 0) ~= epoch
  or revoked(KEYS[7], pairing.operator, pairing.created_at_ms) then
  drop()
  return {0}
end
if pairing.admin_binding ~= ARGV[3] then
  drop()
  return {4}
end
pairing.last_used_at_ms = now
local session = cjson.encode({session_id = ARGV[4], environment_id = ARGV[1], scope = pairing.scope, epoch = epoch,
  pairing_id = pairing.pairing_id, expires_at_ms = now + tonumber(ARGV[5]), issued_at_ms = now,
  operator = pairing.operator, issued_via = pairing.issued_via, public_base_url = pairing.public_base_url})
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
const upgradeScript = clockLua + revokedLua + expirePairingsLua + `
local raw = redis.call('GET', KEYS[1])
if not raw then return {0} end
local record = cjson.decode(raw)
local epoch = tonumber(redis.call('GET', KEYS[4]) or '0')
if record.environment_id ~= ARGV[1] or record.scope ~= ARGV[2] or record.expires_at_ms <= now or (tonumber(record.epoch) or 0) ~= epoch
  or revoked(KEYS[6], record.operator, record.issued_at_ms) then
  return {0}
end
if record.pairing_id and record.pairing_id ~= '' then return {5} end
expirePairings(KEYS[3], KEYS[5], now, tonumber(ARGV[5]))
if tonumber(redis.call('ZCARD', KEYS[3])) >= tonumber(ARGV[6]) then return {3} end
if redis.call('EXISTS', KEYS[2]) == 1 then return {2} end
record.pairing_id = ARGV[3]
record.epoch = epoch
local encoded = cjson.encode(record)
redis.call('SET', KEYS[1], encoded, 'PX', record.expires_at_ms - now, 'XX')
redis.call('SET', KEYS[2], cjson.encode({pairing_id = ARGV[3], environment_id = ARGV[1], scope = record.scope, epoch = epoch,
  admin_binding = ARGV[4], created_at_ms = now, last_used_at_ms = now,
  operator = record.operator, issued_via = record.issued_via, public_base_url = record.public_base_url}), 'PX', ARGV[5])
redis.call('ZADD', KEYS[3], now, ARGV[3])
if record.operator and record.operator ~= '' then
  redis.call('HSET', KEYS[5], ARGV[3], record.operator)
end
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
redis.call('HDEL', KEYS[3], pairing.pairing_id)
return {1}
`

// revokeAllScript moves the environment to a new revocation epoch: every
// session and renewal credential of an earlier epoch fails its next check.
// The roster stays: it is the admin's, not a credential.
const revokeAllScript = `
local epoch = redis.call('INCR', KEYS[1])
local pairings = redis.call('ZCARD', KEYS[2])
redis.call('DEL', KEYS[2])
redis.call('DEL', KEYS[3])
return {1, epoch, pairings}
`

// activeScript counts the pairings used within the idle lifetime.
const activeScript = clockLua + expirePairingsLua + `
expirePairings(KEYS[1], KEYS[2], now, tonumber(ARGV[1]))
return {1, redis.call('ZCARD', KEYS[1])}
`

// rosterScript applies the admin's operator roster once per digest.
//
// KEYS: roster, members, named, revoked, pairings. ARGV: digest,
// operators_digest, how long a revocation is kept, then the operators.
//
// Returns {2, roster} the digest was applied already with this list, {3} the
// digest was applied with another list, {1, roster, pairings revoked,
// operators revoked...} applied. An operator is revoked when the previous
// roster named them or a pairing was issued to them, and the new one does
// not: their pairings leave the bound and the index now, and their
// credentials fail their next check (revokedLua).
const rosterScript = clockLua + `
local raw = redis.call('GET', KEYS[1])
if raw then
  local applied = cjson.decode(raw)
  if applied.digest == ARGV[1] then
    if applied.operators_digest == ARGV[2] then return {2, raw} end
    return {3}
  end
end
local keep = {}
for i = 4, #ARGV do keep[ARGV[i]] = true end
local removed = {}
for _, operator in ipairs(redis.call('SMEMBERS', KEYS[2])) do
  if not keep[operator] then removed[operator] = true end
end
local named = redis.call('HGETALL', KEYS[3])
local pairings = 0
for i = 1, #named, 2 do
  if not keep[named[i + 1]] then
    removed[named[i + 1]] = true
    redis.call('HDEL', KEYS[3], named[i])
    redis.call('ZREM', KEYS[5], named[i])
    pairings = pairings + 1
  end
end
local names = {}
for operator in pairs(removed) do
  redis.call('ZADD', KEYS[4], now, operator)
  table.insert(names, operator)
end
table.sort(names)
-- A revocation older than every credential it could still end is dropped:
-- those were deleted at their next use or have idled out.
redis.call('ZREMRANGEBYSCORE', KEYS[4], '-inf', '(' .. tostring(now - tonumber(ARGV[3])))
redis.call('DEL', KEYS[2])
for i = 4, #ARGV, 500 do
  redis.call('SADD', KEYS[2], unpack(ARGV, i, math.min(i + 499, #ARGV)))
end
local encoded = cjson.encode({digest = ARGV[1], operators_digest = ARGV[2], operators = #ARGV - 3, applied_at_ms = now})
redis.call('SET', KEYS[1], encoded)
local out = {1, encoded, pairings}
for _, name in ipairs(names) do table.insert(out, name) end
return out
`

// rosterReadScript is the applied roster, if any, and how many pairings
// were issued to operators.
const rosterReadScript = `
return {1, redis.call('GET', KEYS[1]) or '', redis.call('HLEN', KEYS[2])}
`
