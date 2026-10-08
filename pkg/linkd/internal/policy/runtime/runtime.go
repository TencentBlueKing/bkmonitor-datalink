// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package runtime 装配策略读取、匹配与抑制执行；只读资源和 Redis 副作用通过独立端口调用。
package runtime

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"time"

	driver "github.com/go-sql-driver/mysql"
	"linkd/internal/blueking"
	"linkd/internal/cmdb"
	"linkd/internal/config"
	"linkd/internal/onemodel"
	onemodelassembly "linkd/internal/onemodel/assembly"
	"linkd/internal/onemodel/metadatastore"
	"linkd/internal/policy"
)

// Runtime 在一个控制面/来源任务内复用有界连接池，缺少资源按本次策略不可求值处理。
type Runtime struct {
	Targets   *onemodel.TargetResolver
	Relations policy.RelationLookup
	db        *sql.DB
	transport *onemodelassembly.Connections
	apigw     *blueking.Client
	closeOnce sync.Once
	closeErr  error
}

// Open 仅校验结构并创建连接句柄，不迁移 Kingeye schema，也不探测并依赖远端启动。
func Open(resources config.ResourcesConfig, bk config.BluekingConfig) (*Runtime, error) {
	if err := resources.Validate(); err != nil {
		return nil, err
	}
	r := &Runtime{}
	var directory onemodel.TargetDirectory
	var cmdbDirectory cmdb.ModelDirectory
	var pager onemodel.TargetPager
	var topology onemodel.TargetTopology
	if resources.MySQL != nil {
		cfg := resources.MySQL
		dsn := driver.NewConfig()
		dsn.User = cfg.Username
		dsn.Passwd = cfg.Password
		dsn.Net = "tcp"
		dsn.Addr = cfg.Address
		dsn.DBName = cfg.Database
		dsn.Timeout = 3 * time.Second
		dsn.ReadTimeout = 3 * time.Second
		dsn.WriteTimeout = 3 * time.Second
		db, err := sql.Open("mysql", dsn.FormatDSN())
		if err != nil {
			return nil, err
		}
		r.db = db
		db.SetMaxOpenConns(4)
		db.SetMaxIdleConns(2)
		db.SetConnMaxLifetime(30 * time.Minute)
		reader, err := metadatastore.New(db)
		if err != nil {
			_ = r.Close()
			return nil, err
		}
		directory = reader
		cmdbDirectory = reader
	}
	if resources.OneModel != nil {
		client, transport, err := onemodelassembly.Open(resources.OneModel, 4, 3*time.Second)
		if err != nil {
			_ = r.Close()
			return nil, err
		}
		r.transport = transport
		p, err := onemodel.NewPager(client)
		if err != nil {
			_ = r.Close()
			return nil, err
		}
		pager = p
		topology = client
		r.Relations = relationReader{reader: client, directory: directory}
	}
	var options []onemodel.TargetResolverOption
	if resources.CMDB != nil {
		gateway, err := blueking.New(bk)
		if err != nil {
			_ = r.Close()
			return nil, err
		}
		r.apigw = gateway
		baseURL := resources.CMDB.BaseURL
		if baseURL == "" {
			baseURL = strings.TrimRight(bk.APIURL, "/") + "/api/bk-cmdb/prod"
		}
		client, err := cmdb.New(baseURL, gateway)
		if err != nil {
			_ = r.Close()
			return nil, err
		}
		options = append(options, onemodel.WithLiveTargetReader(cmdb.TargetReader{Client: client, Directory: cmdbDirectory}))
	}
	r.Targets = onemodel.NewTargetResolver(directory, pager, topology, options...)
	return r, nil
}

// Close 等待调用者先停止使用，再幂等关闭本运行时拥有的资源。
func (r *Runtime) Close() error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		if r.apigw != nil {
			r.apigw.Close()
		}
		if r.db != nil {
			r.closeErr = errors.Join(r.closeErr, r.db.Close())
		}
		if r.transport != nil {
			r.closeErr = errors.Join(r.closeErr, r.transport.Close())
		}
	})
	return r.closeErr
}

type relationReader struct {
	reader    onemodel.Reader
	directory onemodel.TargetDirectory
}

func (r relationReader) Lookup(ctx context.Context, tenant string, origin onemodel.InstanceRef, relation, target string) ([]onemodel.InstanceRef, error) {
	d := onemodel.TargetDescriptor{SchemaVersion: 1, ModelID: origin.ModelID, Selectors: []onemodel.TargetSelector{{Type: "instances", Instances: []onemodel.InstanceRef{origin}}}}
	if err := d.Validate(); err != nil {
		return nil, err
	}
	if r.reader == nil || r.directory == nil || relation == "" || len(relation) > 256 {
		return nil, policy.ErrUnavailable
	}
	// bk_obj_asst_id 是 CMDB 的原始 relation_identity，不按短 relation_code 猜测或拼接。
	// 两端 canonical 模型均须属于当前租户的 CMDB/legacy 目录。
	for _, id := range []string{origin.ModelID, target} {
		model, found, err := r.directory.Model(ctx, tenant, id)
		if err != nil {
			return nil, err
		}
		if !found || (model.DataSource != "cmdb" && model.DataSource != "legacy") || model.CMDBObjectID == "" {
			return nil, policy.ErrUnavailable
		}
		if model.TenantID != tenant || model.ModelID != id {
			return nil, policy.ErrAccess
		}
	}
	roots := []onemodel.Instance{{TenantID: tenant, ModelCode: origin.ModelID, InstanceID: origin.InstanceID}}
	rows, err := r.reader.Related(ctx, tenant, roots, relation, "both", onemodel.Query{ModelID: target, Limit: 1024})
	if err != nil {
		return nil, err
	}
	if len(rows) > 1024 {
		return nil, policy.ErrUnavailable
	}
	refs := make([]onemodel.InstanceRef, 0, len(rows))
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		if row.TenantID != tenant || row.ModelCode != target || row.InstanceID == "" {
			return nil, policy.ErrAccess
		}
		if seen[row.InstanceID] {
			return nil, policy.ErrUnavailable
		}
		seen[row.InstanceID] = true
		refs = append(refs, onemodel.InstanceRef{ModelID: target, InstanceID: row.InstanceID, EntityUID: target + "|" + row.InstanceID})
	}
	if len(refs) > 0 {
		d := onemodel.TargetDescriptor{SchemaVersion: 1, ModelID: target, Selectors: []onemodel.TargetSelector{{Type: "instances", Instances: refs}}}
		if d.Validate() != nil {
			return nil, policy.ErrUnavailable
		}
	}
	return refs, nil
}
