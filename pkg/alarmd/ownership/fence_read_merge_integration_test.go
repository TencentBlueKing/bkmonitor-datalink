// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package ownership

import (
	"context"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
)

// preMergeFenceLua and preMergeCheckFence are the owner fence and the
// check_fence script as they read before the fence's reads were merged
// (f12adba65): one HGET per field, and check_fence's record in an HMGET of
// its own after the fence. Frozen copies, so the merged fence is held to the
// text it replaced rather than to itself. Nothing should edit them.
const preMergeFenceLua = `
redis.replicate_commands()
local function redis_now_ms()
  local now = redis.call('TIME')
  return tonumber(now[1]) * 1000 + math.floor(tonumber(now[2]) / 1000)
end
local function current_content_scope(assignment_key, now_ms)
  local fields = redis.call('HMGET', assignment_key, 'content_scope', 'pending_content_scope', 'effective_at_ms')
  local scope = fields[1] or ''
  local pending = fields[2]
  local effective = tonumber(fields[3] or '0')
  if pending and pending ~= '' and effective > 0 and now_ms >= effective then
    redis.call('HSET', assignment_key, 'content_scope', pending)
    redis.call('HDEL', assignment_key, 'pending_content_scope', 'effective_at_ms')
    scope = pending
  end
  return scope
end
local function draining_holder(assignment_key, owner_id, now_ms)
  local grace = redis.call('HMGET', assignment_key, 'releasing_worker_id', 'effective_at_ms')
  return grace[1] == owner_id and tonumber(grace[2] or '0') > now_ms
end
local function fence_refusal(assignment_key, ownership_key, require_assignment, owner_id, epoch, token, content_scope, now_ms)
  if require_assignment == '1' then
    local desired = redis.call('HGET', assignment_key, 'desired_worker_id')
    if not desired then return 'NOT_DESIRED' end
    if desired ~= owner_id and not draining_holder(assignment_key, owner_id, now_ms) then return 'NOT_DESIRED' end
  end
  if redis.call('HGET', ownership_key, 'execution_disposition') ~= 'ACTIVE' then return 'STALE' end
  if redis.call('HGET', ownership_key, 'owner_id') ~= owner_id or
     redis.call('HGET', ownership_key, 'owner_epoch') ~= epoch or
     redis.call('HGET', ownership_key, 'lease_token') ~= token or
     tonumber(redis.call('HGET', ownership_key, 'deadline_ms') or '0') <= now_ms then return 'STALE' end
  if require_assignment == '1' and content_scope and content_scope ~= '' then
    local named = current_content_scope(assignment_key, now_ms)
    if named ~= '' and named ~= content_scope then
      local change = redis.call('HMGET', assignment_key, 'pending_content_scope', 'effective_at_ms')
      local pending = change[1]
      local effective = tonumber(change[2] or '0')
      if not pending or pending ~= content_scope or effective <= 0 then return 'CONTENT_MOVED' end
    end
  end
  return nil
end
`

const preMergeCheckFence = preMergeFenceLua + `
local require_assignment = ARGV[1]
local owner_id = ARGV[2]
local epoch = ARGV[3]
local token = ARGV[4]
local content_scope = ARGV[5] or ''
local now_ms = redis_now_ms()
local empty = {'', '', '', '', '', '', '', '', '', ''}
local refusal = fence_refusal(KEYS[1], KEYS[2], require_assignment, owner_id, epoch, token, content_scope, now_ms)
if refusal then return {refusal, empty[1], empty[2], empty[3], empty[4], empty[5], empty[6], empty[7], empty[8], empty[9], empty[10]} end
local record = {'', '', '', '', '', '', '', '', '', ''}
if require_assignment == '1' then
  local fields = redis.call('HMGET', KEYS[1], 'desired_worker_id', 'assignment_generation',
    'record_revision', 'control_epoch', 'placement_reason', 'assigned_at_ms',
    'content_scope', 'pending_content_scope', 'effective_at_ms', 'timeline_record_revision')
  for index = 1, 10 do
    if fields[index] then record[index] = fields[index] end
  end
end
return {'VALID', record[1], record[2], record[3], record[4], record[5], record[6], record[7], record[8], record[9], record[10]}
`

const (
	mergeAssignmentKey = "fence-merge:{query-group-1}:assignment"
	mergeOwnershipKey  = "fence-merge:{query-group-1}:ownership"
	mergeFuture        = "{future}"
	mergePast          = "{past}"
)

