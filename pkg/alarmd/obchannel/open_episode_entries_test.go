// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package obchannel

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/obevidence"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openepisodes"
)

// store.inspect hands open_episodes_entries its version, cursor and limit as
// the CLI sends them: a page of one entry at revision 2, with the cursor of
// the next, out of a record that holds three at revision 2 and one at 1.
func TestStoreInspectPagesARecordsEntriesAsAsked(t *testing.T) {
	server := redistest.Start(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr, DB: 5, MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	const prefix = "alarmd-test"
	store, err := openepisodes.NewStore(client, prefix)
	if err != nil {
		t.Fatal(err)
	}
	key := openepisodes.Key{TenantID: "system", StrategyID: "803"}
	var changes []openepisodes.Change
	for i, version := range []string{"2", "1", "2", "2"} {
		changes = append(changes, openepisodes.Change{Key: key, Fingerprint: fmt.Sprintf("%032x", i), Open: true,
			Entry: openepisodes.Entry{Version: version, OpenedAt: time.Unix(1_800_000_000, 0).Unix()}})
	}
	if err := store.Apply(context.Background(), changes, nil); err != nil {
		t.Fatal(err)
	}
	service := obevidence.New(obevidence.Options{OpenEpisodes: obevidence.RedisBinding{Client: client,
		Location: obevidence.Location{Role: "runtime", Address: "fixture", Mode: "standalone", DB: 5, Prefix: prefix}}})
	var inspect Operation
	for _, op := range StoreOperations(service) {
		if op.ID == "store.inspect" {
			inspect = op
		}
	}
	params := Params{"family": "open_episodes_entries", "strategy_id": "803", "tenant": "system", "version": "2", "limit": json.Number("1")}
	if err := validate(inspect, params); err != nil {
		t.Fatal(err)
	}
	out := inspect.Run(context.Background(), params)
	result, _ := out.Value.(obevidence.Result)
	page, _ := result.Value.(obevidence.OpenEpisodeEntries)
	if page.Matching != 3 || len(page.Entries) != 1 || page.Entries[0].Version != "2" || page.NextCursor != fmt.Sprintf("%032x", 0) {
		t.Fatalf("outcome %+v, want one revision-2 entry of three and the next page's cursor", out.Value)
	}
}
