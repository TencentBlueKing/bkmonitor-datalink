package obevidence

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"
)

// InfoFields is every INFO field this read returns, closed: the memory
// ceiling and what the server does at it, what it holds, and what it has
// dropped. Nothing else of INFO is carried, so no client list, address or
// command line leaves the process.
var InfoFields = []string{
	"redis_version", "role", "maxmemory", "maxmemory_policy", "used_memory", "used_memory_peak",
	"used_memory_rss", "evicted_keys", "expired_keys", "keyspace_hits", "keyspace_misses",
	"master_repl_offset", "connected_slaves",
}

// ReplicaInfoFields are reported only by a server that is a replica: how far
// it has applied its master's stream and whether its link is up. On a
// replica each one missing is named; on a master none is expected.
var ReplicaInfoFields = []string{
	"master_link_status", "master_last_io_seconds_ago", "master_sync_in_progress", "slave_repl_offset",
}

// InfoScriptCommands are the commands whose INFO commandstats line is kept:
// the script calls every alarmd write and every fenced read goes through.
// A server lists a command only once it has been called since the stats
// were last reset, so an absent one is no calls, not an unknown.
var InfoScriptCommands = []string{"eval", "evalsha", "eval_ro", "evalsha_ro"}

// ReplicaLink is one replica as its master reports it, without its address:
// its state, the offset it has acknowledged, how many bytes that is behind
// the master's own offset, and the seconds since its last acknowledgement.
// A value the line or the master did not report is absent, not zero, and
// BytesBehind needs both offsets; it may be negative when the replica's
// acknowledgement is newer than the master's INFO.
type ReplicaLink struct {
	State       string `json:"state"`
	Offset      *int64 `json:"offset,omitempty"`
	BytesBehind *int64 `json:"bytes_behind,omitempty"`
	LagSeconds  *int64 `json:"lag_seconds,omitempty"`
}

// ServerInfo is one Redis server as alarmd reaches it: which of alarmd's
// roles read it, where, and the INFO fields above. Status is ok, or
// dependency_unavailable with no fields: a server that did not answer is
// never read as one with nothing in it.
type ServerInfo struct {
	Roles    []string          `json:"roles"`
	Address  string            `json:"address"`
	Mode     string            `json:"mode,omitempty"`
	DBs      []int             `json:"dbs"`
	Status   string            `json:"status"`
	ReadAt   *time.Time        `json:"read_at,omitempty"`
	Fields   map[string]string `json:"fields,omitempty"`
	Missing  []string          `json:"missing,omitempty"`
	Keyspace map[string]string `json:"keyspace,omitempty"`
	// Commands is the commandstats line of each of InfoScriptCommands the
	// server has called since its stats were reset: calls, usec,
	// usec_per_call and, on newer servers, rejected and failed calls.
	Commands map[string]string `json:"commands,omitempty"`
	// Replicas is what a master reports of each of its replicas.
	Replicas []ReplicaLink `json:"replicas,omitempty"`
}

// InfoResult is every Redis server alarmd is configured with, one entry per
// server however many roles share it.
type InfoResult struct {
	Servers  []ServerInfo `json:"servers"`
	Complete bool         `json:"complete"`
}

func (service *Service) bindings() []RedisBinding {
	if service == nil {
		return nil
	}
	o := service.options
	return []RedisBinding{o.Published, o.SourceStrategy, o.CMDBCache, o.TargetGroup, o.DynamicConfig}
}

