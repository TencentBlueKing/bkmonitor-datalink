package obevidence

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/go-redis/redis/v8"
)

// One entry per server however many roles share it, with the memory ceiling,
// its policy, the usage and the evictions as the server reports them; only
// the allowed fields leave; a server that does not answer is named, never
// read as empty.
func TestInfoReadsEachServerOnceAndOnlyTheAllowedFields(t *testing.T) {
	client := redisForTest(t)
	ctx := context.Background()
	if err := client.ConfigSet(ctx, "maxmemory", "104857600").Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.ConfigSet(ctx, "maxmemory-policy", "allkeys-lru").Err(); err != nil {
		t.Fatal(err)
	}
	client.Set(ctx, "k", "v", 0)
	if err := client.ConfigResetStat(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Eval(ctx, "return 1", nil).Err(); err != nil {
		t.Fatal(err)
	}
	// A database no role reads: its keyspace line stays behind.
	other := redis.NewClient(&redis.Options{Addr: client.Options().Addr, DB: 2, MaxRetries: -1})
	defer other.Close()
	other.Set(ctx, "unrelated", "v", 0)
	dead := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
	defer dead.Close()
	runtime := binding(client, "runtime", "p")
	source := binding(client, "strategy_cache", "q")
	source.Location.DB = 8
	gone := RedisBinding{Client: dead, Location: Location{Role: "cmdb_cache", Address: "elsewhere", Mode: "standalone", DB: 0}}
	// The same server under a third role, and a role on no server: one entry
	// each server, roles merged.
	third := binding(client, "target_group", "r")
	got := New(Options{Published: runtime, SourceStrategy: source, CMDBCache: gone, TargetGroup: third}).Info(ctx)
	if len(got.Servers) != 2 || got.Complete {
		t.Fatalf("servers %+v complete %v", got.Servers, got.Complete)
	}
	shared := got.Servers[0]
	if strings.Join(shared.Roles, ",") != "runtime,strategy_cache,target_group" || len(shared.DBs) != 2 || shared.DBs[0] != 5 || shared.DBs[1] != 8 || shared.Status != "ok" {
		t.Fatalf("the shared server %+v", shared)
	}
	if shared.Fields["maxmemory"] != "104857600" || shared.Fields["maxmemory_policy"] != "allkeys-lru" ||
		shared.Fields["evicted_keys"] != "0" || shared.Fields["used_memory"] == "" || shared.Fields["role"] != "master" {
		t.Fatalf("fields %v", shared.Fields)
	}
	for name := range shared.Fields {
		allowed := false
		for _, field := range InfoFields {
			allowed = allowed || field == name
		}
		if !allowed {
			t.Errorf("a field outside the list left: %s", name)
		}
	}
	// The script calls since the stats were reset, and the master's own
	// replication offset; a command never called is absent, not zero.
	if !strings.HasPrefix(shared.Commands["eval"], "calls=1,") || shared.Commands["evalsha"] != "" || len(shared.Commands) != 1 {
		t.Errorf("script commands %v", shared.Commands)
	}
	if shared.Fields["master_repl_offset"] == "" || shared.Fields["connected_slaves"] != "0" || len(shared.Replicas) != 0 || len(shared.Missing) != 0 {
		t.Errorf("replication of a lone master: fields %v replicas %v missing %v", shared.Fields, shared.Replicas, shared.Missing)
	}
	if !strings.HasPrefix(shared.Keyspace["db5"], "keys=1") || len(shared.Keyspace) != 1 {
		t.Errorf("keyspace of the roles' dbs only: %v", shared.Keyspace)
	}
	if down := got.Servers[1]; down.Status != "dependency_unavailable" || down.Fields != nil || down.ReadAt == nil || down.Roles[0] != "cmdb_cache" {
		t.Errorf("an unanswering server %+v", down)
	}
}

func TestParseInfoNamesMissingFields(t *testing.T) {
	parsed := parseInfo("# Memory\r\nused_memory:10\r\nclient_list:secret\r\ndb3:keys=2\r\ncmdstat_get:calls=9\r\n", []int{3})
	if parsed.fields["used_memory"] != "10" || parsed.fields["client_list"] != "" || parsed.keyspace["db3"] != "keys=2" ||
		len(parsed.missing) != len(InfoFields)-1 || parsed.commands != nil {
		t.Fatalf("%+v", parsed)
	}
}

// A master names each replica by its state and how far behind it is, never
// by its address; a replica names its own link and offset, and each of them
// it did not report.
func TestParseInfoReadsReplicationWithoutAddresses(t *testing.T) {
	master := parseInfo("# Replication\r\nrole:master\r\nconnected_slaves:2\r\n"+
		"slave0:ip=10.0.0.7,port=6379,state=online,offset=1000,lag=0\r\n"+
		"slave1:ip=10.0.0.8,port=6379,state=wait_bgsave,offset=400,lag=3\r\n"+
		"master_replid:abc\r\nmaster_repl_offset:1200\r\n"+
		"# Commandstats\r\ncmdstat_evalsha:calls=5,usec=50,usec_per_call=10.00\r\n", nil)
	value := func(n *int64) string {
		if n == nil {
			return "absent"
		}
		return strconv.FormatInt(*n, 10)
	}
	read := func(link ReplicaLink) string {
		return link.State + " " + value(link.Offset) + " " + value(link.BytesBehind) + " " + value(link.LagSeconds)
	}
	if len(master.replicas) != 2 || read(master.replicas[0]) != "online 1000 200 0" || read(master.replicas[1]) != "wait_bgsave 400 800 3" {
		t.Fatalf("replicas %+v", master.replicas)
	}
	// An offset either side did not report leaves bytes behind absent, not
	// the whole other offset.
	noOffset := parseInfo("role:master\r\nmaster_repl_offset:1200\r\nslave0:ip=10.0.0.7,port=6379,state=online,lag=1\r\n", nil)
	noMaster := parseInfo("role:master\r\nslave0:ip=10.0.0.7,port=6379,state=online,offset=1000,lag=1\r\n", nil)
	if read(noOffset.replicas[0]) != "online absent absent 1" || read(noMaster.replicas[0]) != "online 1000 absent 1" {
		t.Fatalf("missing offsets read as %q and %q", read(noOffset.replicas[0]), read(noMaster.replicas[0]))
	}
	if master.commands["evalsha"] != "calls=5,usec=50,usec_per_call=10.00" || master.fields["master_replid"] != "" {
		t.Fatalf("commands %v fields %v", master.commands, master.fields)
	}
	for _, missing := range master.missing {
		for _, replicaOnly := range ReplicaInfoFields {
			if missing == replicaOnly {
				t.Fatalf("a master was held to a replica's fields: %v", master.missing)
			}
		}
	}
	replica := parseInfo("role:slave\r\nmaster_link_status:down\r\nslave_repl_offset:900\r\nmaster_repl_offset:900\r\n", nil)
	if replica.fields["master_link_status"] != "down" || replica.fields["slave_repl_offset"] != "900" ||
		!strings.Contains(strings.Join(replica.missing, ","), "master_last_io_seconds_ago") {
		t.Fatalf("replica %+v", replica)
	}
}