type fenceMergeCase struct {
	name       string
	assignment map[string]string
	ownership  map[string]string
	args       []string
}

// fenceMergeCases covers every answer the fence gives and every branch that
// decides it: the desired worker and the move's grace, each comparison that
// makes a lease stale, the content scope with and without a pending change,
// a pending change that falls due during the run, and the Control Leader's
// shape that carries no assignment at all.
func fenceMergeCases() []fenceMergeCase {
	assignment := func(edit func(map[string]string)) map[string]string {
		fields := map[string]string{
			"desired_worker_id": "worker-1", "assignment_generation": "7", "record_revision": "3",
			"control_epoch": "2", "placement_reason": "rendezvous", "assigned_at_ms": "1000",
			"timeline_record_revision": "5",
		}
		if edit != nil {
			edit(fields)
		}
		return fields
	}
	ownership := func(edit func(map[string]string)) map[string]string {
		fields := map[string]string{
			"execution_disposition": "ACTIVE", "owner_id": "worker-1", "owner_epoch": "4",
			"lease_token": "token-1", "deadline_ms": mergeFuture,
		}
		if edit != nil {
			edit(fields)
		}
		return fields
	}
	args := func(scope string) []string { return []string{"1", "worker-1", "4", "token-1", scope} }
	moved := func(fields map[string]string) { fields["desired_worker_id"] = "worker-2" }
	grace := func(effective string) func(map[string]string) {
		return func(fields map[string]string) {
			moved(fields)
			fields["releasing_worker_id"], fields["effective_at_ms"] = "worker-1", effective
		}
	}
	scoped := func(scope, pending, effective string) func(map[string]string) {
		return func(fields map[string]string) {
			fields["content_scope"] = scope
			if pending != "" {
				fields["pending_content_scope"] = pending
			}
			if effective != "" {
				fields["effective_at_ms"] = effective
			}
		}
	}
	return []fenceMergeCase{
		{name: "valid", assignment: assignment(nil), ownership: ownership(nil), args: args("")},
		{name: "no desired worker", assignment: assignment(func(f map[string]string) { delete(f, "desired_worker_id") }), ownership: ownership(nil), args: args("")},
		{name: "another desired worker", assignment: assignment(moved), ownership: ownership(nil), args: args("")},
		{name: "draining holder inside its grace", assignment: assignment(grace(mergeFuture)), ownership: ownership(nil), args: args("")},
		{name: "draining holder past its grace", assignment: assignment(grace(mergePast)), ownership: ownership(nil), args: args("")},
		{name: "grace names another holder", assignment: assignment(func(f map[string]string) {
			grace(mergeFuture)(f)
			f["releasing_worker_id"] = "worker-3"
		}), ownership: ownership(nil), args: args("")},
		{name: "paused", assignment: assignment(nil), ownership: ownership(func(f map[string]string) { f["execution_disposition"] = "PAUSED" }), args: args("")},
		{name: "no disposition", assignment: assignment(nil), ownership: ownership(func(f map[string]string) { delete(f, "execution_disposition") }), args: args("")},
		{name: "another owner", assignment: assignment(nil), ownership: ownership(func(f map[string]string) { f["owner_id"] = "worker-2" }), args: args("")},
		{name: "another epoch", assignment: assignment(nil), ownership: ownership(func(f map[string]string) { f["owner_epoch"] = "5" }), args: args("")},
		{name: "another token", assignment: assignment(nil), ownership: ownership(func(f map[string]string) { f["lease_token"] = "token-2" }), args: args("")},
		{name: "lapsed lease", assignment: assignment(nil), ownership: ownership(func(f map[string]string) { f["deadline_ms"] = mergePast }), args: args("")},
		{name: "no deadline", assignment: assignment(nil), ownership: ownership(func(f map[string]string) { delete(f, "deadline_ms") }), args: args("")},
		{name: "no lease at all", assignment: assignment(nil), ownership: map[string]string{}, args: args("")},
		{name: "not desired and stale says not desired", assignment: assignment(moved), ownership: ownership(func(f map[string]string) { f["lease_token"] = "token-2" }), args: args("")},
		{name: "declared scope is the named one", assignment: assignment(scoped("scope-a", "", "")), ownership: ownership(nil), args: args("scope-a")},
		{name: "declared scope moved", assignment: assignment(scoped("scope-a", "", "")), ownership: ownership(nil), args: args("scope-b")},
		{name: "declared scope is the pending one", assignment: assignment(scoped("scope-a", "scope-b", mergeFuture)), ownership: ownership(nil), args: args("scope-b")},
		{name: "pending scope falls due, declared it", assignment: assignment(scoped("scope-a", "scope-b", mergePast)), ownership: ownership(nil), args: args("scope-b")},
		{name: "pending scope falls due, declared the old one", assignment: assignment(scoped("scope-a", "scope-b", mergePast)), ownership: ownership(nil), args: args("scope-a")},
		{name: "pending scope without its time", assignment: assignment(scoped("scope-a", "scope-b", "")), ownership: ownership(nil), args: args("scope-b")},
		{name: "record names no scope", assignment: assignment(nil), ownership: ownership(nil), args: args("scope-z")},
		{name: "stale lease with a moved scope says stale", assignment: assignment(scoped("scope-a", "", "")), ownership: ownership(func(f map[string]string) { f["deadline_ms"] = mergePast }), args: args("scope-b")},
		{name: "draining holder with a moved scope", assignment: assignment(func(f map[string]string) {
			grace(mergeFuture)(f)
			f["content_scope"] = "scope-a"
		}), ownership: ownership(nil), args: args("scope-b")},
		{name: "control leader shape", assignment: nil, ownership: ownership(nil), args: []string{"0", "worker-1", "4", "token-1", ""}},
		{name: "control leader shape ignores a declared scope", assignment: nil, ownership: ownership(nil), args: []string{"0", "worker-1", "4", "token-1", "scope-a"}},
	}
}