// Info reads INFO from each server alarmd's roles are bound to.
func (service *Service) Info(ctx context.Context) InfoResult {
	type server struct {
		binding RedisBinding
		info    ServerInfo
	}
	byKey := map[string]*server{}
	var order []string
	for _, binding := range service.bindings() {
		if binding.Client == nil {
			continue
		}
		key := binding.Location.Mode + "|" + binding.Location.Address
		s, ok := byKey[key]
		if !ok {
			s = &server{binding: binding, info: ServerInfo{Address: binding.Location.Address, Mode: binding.Location.Mode}}
			byKey[key] = s
			order = append(order, key)
		}
		s.info.Roles = append(s.info.Roles, binding.Location.Role)
		s.info.DBs = appendUnique(s.info.DBs, binding.Location.DB)
	}
	result := InfoResult{Servers: []ServerInfo{}, Complete: true}
	for _, key := range order {
		s := byKey[key]
		read, cancel := context.WithTimeout(ctx, ReadTimeout)
		// One section per INFO call in this client, and "all" on every server
		// version; the fields are filtered here, so the rest never leaves.
		raw, err := s.binding.Client.Info(read, "all").Result()
		cancel()
		at := time.Now().UTC()
		s.info.ReadAt = &at
		if err != nil {
			s.info.Status = "dependency_unavailable"
			result.Complete = false
		} else {
			s.info.Status = "ok"
			parsed := parseInfo(raw, s.info.DBs)
			s.info.Fields, s.info.Keyspace, s.info.Missing = parsed.fields, parsed.keyspace, parsed.missing
			s.info.Commands, s.info.Replicas = parsed.commands, parsed.replicas
		}
		sort.Strings(s.info.Roles)
		result.Servers = append(result.Servers, s.info)
	}
	return result
}

func appendUnique(values []int, value int) []int {
	for _, v := range values {
		if v == value {
			return values
		}
	}
	values = append(values, value)
	sort.Ints(values)
	return values
}

// parsedInfo is what parseInfo keeps of one INFO answer.
type parsedInfo struct {
	fields, keyspace, commands map[string]string
	missing                    []string
	replicas                   []ReplicaLink
}

// parseInfo keeps the allowed fields, the keyspace line of each db a role
// reads, the commandstats of the script commands, and each replica line
// without its address. A field the server should have reported and did not
// is named as missing, not written as zero.
func parseInfo(raw string, dbs []int) parsedInfo {
	allowed := make(map[string]bool, len(InfoFields)+len(ReplicaInfoFields))
	for _, name := range InfoFields {
		allowed[name] = true
	}
	for _, name := range ReplicaInfoFields {
		allowed[name] = true
	}
	wantDB := map[string]bool{}
	for _, db := range dbs {
		wantDB["db"+strconv.Itoa(db)] = true
	}
	wantCommand := map[string]bool{}
	for _, command := range InfoScriptCommands {
		wantCommand["cmdstat_"+command] = true
	}
	parsed := parsedInfo{fields: map[string]string{}, keyspace: map[string]string{}, commands: map[string]string{}}
	var links []map[string]string
	for _, line := range strings.Split(raw, "\n") {
		name, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || strings.HasPrefix(name, "#") {
			continue
		}
		switch {
		case allowed[name]:
			parsed.fields[name] = value
		case wantDB[name]:
			parsed.keyspace[name] = value
		case wantCommand[name]:
			parsed.commands[strings.TrimPrefix(name, "cmdstat_")] = value
		case replicaLine(name):
			links = append(links, pairs(value))
		}
	}
	expected := InfoFields
	if parsed.fields["role"] == "slave" {
		expected = append(append([]string(nil), InfoFields...), ReplicaInfoFields...)
	}
	for _, name := range expected {
		if _, ok := parsed.fields[name]; !ok {
			parsed.missing = append(parsed.missing, name)
		}
	}
	masterOffset := reported(parsed.fields["master_repl_offset"])
	for _, link := range links {
		// Only these keys are read from the line; ip and port never are.
		replica := ReplicaLink{State: link["state"], Offset: reported(link["offset"]), LagSeconds: reported(link["lag"])}
		if masterOffset != nil && replica.Offset != nil {
			behind := *masterOffset - *replica.Offset
			replica.BytesBehind = &behind
		}
		parsed.replicas = append(parsed.replicas, replica)
	}
	if len(parsed.commands) == 0 {
		parsed.commands = nil
	}
	return parsed
}

// reported is an INFO integer, or nil when it is absent or not a number.
func reported(value string) *int64 {
	number, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return nil
	}
	return &number
}

// replicaLine is whether an INFO name is a master's slaveN line.
func replicaLine(name string) bool {
	number, ok := strings.CutPrefix(name, "slave")
	if !ok {
		return false
	}
	_, err := strconv.Atoi(number)
	return err == nil
}

// pairs splits a k=v,k=v INFO value.
func pairs(value string) map[string]string {
	out := map[string]string{}
	for _, pair := range strings.Split(value, ",") {
		if key, v, ok := strings.Cut(pair, "="); ok {
			out[key] = v
		}
	}
	return out
}
