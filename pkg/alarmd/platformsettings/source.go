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
type Publication struct {
	Published bool
	Revision  string
	Values    map[Field]json.RawMessage
}

// Source reads the distribution. A non-nil error is a read that did not
// happen; a read that happened and found nothing published is a
// Publication with Published false.
type Source interface {
	Read(ctx context.Context, fields []Field) (Publication, error)
}

// RedisSource reads the protocol's keys in one transaction, as the protocol
// prescribes: the revision and every field in one MULTI, so that a
// publication landing between the two reads cannot be half-seen.
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
	var revision *redis.StringCmd
	var values *redis.SliceCmd
	_, err := source.client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		revision = pipe.Get(ctx, RevisionKey(source.prefix))
		if len(keys) > 0 {
			values = pipe.MGet(ctx, keys...)
		}
		return nil
	})
	// A missing revision surfaces as the GET's redis.Nil, which TxPipelined
	// reports as the pipeline's error too; it is the one error that is a
	// reading rather than a failure.
	if err != nil && !errors.Is(err, redis.Nil) {
		return Publication{}, fmt.Errorf("alarmd platformsettings: read distribution: %w", err)
	}
	publication := Publication{Values: make(map[Field]json.RawMessage, len(fields))}
	text, revisionErr := revision.Result()
	switch {
	case errors.Is(revisionErr, redis.Nil):
		return publication, nil
	case revisionErr != nil:
		return Publication{}, fmt.Errorf("alarmd platformsettings: read revision: %w", revisionErr)
	case text == "":
		return Publication{}, errors.New("alarmd platformsettings: the published revision is empty")
	}
	publication.Published, publication.Revision = true, text
	if values == nil {
		return publication, nil
	}
	raw, valuesErr := values.Result()
	if valuesErr != nil {
		return Publication{}, fmt.Errorf("alarmd platformsettings: read fields: %w", valuesErr)
	}
	if len(raw) != len(fields) {
		return Publication{}, errors.New("alarmd platformsettings: the distribution answered a different number of fields than asked")
	}
	for index, value := range raw {
		if value == nil {
			continue
		}
		text, ok := value.(string)
		if !ok {
			return Publication{}, fmt.Errorf("alarmd platformsettings: field %s is not a string value", fields[index])
		}
		publication.Values[fields[index]] = json.RawMessage(text)
	}
	return publication, nil
}
