// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/go-redis/redis/v8"
)

func main() {
	address := flag.String("address", "", "Redis address, host:port")
	username := flag.String("username", "", "Redis username")
	password := flag.String("password", "", "Redis password")
	database := flag.Int("db", 0, "Redis database")
	prefix := flag.String("prefix", "", "the state key prefix this deployment writes under")
	copyTo := flag.String("copy-to", "", "file the saved records are appended to; required with -delete")
	remove := flag.Bool("delete", false, "delete the records that qualify; without it nothing is written")
	timeout := flag.Duration("timeout", 30*time.Minute, "how long the whole walk may take")
	flag.Parse()

	if err := run(*address, *username, *password, *database, *prefix, *copyTo, *remove, *timeout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(
	address, username, password string, database int,
	prefix, copyTo string, remove bool, timeout time.Duration,
) error {
	if address == "" || prefix == "" {
		return errors.New("nodatacleanup: -address and -prefix are required")
	}
	if remove && copyTo == "" {
		return errors.New("nodatacleanup: -delete requires -copy-to, so the delete can be undone")
	}
	client := redis.NewClient(&redis.Options{
		Addr: address, Username: username, Password: password, DB: database,
	})
	defer func() { _ = client.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	options := Options{Prefix: prefix, Delete: remove}
	if copyTo != "" {
		file, err := os.OpenFile(copyTo, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return fmt.Errorf("nodatacleanup: open %s: %w", copyTo, err)
		}
		defer func() { _ = file.Close() }()
		copier := &fileCopier{file: file, writer: bufio.NewWriter(file)}
		defer func() { _ = copier.writer.Flush() }()
		options.Copier = copier
	}

	counts, err := Run(ctx, &redisStore{client: client}, options)
	// The counts are printed whether or not the walk finished. A run that
	// stopped halfway has still deleted what it deleted, and the operator needs
	// to know how much before deciding what to do next.
	fmt.Println(counts.String())
	if !remove {
		fmt.Println("nothing was deleted: pass -delete to remove the eligible records")
		return err
	}
	// Where the copies are and how many, on the same screen as the delete
	// count. A copy nobody can find is not a copy, and the moment anyone looks
	// for one is after the run, when this output is all they have.
	fmt.Printf("%d saved records are in %s\n", counts.Deleted, copyTo)
	fmt.Printf("to put them back: %s\n", restoreCommand(copyTo))
	return err
}

// restoreCommand is the one line that undoes the run, printed rather than
// documented somewhere else: the copies exist for a moment when nobody is
// reading documentation.
func restoreCommand(copyTo string) string {
	return "while IFS=$'\\t' read -r key payload; do " +
		"redis-cli -h <host> -p <port> -n <db> --no-raw RESTORE \"$key\" 0 " +
		"\"$(printf %s \"$payload\" | base64 -d)\"; done < " + copyTo
}

// fileCopier appends one saved record per line as "key<TAB>base64", which
// RESTORE takes back. Each line is flushed and the file synced before the copy
// is reported as saved: a buffered copy that is still in memory when the delete
// goes through is not a copy.
type fileCopier struct {
	file   *os.File
	writer *bufio.Writer
}

func (copier *fileCopier) Save(key string, dump []byte) error {
	if _, err := fmt.Fprintf(copier.writer, "%s\t%s\n", key, base64.StdEncoding.EncodeToString(dump)); err != nil {
		return err
	}
	if err := copier.writer.Flush(); err != nil {
		return err
	}
	return copier.file.Sync()
}

// redisStore is the adapter from the narrow Store to the client. It holds no
// logic on purpose: everything the cleanup decides is in cleanup.go, where a
// fake can drive it.
type redisStore struct {
	client redis.Cmdable
}

func (store *redisStore) Scan(
	ctx context.Context, cursor uint64, match string, count int64,
) ([]string, uint64, error) {
	return store.client.Scan(ctx, cursor, match, count).Result()
}

func (store *redisStore) Get(ctx context.Context, key string) ([]byte, error) {
	value, err := store.client.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	return value, err
}

func (store *redisStore) HGet(ctx context.Context, key, field string) ([]byte, error) {
	value, err := store.client.HGet(ctx, key, field).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	return value, err
}

func (store *redisStore) Dump(ctx context.Context, key string) ([]byte, error) {
	value, err := store.client.Dump(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return []byte(value), nil
}

func (store *redisStore) Del(ctx context.Context, key string) error {
	return store.client.Del(ctx, key).Err()
}
