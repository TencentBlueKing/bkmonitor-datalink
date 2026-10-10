// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storecensus

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
)

func startRedis(t *testing.T) *redis.Client {
	t.Helper()
	executable := redistest.Server(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	_, port, _ := net.SplitHostPort(address)
	command := exec.Command(executable, "--bind", "127.0.0.1", "--port", port, "--save", "", "--appendonly", "no",
		"--dir", t.TempDir(), "--daemonize", "no", "--loglevel", "warning")
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_ = command.Wait()
	})
	client := redis.NewClient(&redis.Options{Addr: address, DialTimeout: time.Second, ReadTimeout: 5 * time.Second})
	t.Cleanup(func() { _ = client.Close() })
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if client.Ping(context.Background()).Err() == nil {
			return client
		}
		if time.Now().After(deadline) {
			t.Fatalf("redis-server did not become ready: %s", output.String())
		}
	}
}

// fill writes count keys of family prefix, each value size bytes.
func fill(t *testing.T, client *redis.Client, prefix string, count, size int) {
	t.Helper()
	ctx := context.Background()
	value := strings.Repeat("v", size)
	for start := 0; start < count; start += 500 {
		if _, err := client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
			for index := start; index < min(start+500, count); index++ {
				pipe.Set(ctx, fmt.Sprintf("%s:%064x", prefix, index), value, 0)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func familyNamed(result Result, name string) (Family, bool) {
	for _, family := range result.Families {
		if family.Name == name {
			return family, true
		}
	}
	return Family{}, false
}

// A store of no more keys than a census weighs is walked whole: every key,
// its exact size, no scaling.
func TestASmallStoreIsCountedExactly(t *testing.T) {
	client := startRedis(t)
	fill(t, client, "alarmd:state", 300, 200)
	fill(t, client, "alarmd:gap", 100, 20)
	result, err := Measure(context.Background(), client, "runtime", nil, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	state, _ := familyNamed(result, "alarmd:state:*")
	gap, _ := familyNamed(result, "alarmd:gap:*")
	if !result.Exact || result.Keys != 400 || result.Weighed != 400 || state.Keys != 300 || gap.Keys != 100 ||
		state.Samples != 300 || gap.Samples != 100 {
		t.Fatalf("census = %+v, want both families counted exactly", result)
	}
	var total int64
	for _, key := range []string{fmt.Sprintf("alarmd:state:%064x", 0), fmt.Sprintf("alarmd:gap:%064x", 0)} {
		size, err := client.MemoryUsage(context.Background(), key, memorySamples).Result()
		if err != nil {
			t.Fatal(err)
		}
		total += size
	}
	if state.Bytes <= gap.Bytes || state.Bytes/300+gap.Bytes/100 != float64(total) {
		t.Fatalf("bytes state %.0f gap %.0f, want each key's MEMORY USAGE summed (%d for one of each)", state.Bytes, gap.Bytes, total)
	}
}

// A larger store is sampled and scaled to its key count: each family's keys
// and bytes come within the sampling error of what it holds, and the error
// is what its Samples says it is.
func TestALargeStoreIsSampledAndScaled(t *testing.T) {
	client := startRedis(t)
	fill(t, client, "alarmd:state", 6000, 400)
	fill(t, client, "python:cache", 2000, 40)
	result, err := Measure(context.Background(), client, "runtime", nil, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Exact || result.Keys != 8000 || result.Weighed != SampleKeys {
		t.Fatalf("census = %+v, want %d of 8000 keys sampled", result, SampleKeys)
	}
	oneState, err := client.MemoryUsage(context.Background(), fmt.Sprintf("alarmd:state:%064x", 1), memorySamples).Result()
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]float64{"alarmd:state:*": 6000, "python:cache:*": 2000} {
		family, found := familyNamed(result, name)
		share := want / 8000
		// Five standard errors of a share drawn SampleKeys times.
		tolerance := 5 * math.Sqrt(share*(1-share)/float64(SampleKeys)) * 8000
		if !found || math.Abs(family.Keys-want) > tolerance {
			t.Fatalf("%s = %+v, want %.0f keys within %.0f", name, family, want, tolerance)
		}
	}
	state, _ := familyNamed(result, "alarmd:state:*")
	if math.Abs(state.Bytes-state.Keys*float64(oneState)) > float64(oneState) {
		t.Fatalf("state bytes %.0f for %.0f keys, want %d each", state.Bytes, state.Keys, oneState)
	}
}

// Past MaxFamilies the smallest families by bytes are one, OtherFamily, and
// nothing is lost in the folding.
func TestFamiliesPastTheBoundAreFoldedIntoOther(t *testing.T) {
	var families []Family
	for index := range MaxFamilies + 5 {
		families = append(families, Family{Name: "f" + strconv.Itoa(index), Samples: 1, Keys: 1, Bytes: float64(100 + index)})
	}
	folded := fold(families)
	if len(folded) != MaxFamilies || folded[len(folded)-1].Name != OtherFamily || folded[0].Bytes != float64(100+MaxFamilies+4) {
		t.Fatalf("folded = %+v, want %d families, the largest first and the rest as %s", folded, MaxFamilies, OtherFamily)
	}
	var keys float64
	for _, family := range folded {
		keys += family.Keys
	}
	if keys != float64(MaxFamilies+5) || folded[len(folded)-1].Samples != 6 {
		t.Fatalf("folded = %+v, want every key kept and six families in %s", folded, OtherFamily)
	}
}

// An empty store is a census of nothing, not an error.
func TestAnEmptyStoreIsACensusOfNothing(t *testing.T) {
	client := startRedis(t)
	result, err := Measure(context.Background(), client, "runtime", nil, time.Now)
	if err != nil || result.Keys != 0 || len(result.Families) != 0 || !result.Exact {
		t.Fatalf("empty store = (%+v, %v), want an exact census of nothing", result, err)
	}
}

// A cluster client is refused by name: its keys and counts are per node.
func TestAClusterIsNotMeasured(t *testing.T) {
	cluster := redis.NewClusterClient(&redis.ClusterOptions{Addrs: []string{"127.0.0.1:1"}})
	defer cluster.Close()
	if _, err := Measure(context.Background(), cluster, "runtime", nil, time.Now); err != ErrUnsupported {
		t.Fatalf("cluster = %v, want ErrUnsupported", err)
	}
}

// A key drawn and gone before it was weighed was one of the store's keys
// when it was drawn: it counts in its family's keys and adds nothing to its
// bytes, and a sample is scaled by every draw, so the families' keys add up
// to the store's count.
func TestAKeyGoneBeforeItWasWeighedCountsInItsFamilyWithNoBytes(t *testing.T) {
	families, weighed, gone := estimate(defaultVocabulary, []string{"alarmd:1", "alarmd:2", "celery:1"}, []int64{10, -1, 5}, 300, false)
	if weighed != 2 || gone != 1 || len(families) != 2 {
		t.Fatalf("estimate = %+v weighed %d gone %d, want two families of the three keys drawn", families, weighed, gone)
	}
	want := map[string]Family{"alarmd:*": {Name: "alarmd:*", Samples: 2, Keys: 200, Bytes: 1000}, "celery:*": {Name: "celery:*", Samples: 1, Keys: 100, Bytes: 500}}
	for _, family := range families {
		if family != want[family.Name] {
			t.Fatalf("family %+v, want %+v", family, want[family.Name])
		}
	}
	exact, _, _ := estimate(defaultVocabulary, []string{"alarmd:1", "alarmd:2"}, []int64{10, -1}, 300, true)
	if len(exact) != 1 || exact[0].Keys != 2 || exact[0].Bytes != 10 {
		t.Fatalf("an exact census = %+v, want its keys as counted", exact)
	}
}

// A walk gives every key once; a store that grew past twice the count it
// was walked for is not walked whole, and its census is not exact.
func TestAWalkThatOverrunsIsNotACensusOfEveryKey(t *testing.T) {
	client := startRedis(t)
	fill(t, client, "alarmd:state", 300, 20)
	keys, whole, err := walk(context.Background(), client)
	if err != nil || !whole || len(keys) != 300 {
		t.Fatalf("walk = %d keys whole %v err %v, want all 300", len(keys), whole, err)
	}
	fill(t, client, "alarmd:gap", 2*SampleKeys, 20)
	if keys, whole, err := walk(context.Background(), client); err != nil || whole || keys != nil {
		t.Fatalf("walk past twice its count = %d keys whole %v err %v, want no census of every key", len(keys), whole, err)
	}
	// Counted at 300 and walked past twice that: sampled instead.
	if drawn, exact, err := gather(context.Background(), client, 300); err != nil || exact || len(drawn) != SampleKeys {
		t.Fatalf("gather = %d keys exact %v err %v, want %d drawn and not exact", len(drawn), exact, err, SampleKeys)
	}
	// SCAN may give a key twice while the server rehashes: it is walked once.
	seen := map[string]struct{}{}
	walked := appendNew(appendNew(nil, seen, []string{"a", "b"}), seen, []string{"b", "c", "c"})
	if strings.Join(walked, ",") != "a,b,c" {
		t.Fatalf("pages walked = %v, want each key once", walked)
	}
}

// A census names its families with the vocabulary it is given: a
// deployment's prefix it knows stays, one it does not is folded.
func TestACensusNamesFamiliesWithItsVocabulary(t *testing.T) {
	client := startRedis(t)
	fill(t, client, "deployment[x]:state", 10, 20)
	named := func(vocabulary *Vocabulary) string {
		t.Helper()
		result, err := Measure(context.Background(), client, "runtime", vocabulary, time.Now)
		if err != nil || len(result.Families) != 1 {
			t.Fatalf("census = %+v, %v", result, err)
		}
		return result.Families[0].Name
	}
	if got := named(NewVocabulary("deployment[x]")); got != "deployment[x]:state:*" {
		t.Errorf("with the prefix = %q", got)
	}
	if got := named(nil); got != "*:state:*" {
		t.Errorf("without it = %q", got)
	}
}
