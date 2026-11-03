// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package platformsettings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/go-redis/redis/v8"
)

// Publication is one read of the distribution: the global revision and, per
// field, the raw JSON the platform distributes for it. A field whose key is
// absent is absent from Values; that is the protocol's "no override", which
// is a different thing from a JSON null, which is present.
//
// Published is false when the revision key does not exist. The protocol
// says that means nothing was ever published; the values read beside it
// are then not the platform's word, and the cache does not use them.
//
// Retried says the first try was torn -- its revision moved during the read
// -- and the read was tried again. It is set whatever the second try came
// to, an error included, so a reader counts every torn first try.
type Publication struct {
	Published bool
	Revision  string
	Values    map[Field]json.RawMessage
	Retried   bool
}

// Source reads the distribution. A non-nil error is a read that did not
// happen; a read that happened and found nothing published is a
// Publication with Published false. Beside an error, only Retried is
// meaningful.
type Source interface {
	Read(ctx context.Context, fields []Field) (Publication, error)
}

// RedisSource reads the protocol's keys so that a publication landing
// during the read cannot be half-seen: the revision, every field, and the
// revision again, in one pipeline, and the fields are taken only when the
// two revisions agree. The platform writes the fields and the revision in
// one transaction, so fields read between two equal revisions are that
// revision's.
//
// This rests on the revision changing on every publication. A writer that
// published B and then A again between the two GETs would leave equal
// revisions around fields of B: the platform's writer never reuses a
// revision, and one that did would break this check.
//
// It is a pipeline rather than a MULTI or a script. A MULTI is what the
// protocol suggests, but go-redis returns from a refused MULTI without
// reading the rest of its replies and leaves them for the next command on
// the connection (see redisatomic). A script would be one command, but this
// store is the platform's: the account alarmd reads it with may not be
// allowed EVAL, and a proxy in front of a cluster refuses a script over the
// revision key and the tenant's keys, which are in different slots.
type RedisSource struct {
	client redis.Cmdable
	prefix string
	tenant string
}

func NewRedisSource(client redis.Cmdable, prefix string) (*RedisSource, error) {
	if client == nil {
		return nil, errors.New("alarmd platformsettings: a redis client is required")
	}
	if err := ValidateKeyPrefix(prefix); err != nil {
		return nil, err
	}
	return &RedisSource{client: client, prefix: prefix, tenant: Tenant}, nil
}

func (source *RedisSource) Read(ctx context.Context, fields []Field) (Publication, error) {
	if source == nil || source.client == nil {
		return Publication{}, errors.New("alarmd platformsettings: source is not initialised")
	}
	keys := make([]string, 0, len(fields))
	for _, field := range fields {
		if !ValidField(field) {
			return Publication{}, fmt.Errorf("alarmd platformsettings: unknown field %q", field)
		}
		keys = append(keys, ConfigKey(source.prefix, source.tenant, field.DBKey()))
	}
	// A revision that moved between the two reads is a publication landing
	// meanwhile; the next read almost always falls between two, so it is
	// tried once more. Two torn reads in a row is a publisher writing faster
	// than a round trip, and is a failed read rather than a third try.
	for attempt := 0; attempt < 2; attempt++ {
		publication, stable, err := source.read(ctx, fields, keys)
		publication.Retried = attempt > 0
		if err != nil || stable {
			return publication, err
		}
	}
	return Publication{Retried: true}, ErrRevisionMoved
}

// ErrRevisionMoved is a read of the distribution whose revision changed
// between its first and last command on both tries: the fields read may be
// of two publications, and are not used.
var ErrRevisionMoved = errors.New("alarmd platformsettings: the revision changed during the read, twice")

// read is one pipelined read: GET revision, MGET fields, GET revision. It
// says whether the two revisions agreed; when they did not, the publication
// is empty and the caller reads again.
func (source *RedisSource) read(ctx context.Context, fields []Field, keys []string) (Publication, bool, error) {
	var before, after *redis.StringCmd
	var values *redis.SliceCmd
	_, err := source.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		before = pipe.Get(ctx, RevisionKey(source.prefix))
		if len(keys) > 0 {
			values = pipe.MGet(ctx, keys...)
		}
		after = pipe.Get(ctx, RevisionKey(source.prefix))
		return nil
	})
	// A missing revision surfaces as the GET's redis.Nil, which Pipelined
	// reports as the pipeline's error too; it is the one error that is a
	// reading rather than a failure. Every other command's error is read
	// from the command itself below.
	if err != nil && !errors.Is(err, redis.Nil) {
		return Publication{}, false, fmt.Errorf("alarmd platformsettings: read distribution: %w", err)
	}
	text, revisionErr := before.Result()
	again, againErr := after.Result()
	for _, err := range []error{revisionErr, againErr} {
		if err != nil && !errors.Is(err, redis.Nil) {
			return Publication{}, false, fmt.Errorf("alarmd platformsettings: read revision: %w", err)
		}
	}
	if errors.Is(revisionErr, redis.Nil) != errors.Is(againErr, redis.Nil) || text != again {
		return Publication{}, false, nil
	}
	return decodePublication(fields, text, revisionErr, values)
}

// decodePublication is the publication one stable read found.
func decodePublication(fields []Field, text string, revisionErr error, values *redis.SliceCmd) (Publication, bool, error) {
	publication := Publication{Values: make(map[Field]json.RawMessage, len(fields))}
	switch {
	case errors.Is(revisionErr, redis.Nil):
		return publication, true, nil
	case revisionErr != nil:
		return Publication{}, false, fmt.Errorf("alarmd platformsettings: read revision: %w", revisionErr)
	case text == "":
		return Publication{}, false, errors.New("alarmd platformsettings: the published revision is empty")
	}
	publication.Published, publication.Revision = true, text
	if values == nil {
		return publication, true, nil
	}
	raw, valuesErr := values.Result()
	if valuesErr != nil {
		return Publication{}, false, fmt.Errorf("alarmd platformsettings: read fields: %w", valuesErr)
	}
	if len(raw) != len(fields) {
		return Publication{}, false, errors.New("alarmd platformsettings: the distribution answered a different number of fields than asked")
	}
	for index, value := range raw {
		if value == nil {
			continue
		}
		text, ok := value.(string)
		if !ok {
			return Publication{}, false, fmt.Errorf("alarmd platformsettings: field %s is not a string value", fields[index])
		}
		publication.Values[fields[index]] = json.RawMessage(text)
	}
	return publication, true, nil
}