func fenceMergeClient(t *testing.T) *redis.Client {
	t.Helper()
	server := redistest.Start(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr})
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func fenceMergeServerMillis(t *testing.T, client *redis.Client) int64 {
	t.Helper()
	now, err := client.Time(context.Background()).Result()
	if err != nil {
		t.Fatal(err)
	}
	return now.UnixMilli()
}

// seedFenceMergeCase writes the case's records with its relative times
// resolved a minute either side of now, an instant on the server's clock the
// caller reads once per case: far enough from it that both runs of one case
// read the same verdict, and the same instant for both so the records they
// leave can be compared field for field.
func seedFenceMergeCase(t *testing.T, client *redis.Client, c fenceMergeCase, now int64) {
	t.Helper()
	ctx := context.Background()
	if err := client.Del(ctx, mergeAssignmentKey, mergeOwnershipKey).Err(); err != nil {
		t.Fatal(err)
	}
	resolve := func(value string) string {
		switch value {
		case mergeFuture:
			return strconv.FormatInt(now+60_000, 10)
		case mergePast:
			return strconv.FormatInt(now-60_000, 10)
		}
		return value
	}
	for key, fields := range map[string]map[string]string{mergeAssignmentKey: c.assignment, mergeOwnershipKey: c.ownership} {
		for field, value := range fields {
			if err := client.HSet(ctx, key, field, resolve(value)).Err(); err != nil {
				t.Fatal(err)
			}
		}
	}
}

type fenceMergeOutcome struct {
	reply      interface{}
	assignment map[string]string
	ownership  map[string]string
}

func runFenceMergeCase(t *testing.T, client *redis.Client, script *redis.Script, c fenceMergeCase, now int64) fenceMergeOutcome {
	t.Helper()
	ctx := context.Background()
	seedFenceMergeCase(t, client, c, now)
	args := make([]interface{}, len(c.args))
	for index, arg := range c.args {
		args[index] = arg
	}
	reply, err := script.Eval(ctx, client, []string{mergeAssignmentKey, mergeOwnershipKey}, args...).Result()
	if err != nil {
		t.Fatalf("%s: script error = %v", c.name, err)
	}
	assignment, err := client.HGetAll(ctx, mergeAssignmentKey).Result()
	if err != nil {
		t.Fatal(err)
	}
	ownership, err := client.HGetAll(ctx, mergeOwnershipKey).Result()
	if err != nil {
		t.Fatal(err)
	}
	return fenceMergeOutcome{reply: reply, assignment: assignment, ownership: ownership}
}

