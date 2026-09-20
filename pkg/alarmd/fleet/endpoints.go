// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

// Endpoint is one external system a replica reads from or writes to, as the
// replica resolved it: where, under what key space, and what it has seen of
// it lately.
//
// The page had no way to answer "which Redis is this", and the reader's next
// question -- "is it the one the platform writes to" -- is what a deployment
// with an empty Active Set turns on. The address is the deployment's; the
// credentials are never here. Where the replica reads something the platform
// wrote, Writer says what it found, because the address alone cannot tell a
// Redis nobody writes to from the right one.
type Endpoint struct {
	// Role is the closed name of what the replica uses it for; EndpointRoles
	// lists them.
	Role string `json:"role"`
	// Kind is redis, kafka or http.
	Kind string `json:"kind"`
	// Address is host:port, several joined with commas, or for a sentinel
	// deployment the master name followed by the sentinel addresses. Never a
	// username or password.
	Address string `json:"address"`
	Mode    string `json:"mode,omitempty"`
	// DB is the logical database, for Redis only.
	DB *int `json:"db,omitempty"`
	// Prefix is the key space or topic the role reads or writes under.
	Prefix string `json:"prefix,omitempty"`
	// SharedWith names the role whose connection this one reuses, when the
	// deployment resolved two roles to the same instance and database. The
	// health facts below then belong to that connection.
	SharedWith string `json:"shared_with,omitempty"`
	// Configured is false when the deployment renders no coordinates for the
	// role at all, which is a different answer from an address that does not
	// answer.
	Configured bool `json:"configured"`
	// LastSuccessAgeSeconds and LastFailureAgeSeconds are how long since this
	// process last completed a command against the connection, and last
	// failed one; LastFailure is what the failure said, sanitised and bounded.
	// Absent until the process has done either.
	LastSuccessAgeSeconds *float64 `json:"last_success_age_seconds,omitempty"`
	LastFailureAgeSeconds *float64 `json:"last_failure_age_seconds,omitempty"`
	LastFailure           string   `json:"last_failure,omitempty"`
	// Writer is what the replica found of the platform's writing under this
	// role, for the roles that read a platform cache.
	Writer *WriterEvidence `json:"writer,omitempty"`
}

// WriterEvidence is what a replica found of the platform's writing under a
// dependency it only reads: how much is there and how old it is. It is the
// evidence that the address is the one the platform writes to, and that the
// writer is alive; neither follows from the connection answering.
type WriterEvidence struct {
	// Present says the replica has read the writer's content at least once.
	Present bool `json:"present"`
	// Count is what there is: strategies listed, hosts indexed.
	Count int `json:"count"`
	// AgeSeconds is how old the writer's content is by its own marker, when it
	// carries one: the strategy cache's change marker, the host index's
	// source refresh time.
	AgeSeconds *float64 `json:"age_seconds,omitempty"`
	// State is the reader's own reading of the copy, in the reader's closed
	// words, when it keeps one: never_loaded, index_stale, index_empty for the
	// host cache; not_configured, authoritative and the like for the settings
	// copy. Empty where the reader keeps no such state.
	State string `json:"state,omitempty"`
}

// The endpoint roles, closed. The page's wording table is held to this list.
const (
	EndpointStateRedis    = "state_redis"
	EndpointStrategyCache = "strategy_cache"
	EndpointCMDBCache     = "cmdb_cache"
	EndpointDynamicConfig = "dynamic_config"
	EndpointOutputKafka   = "output_kafka"
	EndpointQueryBackend  = "query_backend"
	EndpointCompatOutput  = "compat_output_redis"
)

// EndpointRoles is the closed list, in the order the page shows them: the
// replica's own storage first, then what it reads of the platform's, then
// where its work goes and where its queries go.
var EndpointRoles = []string{
	EndpointStateRedis, EndpointStrategyCache, EndpointCMDBCache, EndpointDynamicConfig,
	EndpointQueryBackend, EndpointOutputKafka, EndpointCompatOutput,
}
