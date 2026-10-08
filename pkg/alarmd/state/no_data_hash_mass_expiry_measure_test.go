// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package state

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
)

// massExpiryGroupField is a no-data group field as the store writes it: the
// group prefix, then the group key -- host dimensions and the no-data tag,
// escaped the way nodata.Group.Key writes them -- and an absence value.
func massExpiryGroupField(index int, wide bool) HashField {
	ip := fmt.Sprintf("10.%d.%d.%d", index>>16&0xff, index>>8&0xff, index&0xff)
	key := "bk_target_cloud_id=0,bk_target_ip=" + ip + "," + contract.NoDataDimensionTag + "=true"
	if wide {
		// A group of a disk-level strategy: four dimensions, one of them a
		// long mount path -- about three times the host group's key.
		key = "bk_target_cloud_id=0,bk_target_ip=" + ip + ",device_name=/dev/mapper/vg_data-lv_data_" + strconv.Itoa(index) +
			",mount_point=/data/containers/overlay2/volumes/app-" + strconv.Itoa(index) + "/merged," + contract.NoDataDimensionTag + "=true"
	}
	value, _ := json.Marshal(noDataGroupAbsenceValue{LastSeen: 1_790_000_000, FirstAbsent: 1_790_000_060})
	return HashField{Name: noDataGroupPrefix + key, Value: value}
}