// TestTheMergedFenceAnswersAsTheFenceItReplaced runs check_fence before and
// after the merge on the same records, case by case, and wants the same
// reply -- the verdict and the eleven-element record -- and the same records
// afterwards, which is where a promotion of a pending scope shows.
func TestTheMergedFenceAnswersAsTheFenceItReplaced(t *testing.T) {
	client := fenceMergeClient(t)
	before := redis.NewScript(preMergeCheckFence)
	verdicts := map[string]bool{}
	for _, c := range fenceMergeCases() {
		now := fenceMergeServerMillis(t, client)
		want := runFenceMergeCase(t, client, before, c, now)
		got := runFenceMergeCase(t, client, checkFenceScript, c, now)
		// A valid reply now also says the server instant it was read at
		// (A2), the one element the fence it replaced did not send.
		if reply, ok := got.reply.([]interface{}); ok && len(reply) == 12 {
			observed, isInt := reply[11].(int64)
			if !isInt || observed < now || observed > now+60_000 {
				t.Errorf("%s: the reply's server instant %v, want TIME's, at or after %d", c.name, reply[11], now)
			}
			got.reply = reply[:11]
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: merged fence = %+v, the fence it replaced = %+v", c.name, got, want)
		}
		if reply, ok := want.reply.([]interface{}); ok && len(reply) > 0 {
			verdicts[reply[0].(string)] = true
		}
	}
	// The table has to reach every answer, or an answer the merge changed
	// could be one no case asked for.
	for _, verdict := range []string{"VALID", "NOT_DESIRED", "STALE", "CONTENT_MOVED"} {
		if !verdicts[verdict] {
			t.Errorf("no case answered %s", verdict)
		}
	}
}

// commandCounts is how many times each command ran on the server since the
// last CONFIG RESETSTAT, commands a script ran included.
func commandCounts(t *testing.T, client *redis.Client) map[string]int {
	t.Helper()
	info, err := client.Info(context.Background(), "commandstats").Result()
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, line := range strings.Split(info, "\n") {
		name, rest, found := strings.Cut(strings.TrimSpace(line), ":")
		if !found || !strings.HasPrefix(name, "cmdstat_") {
			continue
		}
		for _, part := range strings.Split(rest, ",") {
			if value, ok := strings.CutPrefix(part, "calls="); ok {
				calls, err := strconv.Atoi(value)
				if err != nil {
					t.Fatal(err)
				}
				counts[strings.TrimPrefix(name, "cmdstat_")] = calls
			}
		}
	}
	return counts
}

// TestAFenceCheckRunsTimeAndTwoHashReadsOnTheServer pins what one check_fence
// costs the server: TIME and one HMGET per record, where the fence it
// replaced ran six HGETs and an HMGET for the record (two with a content
// scope declared). The old text runs beside it so the reading is known to
// count commands a script runs.
func TestAFenceCheckRunsTimeAndTwoHashReadsOnTheServer(t *testing.T) {
	client := fenceMergeClient(t)
	ctx := context.Background()
	cases := fenceMergeCases()
	valid, scoped := cases[0], cases[15]
	if valid.name != "valid" || scoped.name != "declared scope is the named one" {
		t.Fatalf("cases moved: %q, %q", valid.name, scoped.name)
	}
	for _, run := range []struct {
		name   string
		script *redis.Script
		c      fenceMergeCase
		want   map[string]int
	}{
		{"merged", checkFenceScript, valid, map[string]int{"time": 1, "hmget": 2}},
		{"merged with a scope", checkFenceScript, scoped, map[string]int{"time": 1, "hmget": 2}},
		{"replaced", redis.NewScript(preMergeCheckFence), valid, map[string]int{"time": 1, "hget": 6, "hmget": 1}},
		{"replaced with a scope", redis.NewScript(preMergeCheckFence), scoped, map[string]int{"time": 1, "hget": 6, "hmget": 2}},
	} {
		seedFenceMergeCase(t, client, run.c, fenceMergeServerMillis(t, client))
		if err := run.script.Load(ctx, client).Err(); err != nil {
			t.Fatal(err)
		}
		if err := client.ConfigResetStat(ctx).Err(); err != nil {
			t.Fatal(err)
		}
		args := make([]interface{}, len(run.c.args))
		for index, arg := range run.c.args {
			args[index] = arg
		}
		if err := run.script.EvalSha(ctx, client, []string{mergeAssignmentKey, mergeOwnershipKey}, args...).Err(); err != nil {
			t.Fatal(err)
		}
		counts := commandCounts(t, client)
		for _, command := range []string{"time", "hget", "hmget", "hset", "hdel"} {
			if counts[command] != run.want[command] {
				t.Errorf("%s: %s ran %d times, want %d (all: %v)", run.name, command, counts[command], run.want[command], counts)
			}
		}
	}
}
