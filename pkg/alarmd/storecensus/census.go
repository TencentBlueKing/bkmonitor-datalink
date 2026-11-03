// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package storecensus measures what a Redis the process stores into holds,
// by key family, from the store's side: keys drawn by the server, each
// weighed by the server, scaled to the server's key count. The process's own
// counters say how many commands and bytes it sends; they cannot say what
// the store keeps of them, how long, or next to what the store's other
// users keep. A design that adds keys starts from this.
//
// A store of at most SampleKeys keys is walked whole and every key weighed:
// the census is exact. A larger one is sampled: SampleKeys draws of
// RANDOMKEY, with replacement, each weighed with MEMORY USAGE, and a
// family's keys and bytes are its share of the draws times DBSIZE. So is a
// small store that grew past twice its count while it was walked: what was
// walked of it is not every key, and not a sample either.
//
// A key drawn and gone before it was weighed was one of the DBSIZE keys when
// it was drawn: it counts in its family's keys and adds nothing to its
// bytes, and the share is of every draw. The families' keys then add up to
// DBSIZE, and a family whose keys come and go quickly - locks, leases -
// reads as its count with next to no bytes. Leaving such a key out would
// have shared its part of DBSIZE among the others, and undercounted its
// own family.
//
// The error, by the sample size N = SampleKeys. A family holding a share p
// of the keys is drawn n = p*N times on average, and its key estimate has a
// relative standard error of sqrt((1-p)/(p*N)): 3.1% at p = 0.5, 9.4% at
// p = 0.1, 31% at p = 0.01, and a family of one key in a thousand is drawn
// once or not at all. Its bytes carry, beside that, the spread of its keys'
// sizes: a relative standard error of sqrt((1-p+c*c)/(p*N)), c being the
// coefficient of variation of the family's key sizes - so a family of equal
// keys is as good as its key count, and one of few very large keys among
// many small ones is known only as well as the large ones happen to be
// drawn. Samples, reported with every family, is n: the error of a reading
// is read from it, 1/sqrt(n) at the least. MEMORY USAGE weighs an aggregate
// - a hash, a set - from a sample of its elements, as the server does.
package storecensus

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/go-redis/redis/v8"
)

const (
	// SampleKeys is how many keys a census weighs.
	SampleKeys = 1024
	// MaxFamilies is how many families a census names; the rest are
	// OtherFamily. A family's name is built from key names, which the
	// store's other users choose, so the count is held here and not by them.
	MaxFamilies = 24
	// OtherFamily is the families past MaxFamilies, by bytes, together.
	OtherFamily = "_other"
	// Interval is how often a census runs.
	Interval = 10 * time.Minute
	// batch bounds one pipeline of a census.
	batch = 128
	// memorySamples is the MEMORY USAGE SAMPLES argument: the server's own
	// default, named.
	memorySamples = 5
)

// ErrUnsupported is a client a census does not measure: a cluster, whose
// keys and counts are per node.
var ErrUnsupported = errors.New("storecensus: a cluster client is not measured")

// Family is one family of keys as a census estimated it.
type Family struct {
	Name string
	// Samples is how many of the keys drawn were of this family, gone ones
	// included: the estimate below rests on these alone.
	Samples int
	Keys    float64
	Bytes   float64
}

// Result is one census of one store.
type Result struct {
	// Store is the name the process knows the store by: the client label
	// its operations are counted under.
	Store string
	At    time.Time
	// Keys is the store's key count (DBSIZE), Weighed the keys weighed and
	// Exact whether they were every key.
	Keys    int64
	Weighed int
	Exact   bool
	// Gone is keys drawn that no longer existed when weighed.
	Gone     int
	Families []Family
	Duration time.Duration
}

// Measure takes one census of the store client reaches, its keys named by
// vocabulary (nil: the built-in words alone).
func Measure(ctx context.Context, client redis.UniversalClient, store string, vocabulary *Vocabulary, now func() time.Time) (Result, error) {
	if vocabulary == nil {
		vocabulary = defaultVocabulary
	}
	if _, cluster := client.(*redis.ClusterClient); cluster {
		return Result{}, ErrUnsupported
	}
	started := now()
	result := Result{Store: store, At: started}
	keys, err := client.DBSize(ctx).Result()
	if err != nil {
		return Result{}, err
	}
	result.Keys = keys
	drawn, exact, err := gather(ctx, client, keys)
	if err != nil {
		return Result{}, err
	}
	result.Exact = exact
	sizes, err := weigh(ctx, client, drawn)
	if err != nil {
		return Result{}, err
	}
	families, weighed, gone := estimate(vocabulary, drawn, sizes, keys, result.Exact)
	result.Weighed, result.Gone = weighed, gone
	result.Families = fold(families)
	result.Duration = now().Sub(started)
	return result, nil
}

