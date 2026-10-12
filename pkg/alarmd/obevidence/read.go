package obevidence

import (
	"context"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/redisfailure"
)

func readOne(ctx context.Context, source string, binding RedisBinding, key string) (Result, []byte) {
	results, raw := readMany(ctx, source, binding, []string{key})
	return results[0], raw[0]
}

// readMany is one bounded Redis transaction. It keeps TYPE/PTTL/GETRANGE
// coherent within this read, not with other operations or runtime adoption.
func readMany(ctx context.Context, source string, binding RedisBinding, keys []string) ([]Result, [][]byte) {
	results := make([]Result, len(keys))
	raw := make([][]byte, len(keys))
	for i, key := range keys {
		results[i] = result(source, binding, "dependency_unavailable")
		results[i].Location.Key = key
	}
	if len(keys) == 0 {
		return results, raw
	}
	if binding.Client == nil {
		for i := range results {
			results[i].Status = "not_configured"
		}
		return results, raw
	}
	commands := 2 + 3*len(keys) // MULTI/EXEC are included.
	if commands > MaxCommands {
		for i := range results {
			results[i].Status = "budget_exceeded"
		}
		return results, raw
	}
	ctx, cancel := context.WithTimeout(ctx, ReadTimeout)
	defer cancel()
	types := make([]*redis.StatusCmd, len(keys))
	ttls := make([]*redis.DurationCmd, len(keys))
	values := make([]*redis.StringCmd, len(keys))
	// Reserve the overflow sentinel for every document before issuing reads.
	limit := min(MaxDocumentBytes, MaxBytes/len(keys)-1)
	for i := range results {
		results[i].Limits.DocumentReadLimitBytes = limit
	}
	_, txErr := binding.Client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		for i, key := range keys {
			types[i] = pipe.Type(ctx, key)
			ttls[i] = pipe.PTTL(ctx, key)
			values[i] = pipe.GetRange(ctx, key, 0, int64(limit))
		}
		return nil
	})
	bytesRead := 0
	unanswered := ""
	for i := range results {
		r := &results[i]
		at := time.Now().UTC()
		r.ReadAt = &at
		// A result the server did not answer says why, never only that it
		// was unavailable: an idle connection the network cut and a command
		// the server refused are fixed in different places.
		if types[i] == nil || types[i].Err() != nil || ttls[i].Err() != nil {
			r.Reason, r.ReasonText = failureReason(txErr, types[i], ttls[i])
			unanswered = r.Reason
			continue
		}
		r.Type = types[i].Val()
		ttl := ttls[i].Val()
		ttlMS := ttl.Milliseconds()
		// go-redis preserves Redis's negative sentinels as durations -1/-2,
		// not as millisecond durations.
		if ttl < 0 {
			ttlMS = int64(ttl)
		}
		r.TTLMS = &ttlMS
		switch r.Type {
		case "none":
			r.Status = "missing"
			r.Complete = true
			continue
		case "string":
		default:
			r.Status = "wrong_type"
			continue
		}
		if values[i].Err() != nil {
			r.Reason, r.ReasonText = failureReason(txErr, values[i])
			unanswered = r.Reason
			continue
		}
		payload := []byte(values[i].Val())
		bytesRead += len(payload)
		if len(payload) > limit {
			r.Status = "budget_exceeded"
			r.Reason = "document_bytes"
			continue
		}
		if len(payload) == 0 {
			r.Status = "empty"
			r.Complete = true
			continue
		}
		r.Status = "ok"
		r.Complete = true
		raw[i] = payload
	}
	for i := range results {
		results[i].Limits.Commands = commands
		results[i].Limits.Bytes = bytesRead
	}
	binding.failed(unanswered)
	return results, raw
}

// failureReason is why a read went unanswered, and the error's own text:
// the first failed command's error, else the transaction's, else a reply
// that was never filled in.
func failureReason(txErr error, commands ...redis.Cmder) (string, string) {
	for _, command := range commands {
		if command != nil && command.Err() != nil {
			return redisfailure.Reason(command.Err()), redisfailure.Detail(command.Err())
		}
	}
	if txErr != nil {
		return redisfailure.Reason(txErr), redisfailure.Detail(txErr)
	}
	return redisfailure.MalformedReply, "a reply the transaction did not fill in"
}
