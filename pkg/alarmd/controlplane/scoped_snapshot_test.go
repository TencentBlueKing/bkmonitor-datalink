package controlplane

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
)

type scopeRedis struct {
	redis.Cmdable
	values               map[string]string
	gets, mgets, strlens int
	getError             error
}

func (c *scopeRedis) Get(ctx context.Context, k string) *redis.StringCmd {
	c.gets++
	if c.getError != nil {
		return redis.NewStringResult("", c.getError)
	}
	v, ok := c.values[k]
	if !ok {
		return redis.NewStringResult("", redis.Nil)
	}
	return redis.NewStringResult(v, ctx.Err())
}
func (c *scopeRedis) StrLen(ctx context.Context, k string) *redis.IntCmd {
	c.strlens++
	if c.getError != nil {
		return redis.NewIntResult(0, c.getError)
	}
	return redis.NewIntResult(int64(len(c.values[k])), ctx.Err())
}
func (c *scopeRedis) HGet(ctx context.Context, k, field string) *redis.StringCmd {
	return redis.NewStringResult("", redis.Nil)
}
func (c *scopeRedis) MGet(ctx context.Context, keys ...string) *redis.SliceCmd {
	c.mgets++
	v := make([]interface{}, len(keys))
	for i, k := range keys {
		if x, ok := c.values[k]; ok {
			v[i] = x
		}
	}
	return redis.NewSliceResult(v, ctx.Err())
}
func TestScopedSnapshotLifetimeAndAuthority(t *testing.T) {
	rev, payload := neutralSnapshotPayload(t, "qg-a")
	client := &scopeRedis{values: map[string]string{}}
	repo, e := NewRedisCatalogRepository(client, "test", time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	client.values[repo.snapshotKey(rev)] = payload
	client.values[repo.epochForRevisionKey(rev)] = "1"
	ctx, closeScope := WithSnapshotReadScope(context.Background())
	defer closeScope()
	for i := 0; i < 4; i++ {
		g, e := repo.loadPublishedQueryGroup(ctx, SnapshotPublicationRef{rev, 1}, "qg-a")
		if e != nil || g.Identity != "qg-a" {
			t.Fatal(g, e)
		}
		g.Identity = "mutated"
	}
	if client.mgets != 1 {
		t.Fatalf("payload reads %d", client.mgets)
	}
	client.values[repo.publicationKey(1)] = "changed-after-first-read"
	if _, err := repo.loadPublishedQueryGroup(ctx, SnapshotPublicationRef{rev, 1}, "qg-a"); err == nil {
		t.Fatal("cached content masked changed publication")
	}
	if snapshotScope(ctx).entry.payload != "" {
		t.Fatal("publication failure retained content")
	}
	delete(client.values, repo.publicationKey(1))
	if _, err := repo.loadPublishedQueryGroup(ctx, SnapshotPublicationRef{rev, 1}, "qg-a"); err != nil {
		t.Fatal(err)
	}
	delete(client.values, repo.snapshotKey(rev))
	if _, e := repo.LoadQueryGroup(ctx, rev, "qg-a"); e != nil {
		t.Fatal("same call lost immutable body", e)
	}
	next, done := WithSnapshotReadScope(ctx)
	defer done()
	if _, e := repo.LoadQueryGroup(next, rev, "qg-a"); !errors.Is(e, ErrSnapshotUnavailable) {
		t.Fatal("nested call reused old content", e)
	}
	client.values[repo.epochForRevisionKey(rev)] = "2"
	if _, e := repo.LoadQueryGroup(ctx, rev, "qg-a"); !errors.Is(e, ErrSnapshotUnavailable) {
		t.Fatal("epoch change hid missing body", e)
	}
	client.values[repo.snapshotKey(rev)] = payload
	client.values[repo.epochForRevisionKey(rev)] = "1"
	if _, e := repo.LoadQueryGroup(ctx, rev, "qg-a"); e != nil {
		t.Fatal(e)
	}
	closeScope()
	if snapshotScope(ctx).entry.payload != "" {
		t.Fatal("retained after return")
	}
	delete(client.values, repo.snapshotKey(rev))
	if _, e := repo.LoadQueryGroup(ctx, rev, "qg-a"); !errors.Is(e, ErrSnapshotUnavailable) {
		t.Fatal(e)
	}
}
func TestScopedSnapshotRejectDoesNotRetain(t *testing.T) {
	rev, payload := neutralSnapshotPayload(t, "qg-a")
	client := &scopeRedis{values: map[string]string{}}
	repo, _ := NewRedisCatalogRepository(client, "test", time.Hour)
	client.values[repo.snapshotKey(rev)] = payload
	client.values[repo.epochForRevisionKey(rev)] = "1"
	client.values[repo.publicationKey(1)] = "wrong"
	ctx, done := WithSnapshotReadScope(context.Background())
	defer done()
	if _, e := repo.loadPublishedQueryGroup(ctx, SnapshotPublicationRef{rev, 1}, "qg-a"); e == nil {
		t.Fatal("publication accepted")
	}
	if snapshotScope(ctx).entry.payload != "" {
		t.Fatal("half-success cached")
	}
	delete(client.values, repo.publicationKey(1))
	if _, e := repo.LoadQueryGroup(ctx, rev, "missing-qg"); e == nil {
		t.Fatal("missing group")
	}
	if snapshotScope(ctx).entry.payload != "" {
		t.Fatal("missing group cached")
	}
	if _, e := repo.LoadQueryGroup(ctx, rev, "qg-a"); e != nil {
		t.Fatal(e)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, e := repo.LoadQueryGroup(cancelled, rev, "qg-a"); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if snapshotScope(ctx).entry.payload != "" {
		t.Fatal("cancel retained")
	}
}

func TestScopedSnapshotEpochFailuresClearRetention(t *testing.T) {
	for _, value := range []string{"", "bad", "0", "io"} {
		t.Run(value, func(t *testing.T) {
			rev, payload := neutralSnapshotPayload(t, "qg-a")
			client := &scopeRedis{values: map[string]string{}}
			repo, _ := NewRedisCatalogRepository(client, "test", time.Hour)
			client.values[repo.snapshotKey(rev)] = payload
			client.values[repo.epochForRevisionKey(rev)] = "1"
			ctx, done := WithSnapshotReadScope(context.Background())
			defer done()
			if _, err := repo.LoadQueryGroup(ctx, rev, "qg-a"); err != nil {
				t.Fatal(err)
			}
			if value == "io" {
				client.getError = errors.New("read failed")
			} else if value == "" {
				delete(client.values, repo.epochForRevisionKey(rev))
			} else {
				client.values[repo.epochForRevisionKey(rev)] = value
			}
			if _, err := repo.LoadQueryGroup(ctx, rev, "qg-a"); err == nil {
				t.Fatal("epoch failure hidden")
			}
			if snapshotScope(ctx).entry.payload != "" {
				t.Fatal("failure retained payload")
			}
		})
	}
}

func TestScopedSnapshotFirstCorruptionAndRepositoryIsolation(t *testing.T) {
	rev, payload := neutralSnapshotPayload(t, "qg-a")
	client := &scopeRedis{values: map[string]string{}}
	repo, _ := NewRedisCatalogRepository(client, "test", time.Hour)
	client.values[repo.snapshotKey(rev)] = "{broken"
	client.values[repo.epochForRevisionKey(rev)] = "1"
	ctx, done := WithSnapshotReadScope(context.Background())
	defer done()
	if _, err := repo.LoadQueryGroup(ctx, rev, "qg-a"); err == nil {
		t.Fatal("corruption accepted")
	}
	if snapshotScope(ctx).entry.payload != "" {
		t.Fatal("corruption retained")
	}
	client.values[repo.snapshotKey(rev)] = payload
	if _, err := repo.LoadQueryGroup(ctx, rev, "qg-a"); err != nil {
		t.Fatal(err)
	}
	other, _ := NewRedisCatalogRepository(&scopeRedis{values: map[string]string{}}, "test", time.Hour)
	if _, err := other.LoadQueryGroup(ctx, rev, "qg-a"); !errors.Is(err, ErrSnapshotUnavailable) {
		t.Fatal("other repository reused body", err)
	}
	if snapshotScope(ctx).entry.payload != "" {
		t.Fatal("fallback retained former entry")
	}
}