// estimate is the families of the keys drawn, scaled to a store of keys
// keys unless they were every key: each family's share of every draw, gone
// ones included, times the store's count.
func estimate(vocabulary *Vocabulary, drawn []string, sizes []int64, keys int64, exact bool) (families []Family, weighed, gone int) {
	families, weighed, gone = tally(vocabulary, drawn, sizes)
	if exact || len(drawn) == 0 {
		return families, weighed, gone
	}
	scale := float64(keys) / float64(len(drawn))
	for index := range families {
		families[index].Keys *= scale
		families[index].Bytes *= scale
	}
	return families, weighed, gone
}

// tally is the keys drawn by family, unscaled, and how many were weighed and
// how many had gone before they could be: a key gone counts in its family's
// keys and adds nothing to its bytes.
func tally(vocabulary *Vocabulary, drawn []string, sizes []int64) (families []Family, weighed, gone int) {
	byName := map[string]int{}
	for index, key := range drawn {
		name := vocabulary.FamilyOf(key)
		position, found := byName[name]
		if !found {
			position = len(families)
			byName[name] = position
			families = append(families, Family{Name: name})
		}
		families[position].Samples++
		families[position].Keys++
		if sizes[index] < 0 {
			gone++
			continue
		}
		weighed++
		families[position].Bytes += float64(sizes[index])
	}
	return families, weighed, gone
}

// gather is the keys a census of a store of keys keys weighs, and whether
// they are every key: a walk of a small store, or a draw from a larger one
// or from a small one that outgrew its walk.
func gather(ctx context.Context, client redis.UniversalClient, keys int64) ([]string, bool, error) {
	if keys <= SampleKeys {
		walked, whole, err := walk(ctx, client)
		if err != nil || whole {
			return walked, whole, err
		}
	}
	drawn, err := draw(ctx, client)
	return drawn, false, err
}

// walk is every key of a small store, each once, and whether it was every
// key: a store that grew past twice the count it was walked for is walked
// no further, and is sampled instead.
func walk(ctx context.Context, client redis.UniversalClient) ([]string, bool, error) {
	var keys []string
	// SCAN may return a key twice while the server rehashes its table.
	seen := map[string]struct{}{}
	var cursor uint64
	for {
		page, next, err := client.Scan(ctx, cursor, "", batch).Result()
		if err != nil {
			return nil, false, err
		}
		keys = appendNew(keys, seen, page)
		if len(keys) > 2*SampleKeys {
			return nil, false, nil
		}
		if cursor = next; cursor == 0 {
			return keys, true, nil
		}
	}
}

// appendNew appends the keys of page not seen before, and marks them seen.
func appendNew(keys []string, seen map[string]struct{}, page []string) []string {
	for _, key := range page {
		if _, repeated := seen[key]; !repeated {
			seen[key] = struct{}{}
			keys = append(keys, key)
		}
	}
	return keys
}

// draw is SampleKeys keys drawn by the server, with replacement.
func draw(ctx context.Context, client redis.UniversalClient) ([]string, error) {
	keys := make([]string, 0, SampleKeys)
	for len(keys) < SampleKeys {
		commands := make([]*redis.StringCmd, 0, batch)
		if _, err := client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
			for range min(batch, SampleKeys-len(keys)) {
				commands = append(commands, pipe.RandomKey(ctx))
			}
			return nil
		}); err != nil && !errors.Is(err, redis.Nil) {
			return nil, err
		}
		for _, command := range commands {
			// An emptied store answers nil: nothing to draw.
			key, err := command.Result()
			if errors.Is(err, redis.Nil) {
				return keys, nil
			}
			if err != nil {
				return nil, err
			}
			keys = append(keys, key)
		}
	}
	return keys, nil
}

// weigh is each key's MEMORY USAGE, -1 for a key gone before it was weighed.
func weigh(ctx context.Context, client redis.UniversalClient, keys []string) ([]int64, error) {
	sizes := make([]int64, 0, len(keys))
	for start := 0; start < len(keys); start += batch {
		part := keys[start:min(start+batch, len(keys))]
		commands := make([]*redis.IntCmd, 0, len(part))
		if _, err := client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
			for _, key := range part {
				commands = append(commands, pipe.MemoryUsage(ctx, key, memorySamples))
			}
			return nil
		}); err != nil && !errors.Is(err, redis.Nil) {
			return nil, err
		}
		for _, command := range commands {
			size, err := command.Result()
			switch {
			case errors.Is(err, redis.Nil):
				sizes = append(sizes, -1)
			case err != nil:
				return nil, err
			default:
				sizes = append(sizes, size)
			}
		}
	}
	return sizes, nil
}

// fold names the MaxFamilies-1 largest families by bytes and folds the rest
// into OtherFamily, largest first.
func fold(families []Family) []Family {
	sort.Slice(families, func(i, j int) bool {
		if families[i].Bytes != families[j].Bytes {
			return families[i].Bytes > families[j].Bytes
		}
		return families[i].Name < families[j].Name
	})
	if len(families) <= MaxFamilies {
		return families
	}
	other := Family{Name: OtherFamily}
	for _, family := range families[MaxFamilies-1:] {
		other.Samples += family.Samples
		other.Keys += family.Keys
		other.Bytes += family.Bytes
	}
	return append(families[:MaxFamilies-1:MaxFamilies-1], other)
}
