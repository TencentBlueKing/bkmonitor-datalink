package fleet

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/go-redis/redis/v8"
)

// LoadRetainedDiagnostics reads existing records independently of whether this
// process has a sampler. It starts no collector and never changes a window or
// retention. The caller supplies a bounded diagnostic Redis reader.
func LoadRetainedDiagnostics(ctx context.Context, client redis.Cmdable, prefix, queryGroup string, recordsLimit, samplesLimit int) ([]json.RawMessage, []json.RawMessage, error) {
	if client == nil || prefix == "" || ValidateQueryGroup(queryGroup) != nil || recordsLimit < 1 || recordsLimit > DiagnosticRecordsPerObject || samplesLimit < 1 || samplesLimit > DiagnosticRecordsPerObject {
		return nil, nil, errors.New("alarmd fleet: bounded retained diagnostic read required")
	}
	store := &DiagnosticStore{client: client, prefix: prefix}
	load := func(key string, limit int) ([]json.RawMessage, error) {
		rows, err := client.LRange(ctx, key, 0, int64(limit-1)).Result()
		if err != nil {
			return nil, err
		}
		result := make([]json.RawMessage, 0, len(rows))
		for _, row := range rows {
			result = append(result, json.RawMessage(row))
		}
		return result, nil
	}
	records, err := load(store.key(queryGroup), recordsLimit)
	if err != nil {
		return nil, nil, err
	}
	samples, err := load(store.sampleKey(queryGroup), samplesLimit)
	return records, samples, err
}