// TestMeasureNoDataHashMassExpiry measures decision-018's largest single
// write: a Plan whose remembered groups all expire in one round, so one
// ApplyHashDelta carries every one of them in Del and the script deletes
// them in one call while Redis serves nobody else.
//
// It is a measurement, not a check: run with ALARMD_MEASURE_MASS_EXPIRY=1.
// The server's own SLOWLOG gives the script's execution time -- the time
// Redis is blocked -- and the client clock gives the call as the store sees
// it, argument transfer included.
func TestMeasureNoDataHashMassExpiry(t *testing.T) {
	if os.Getenv("ALARMD_MEASURE_MASS_EXPIRY") == "" {
		t.Skip("a measurement; set ALARMD_MEASURE_MASS_EXPIRY=1 to run it")
	}
	executable := redistest.Server(t)
	address := reserveTCPAddress(t)
	startRedisServer(t, executable, address)
	backend, err := NewRedisBackend(RedisBackendOptions{Address: address, DialTimeout: time.Second,
		ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, PoolSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	waitRedisReady(t, backend)
	admin := redis.NewClient(&redis.Options{Addr: address, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second})
	t.Cleanup(func() { _ = admin.Close() })
	ctx := context.Background()
	for _, setting := range [][2]string{{"slowlog-log-slower-than", "0"}, {"slowlog-max-len", "100000"}} {
		if err := admin.ConfigSet(ctx, setting[0], setting[1]).Err(); err != nil {
			t.Fatal(err)
		}
	}
	version := admin.Info(ctx, "server").Val()

	header, _ := json.Marshal(noDataHashHeader{Schema: "alarmd-plan-no-data-hash", Version: 3,
		Identity:       execution.PlanNoDataIdentity{Plan: execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "9"}, StateGeneration: "generation"},
		MarkerRevision: 41, ApplyVersion: execution.ApplyVersion{StateApplyEpoch: 1, EvaluationTime: 1_790_003_600, SlotDigest: "slot"},
		MemoryDigest: "memory", ScheduleRevision: "schedule", RosterVersion: "roster", PresentAsOf: 1_790_003_600})
	next := append([]byte(nil), header...)
	next = append(next[:len(next)-1], []byte(`,"tracking_exhausted_at":1790003660}`)...)

	runs := 30
	if text := os.Getenv("ALARMD_MEASURE_RUNS"); text != "" {
		runs, _ = strconv.Atoi(text)
	}
	type shape struct {
		groups int
		wide   bool
	}
	shapes := []shape{{1_000, false}, {10_000, false}, {50_000, false}, {100_000, false}, {100_000, true}}
	for _, current := range shapes {
		groups := current.groups
		fields := make([]HashField, groups)
		names := make([]string, groups)
		fieldBytes := 0
		for index := range fields {
			fields[index] = massExpiryGroupField(index, current.wide)
			names[index] = fields[index].Name
			fieldBytes += len(fields[index].Name)
		}
		var server, client []time.Duration
		for run := 0; run < runs; run++ {
			key := fmt.Sprintf("alarmd.nodata:{q}:%d:%d", groups, run)
			// The memory as it stands before the round: header and every
			// group, written outside the measurement.
			pipe := admin.Pipeline()
			pipe.HSet(ctx, key, noDataHeaderField, header)
			for start := 0; start < groups; start += 1000 {
				values := make([]interface{}, 0, 2000)
				for _, field := range fields[start:min(start+1000, groups)] {
					values = append(values, field.Name, field.Value)
				}
				pipe.HSet(ctx, key, values...)
			}
			pipe.PExpire(ctx, key, time.Hour)
			if _, err := pipe.Exec(ctx); err != nil {
				t.Fatal(err)
			}
			if got := admin.HLen(ctx, key).Val(); got != int64(groups+1) {
				t.Fatalf("hash holds %d fields, want %d", got, groups+1)
			}
			if err := admin.Do(ctx, "SLOWLOG", "RESET").Err(); err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			outcome, err := backend.ApplyHashDelta(ctx, HashDeltaWrite{Key: key, HeaderField: noDataHeaderField,
				ExpectedDigest: HeaderDigest(header), Header: next, Del: names, TTL: time.Hour})
			elapsed := time.Since(started)
			if err != nil || outcome.Status != HashDeltaApplied {
				t.Fatalf("%d groups: %+v, %v", groups, outcome, err)
			}
			if got := admin.HLen(ctx, key).Val(); got != 1 {
				t.Fatalf("after the expiry the hash holds %d fields, want the header alone", got)
			}
			entries, err := admin.SlowLogGet(ctx, -1).Result()
			if err != nil {
				t.Fatal(err)
			}
			var script time.Duration
			for _, entry := range entries {
				if len(entry.Args) > 0 && (entry.Args[0] == "evalsha" || entry.Args[0] == "EVALSHA" || entry.Args[0] == "eval" || entry.Args[0] == "EVAL") {
					script += entry.Duration
				}
			}
			if script == 0 {
				t.Fatalf("%d groups: no script in the slow log: %+v", groups, entries)
			}
			server = append(server, script)
			client = append(client, elapsed)
			admin.Del(ctx, key)
		}
		t.Logf("groups=%d wide=%v del_arg_bytes=%d runs=%d  server(script) p50=%v p99=%v max=%v  client(call) p50=%v p99=%v max=%v",
			groups, current.wide, fieldBytes, runs, quantile(server, 0.50), quantile(server, 0.99), quantile(server, 1),
			quantile(client, 0.50), quantile(client, 0.99), quantile(client, 1))
	}
	t.Logf("server: %s", firstLines(version, "redis_version", "arch_bits", "os"))
}

func quantile(samples []time.Duration, q float64) time.Duration {
	sorted := append([]time.Duration(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	index := int(q*float64(len(sorted)-1) + 0.5)
	return sorted[index].Round(10 * time.Microsecond)
}

func firstLines(info string, names ...string) string {
	out := ""
	for _, line := range splitLines(info) {
		for _, name := range names {
			if len(line) > len(name) && line[:len(name)+1] == name+":" {
				out += line + " "
			}
		}
	}
	return out
}

func splitLines(text string) []string {
	var lines []string
	start := 0
	for index := 0; index < len(text); index++ {
		if text[index] == '\n' {
			line := text[start:index]
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			lines = append(lines, line)
			start = index + 1
		}
	}
	return lines
}
