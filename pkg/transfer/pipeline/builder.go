// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pipeline

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/emirpasic/gods/lists/doublylinkedlist"
	"github.com/emirpasic/gods/sets/hashset"
	"github.com/mitchellh/mapstructure"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// DefaultFrontendWaitDelay : 拉取结束后多久停掉流水线
var DefaultFrontendWaitDelay = time.Second

type visitContext struct {
	// raw exists which has been visited
	visitedNodes *hashset.Set
	// real exists for pipeline
	nodes *doublylinkedlist.List
}

func newVisitContext() *visitContext {
	return &visitContext{
		visitedNodes: hashset.New(),
		nodes:        doublylinkedlist.New(),
	}
}

// Builder : 流水线构造器
type Builder struct {
	name   string
	errors *utils.MultiErrors
	ctx    context.Context
	// root node
	frontend Node
	// declared exists
	nodes map[string]Node
	// declared edges linked exists
	edges map[string][]string
}

// String : as mermaidjs format
func (b *Builder) String() string {
	buffer := bytes.NewBuffer(nil)

	for name, node := range b.nodes {
		_, err := fmt.Fprintf(buffer, "%s[%v]\n", name, node)
		logging.PanicIf(err)
	}

	for from, edges := range b.edges {
		for _, to := range edges {
			_, err := fmt.Fprintf(buffer, "%s --> %s\n", from, to)
			logging.PanicIf(err)
		}
	}
	return buffer.String()
}

// AddError :
func (b *Builder) AddError(err error) {
	logging.Warnf("received error %+v", err)
	b.errors.Add(err)
}

// Error :
func (b *Builder) Error() error {
	return b.errors.AsError()
}

func (b *Builder) getName(node Node) string {
	return fmt.Sprintf("%x", reflect.Indirect(reflect.ValueOf(node)).Addr().Pointer())
}

// Declare :
func (b *Builder) Declare(nodes ...Node) *Builder {
	for _, node := range nodes {
		name := b.getName(node)
		_, ok := b.nodes[name]
		if !ok {
			b.nodes[name] = node
			b.edges[name] = make([]string, 0, 1)
		}
	}
	return b
}

// ConnectFrontend :
func (b *Builder) ConnectFrontend(to Node) *Builder {
	return b.Connect(b.frontend, to)
}

// Connect :
func (b *Builder) Connect(from, to Node) *Builder {
	b.Declare(from, to)

	name := b.getName(from)
	edges := b.edges[name]
	b.edges[name] = append(edges, b.getName(to))
	return b
}

func (b *Builder) getNode(node interface{}) (Node, error) {
	switch n := node.(type) {
	case string:
		value, ok := b.nodes[n]
		if !ok {
			return nil, define.ErrItemNotFound
		}
		return value, nil
	case Node:
		return n, nil
	}
	return nil, define.ErrType
}

func (b *Builder) getEdges(node interface{}) ([]string, error) {
	key := ""
	switch n := node.(type) {
	case string:
		key = n
	case Node:
		key = b.getName(n)
	default:
		return nil, errors.Wrapf(define.ErrType, "unknown type %t", node)
	}

	val, ok := b.edges[key]
	if !ok {
		return nil, define.ErrItemNotFound
	}

	return val, nil
}

func (b *Builder) checkConnectLoop(from, to interface{}) error {
	fNode, err := b.getNode(from)
	logging.PanicIf(err)
	tNode, err := b.getNode(to)
	logging.PanicIf(err)

	if b.isConnectLoop(fNode, tNode, 0) {
		return errors.Wrapf(define.ErrOperationForbidden, "connection loop between %v to %v", fNode, tNode)
	}

	return nil
}

func (b *Builder) isConnectLoop(from, to interface{}, depth int) bool {
	if depth > len(b.nodes) {
		return true
	}

	fNode, err := b.getNode(from)
	logging.PanicIf(err)
	tNode, err := b.getNode(to)
	logging.PanicIf(err)

	if fNode == tNode {
		return true
	}

	edges, err := b.getEdges(to)
	if err != nil {
		return false
	}
	if len(edges) == 0 {
		return false
	}

	name := b.getName(fNode)
	for _, value := range edges {
		if value == name {
			return true
		}
		if b.isConnectLoop(fNode, value, depth+1) {
			return true
		}
	}
	return false
}

func (b *Builder) visitPassBy(ctx *visitContext, from Node, to interface{}) error {
	node, err := b.getNode(to)
	if err != nil {
		return errors.Wrapf(err, "node not found in edges of %v", from)
	}

	err = b.checkConnectLoop(from, node)
	if err != nil {
		return err
	}

	err = b.visit(ctx, node)
	if err != nil {
		return err
	}

	err = from.ConnectTo(node)
	if err != nil {
		return err
	}

	ctx.nodes.Add(node)

	return nil
}

func (b *Builder) visitMultiOutput(ctx *visitContext, from Node, to []string) error {
	var connector Node
	if from.NoCopy() {
		connector = NewRoundRobinConnector(b.ctx, from)
	} else {
		connector = NewFanOutConnector(b.ctx, from)
	}

	node := NewGroupConnector(b.ctx, connector)

	for _, key := range to {
		subNode, err := b.getNode(key)
		if err != nil {
			return err
		}

		err = b.checkConnectLoop(from, subNode)
		if err != nil {
			return err
		}

		err = b.visit(ctx, subNode)
		if err != nil {
			return err
		}

		err = connector.ConnectTo(subNode)
		if err != nil {
			return err
		}
		node.Join(subNode)
	}

	err := from.ConnectTo(node)
	if err != nil {
		return err
	}

	ctx.nodes.Add(node)

	return nil
}

func (b *Builder) visit(ctx *visitContext, root Node) error {
	edges, err := b.getEdges(root)
	if err != nil {
		return errors.Wrapf(err, "edges not found for %v", root)
	}

	ctx.visitedNodes.Add(root)

	switch len(edges) {
	case 0:
		break
	case 1:
		err = b.visitPassBy(ctx, root, edges[0])
		if err != nil {
			return err
		}
	default:
		err = b.visitMultiOutput(ctx, root, edges)
		if err != nil {
			return err
		}
	}

	return nil
}

// SetFrontend :
func (b *Builder) SetFrontend(frontend Node) *Builder {
	b.Declare(frontend)
	b.frontend = frontend
	return b
}

// checkEdgesLeak :
func (b *Builder) checkEdgesLeak(ctx *visitContext) error {
	errs := utils.NewMultiErrors()
	visitedNodes := ctx.visitedNodes
	if visitedNodes.Size() != len(b.edges) {
		for name := range b.edges {
			if !visitedNodes.Contains(name) {
				node, err := b.getNode(name)
				if err != nil {
					errs.Add(errors.Wrapf(err, "node not found"))
				} else {
					errs.Add(errors.Wrapf(define.ErrOperationForbidden, "node %v leak", node))
				}
			}
		}
	}
	return errs.AsError()
}

func (b *Builder) getNodes(ctx *visitContext) ([]Node, error) {
	nodes := make([]Node, 0, ctx.nodes.Size())

	for _, node := range ctx.nodes.Values() {
		node, err := b.getNode(node)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

// ConnectNodes : 连接多个节点
func (b *Builder) ConnectNodes(nodes ...Node) *Builder {
	var last Node
	for _, node := range nodes {
		if last != nil {
			b.Connect(last, node)
		}
		last = node
	}
	return b
}

// Finish :
func (b *Builder) Finish() (*Pipeline, error) {
	err := b.errors.AsError()
	if err != nil {
		return nil, err
	}

	if b.frontend == nil {
		return nil, errors.Wrapf(define.ErrOperationForbidden, "frontend node not set")
	}

	ctx := newVisitContext()
	ctx.nodes.Add(b.frontend)
	err = b.visit(ctx, b.frontend)
	if err != nil {
		return nil, err
	}

	err = b.checkEdgesLeak(ctx)
	if err != nil {
		return nil, err
	}

	nodes, err := b.getNodes(ctx)
	if err != nil {
		return nil, err
	}

	return NewPipeline(b.ctx, b.name, nodes), nil
}

// NewBuilder :
func NewBuilder(ctx context.Context, name string) *Builder {
	return &Builder{
		name:   name,
		ctx:    ctx,
		errors: utils.NewMultiErrors(),
		nodes:  make(map[string]Node),
		edges:  make(map[string][]string),
	}
}

// NewBuilderWithFrontend :
func NewBuilderWithFrontend(ctx context.Context, frontend Node, name string) *Builder {
	builder := NewBuilder(ctx, name)
	builder.SetFrontend(frontend)
	return builder
}

// ContextBuilderBranchingCallback : 分支路径构造方法
type ContextBuilderBranchingCallback func(ctx context.Context, from Node, to Node) error

// ConfigBuilder
type ConfigBuilder struct {
	*Builder
	frontendWaitDelay time.Duration

	PipeConfigInitFn            func(pipelineConfig *config.PipelineConfig)
	TableConfigInitFn           func(tableConfig *config.MetaResultTableConfig)
	FrontendClusterConfigInitFn func(cluster *config.MetaClusterInfo)
	BackendClusterConfigInitFn  func(cluster *config.MetaClusterInfo)
}

// NewConfigBuilder
func NewConfigBuilder(ctx context.Context, name string) *ConfigBuilder {
	return NewConfigBuilderWithWaitDelay(ctx, name, DefaultFrontendWaitDelay)
}

// NewConfigBuilderWithWaitDelay
func NewConfigBuilderWithWaitDelay(ctx context.Context, name string, frontendWaitDelay time.Duration) *ConfigBuilder {
	return &ConfigBuilder{
		Builder:           NewBuilder(ctx, name),
		frontendWaitDelay: frontendWaitDelay,
	}
}

// FrontendProcessor :
func (b *ConfigBuilder) FrontendProcessor(ctx context.Context) (Node, error) {
	mqConf := config.MQConfigFromContext(ctx)
	if mqConf == nil {
		return nil, errors.Wrapf(define.ErrItemNotFound, "get mq config failed")
	}

	if b.FrontendClusterConfigInitFn != nil {
		b.FrontendClusterConfigInitFn(mqConf)
	}

	frontend, err := define.NewFrontend(ctx, mqConf.ClusterType)
	if err != nil {
		return nil, errors.Wrapf(err, "create frontend by type %v", mqConf.ClusterType)
	}
	ctx, cancel := context.WithCancel(ctx)
	frontendProcessor := NewFrontendNode(ctx, cancel, frontend, b.frontendWaitDelay)
	return frontendProcessor, nil
}

// ConnectFrontend
func (b *ConfigBuilder) ConnectFrontend(to Node) *ConfigBuilder {
	if b.frontend == nil {
		b.SetupFrontend()
	}
	b.Builder.ConnectFrontend(to)
	return b
}

// SetupFrontend :
func (b *ConfigBuilder) SetupFrontend() *ConfigBuilder {
	frontend, err := b.FrontendProcessor(b.ctx)
	if err != nil {
		b.AddError(err)
		return b
	}
	b.SetFrontend(frontend)

	return b
}

// GetBackendByContextFields : 按照context中的结果表shipper配置，得到该结果表的写入后端processor
func (b *ConfigBuilder) GetBackendByContextFields(ctx context.Context, f *define.ETLRecordFields) (Node, error) {
	rt := config.ResultTableConfigFromContext(ctx)
	processors := make([]Node, 0, len(rt.ShipperList))
	for _, s := range rt.ShipperList {
		if b.BackendClusterConfigInitFn != nil {
			b.BackendClusterConfigInitFn(s)
		}

		shipperCtx := context.WithValue(ctx, define.ContextShipperKey, s)
		backend, err := define.NewBackend(shipperCtx, s.ClusterType)
		if err != nil {
			return nil, errors.Wrapf(err, "create backend by type %v", s.ClusterType)
		}
		if f != nil {
			backend.SetETLRecordFields(f)
		}

		ctx, cancel := context.WithCancel(ctx)
		backendProcessor := NewBackendNode(ctx, cancel, backend)
		processors = append(processors, backendProcessor)
	}

	switch len(processors) {
	case 0:
		return nil, nil
	case 1:
		return processors[0], nil
	default:
		return NewChainConnector(ctx, processors), nil
	}
}

// GetBackendByContext : 按照context中的结果表shipper配置，得到该结果表的写入后端processor
func (b *ConfigBuilder) GetBackendByContext(ctx context.Context) (Node, error) {
	return b.GetBackendByContextFields(ctx, nil)
}

// DataProcessor :
func (b *ConfigBuilder) DataProcessor(ctx context.Context, name string) (Node, error) {
	processor, err := define.NewDataProcessor(ctx, name)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	return NewProcessNode(ctx, cancel, processor), nil
}

// GetDataProcessors
func (b *ConfigBuilder) GetDataProcessors(ctx context.Context, processors ...string) ([]Node, error) {
	nodes := make([]Node, 0, len(processors))
	for _, name := range processors {
		processor, err := b.DataProcessor(ctx, name)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, processor)
	}
	return nodes, nil
}

// BuildBranchingWithGluttonous: 建立一个可以最终是否没有消费者的pipeline
func (b *ConfigBuilder) BuildBranchingWithGluttonous(from Node, callback ContextBuilderBranchingCallback) (*Pipeline, error) {
	return b.BuildBranching(from, true, callback)
}

// BuildBranching :
func (b *ConfigBuilder) BuildBranching(from Node, allowGluttonous bool, callback ContextBuilderBranchingCallback) (*Pipeline, error) {
	ctx := b.ctx
	conf := config.FromContext(ctx)
	strictMode := conf.GetBool(define.ConfPipelineStrictMode)

	// 此处会通过ctx中的pipeline mq_config指定frontend（数据读取来源）
	if from == nil { // 如果起点没有指明,则将frontend 作为第一个lastNode
		if b.frontend == nil {
			b.SetupFrontend()
		}
		from = b.frontend
	}

	// 从ctx中，获取pipeline的配置；此处的pipeline配置，则是这个data_id在consul下的json配置内容
	pipeConfig := config.PipelineConfigFromContext(ctx)
	if pipeConfig.ResultTableList == nil || len(pipeConfig.ResultTableList) == 0 {
		return nil, errors.Wrapf(define.ErrOperationForbidden, "result table is empty")
	}
	// 判断是否有pipeline初始化方法指定，如果有，需要依赖pipeline的
	if b.PipeConfigInitFn != nil {
		// 初始化整个pipeline的option配置
		b.PipeConfigInitFn(pipeConfig)
	}

	// 遍历rtTable
	for _, rt := range pipeConfig.ResultTableList {
		if rt.ResultTable == "" {
			logging.Warnf("create etl data processor %d failed because of empty result table", pipeConfig.DataID)
			continue
		}
		if b.TableConfigInitFn != nil {
			// 初始化各个结果表的result_table option配置
			b.TableConfigInitFn(rt)
		}

		// 将单个结果表的配置放置到context中
		subCtx := config.ResultTableConfigIntoContext(ctx, rt)
		// 得到该结果表的写入后端Node
		backend, err := b.GetBackendByContext(subCtx)
		if err != nil {
			if strictMode {
				return nil, errors.Wrapf(err, "get result table %s backend failed", rt.ResultTable)
			}
			logging.Warnf("get result table %s backend error %v", rt.ResultTable, err)
			continue
		}
		if backend == nil {
			if allowGluttonous {
				backend = NewGluttonousNode(subCtx)
			} else {
				logging.Warnf("result table %s backend is empty, skipped", rt.ResultTable)
				continue
			}
		}
		multiNum := rt.MultiNum
		// 环境变量直接覆盖
		multiNum = GetPipeLineNum(pipeConfig.DataID)
		logging.Debugf("pipeline %d will parallel handling by %d processors", pipeConfig.DataID, multiNum)

		var passer Node
		// 如果启用并发模型，前后端都要做对应处理
		if multiNum > 1 {
			passer, err = b.DataProcessor(subCtx, "passer")
			if err != nil {
				return nil, err
			}
			passer.SetNoCopy(true)
			b.Connect(from, passer)
			backend = NewFanInConnector(ctx, backend)
		} else {
			passer = from
		}

		// 根据配置的并发数，同个rt表分裂多个pipeline并行处理数据
		for index := 0; index < multiNum; index++ {
			// 写入运行时信息
			runtimeConfig := new(config.RuntimeConfig)
			runtimeConfig.PipelineCount = index
			runtimeCtx := config.RuntimeConfigIntoContext(subCtx, runtimeConfig)

			err = callback(runtimeCtx, passer, backend)
			if err != nil {
				if strictMode {
					return nil, errors.Wrapf(err, "create branching by %s failed", rt.ResultTable)
				}
				logging.Warnf("create etl data processor %s error %v", rt.ResultTable, err)
				continue
			}
		}

	}

	logging.Debugf("pipeline %v layout: %v", pipeConfig.DataID, b)
	return b.Finish() // 返回一个 NewPipeline(b.context, b.name, exists)
}

type BackendFields struct {
	RawES     define.ETLRecordFields `json:"raw_es" mapstructure:"raw_es"`
	PatternES define.ETLRecordFields `json:"pattern_es" mapstructure:"pattern_es"`
}

// getBackendFields 调用方需要自行判断其属性是否为空
func (b *ConfigBuilder) getBackendFields(rtOpts map[string]interface{}) BackendFields {
	var conf BackendFields
	v, ok := rtOpts[config.PipelineConfigOptLogClusterConfig]
	if !ok {
		return conf
	}
	obj, ok := v.(map[string]interface{})
	if !ok {
		return conf
	}

	_ = mapstructure.Decode(obj[config.PipelineConfigOptBackendFields], &conf)
	return conf
}

func (b *ConfigBuilder) BuildBranchingForLogCluster(from Node, callbacks ...ContextBuilderBranchingCallback) (*Pipeline, error) {
	ctx := b.ctx

	// 当关闭日志聚类时 回退到正常的日志清洗分支
	// callbacks[0] : bk_flat_batch
	// callbacks[1] : bk_log_cluster
	pipeConfig := config.PipelineConfigFromContext(ctx)
	pipeOpts := utils.NewMapHelper(pipeConfig.Option)
	isLogCluster, _ := pipeOpts.GetBool(config.PipelineConfigOptIsLogCluster)
	if !isLogCluster {
		pipeConfig.ResultTableList = pipeConfig.ResultTableList[:1] // 避免写入两个 ES
		config.PipelineConfigIntoContext(b.ctx, pipeConfig)
		return b.BuildBranching(from, true, callbacks[0])
	}

	conf := config.FromContext(ctx)
	strictMode := conf.GetBool(define.ConfPipelineStrictMode)
	fields := b.getBackendFields(pipeConfig.Option)

	// 日志聚类会从单个数据源派生出多个分支
	// 但此流程只会在内部处理 共用同一个数据源
	if from == nil {
		if b.frontend == nil {
			b.SetupFrontend()
		}
		from = b.frontend
	}

	if len(pipeConfig.ResultTableList) == 0 {
		return nil, errors.Wrapf(define.ErrOperationForbidden, "result table is empty")
	}

	// 日志聚类必须保证两个 ES backend (raw/pattern)
	if len(pipeConfig.ResultTableList) != 2 {
		return nil, errors.Wrapf(define.ErrOperationForbidden, "result table missing")
	}

	// 初始化 pipeline 配置
	if b.PipeConfigInitFn != nil {
		b.PipeConfigInitFn(pipeConfig)
	}

	buildBackend := func(subCtx context.Context, f *define.ETLRecordFields) (Node, error) {
		rt := config.ResultTableConfigFromContext(subCtx)
		backend, err := b.GetBackendByContextFields(subCtx, f)
		if err != nil {
			if strictMode {
				return nil, errors.Wrapf(err, "get result table %s backend failed", rt.ResultTable)
			}
			logging.Warnf("get result table %s backend error %v", rt.ResultTable, err)
			return nil, nil // 非严格模式下忽略此错误
		}
		return backend, nil
	}

	chainNode := func(subCtx context.Context, cb ContextBuilderBranchingCallback, backend Node) error {
		var passer Node
		var err error

		rt := config.ResultTableConfigFromContext(subCtx)
		multiNum := defaultPipelineNums
		opts := utils.NewMapHelper(rt.Option)
		n, _ := opts.GetInt("multi_num")
		if n > 0 {
			multiNum = n
		}

		if multiNum > 1 {
			passer, err = b.DataProcessor(subCtx, "passer")
			if err != nil {
				return err
			}
			passer.SetNoCopy(true)
			b.Connect(from, passer)
			backend = NewFanInConnector(subCtx, backend)
		} else {
			passer = from
		}

		for index := 0; index < multiNum; index++ {
			runtimeConfig := new(config.RuntimeConfig)
			runtimeConfig.PipelineCount = index
			runtimeCtx := config.RuntimeConfigIntoContext(subCtx, runtimeConfig)

			err = cb(runtimeCtx, passer, backend)
			if err != nil {
				if strictMode {
					return errors.Wrapf(err, "create branching by %s failed", rt.ResultTable)
				}
				// 非严格模式下忽略此错误
				logging.Warnf("create etl data processor %s error %v", rt.ResultTable, err)
			}
		}
		return nil
	}

	rt0 := pipeConfig.ResultTableList[0]
	ctx0 := config.ResultTableConfigIntoContext(ctx, rt0)
	backend0, err := buildBackend(ctx0, &fields.RawES)
	if err != nil {
		return nil, err
	}

	rt1 := pipeConfig.ResultTableList[1]
	rt1.FieldList = rt0.FieldList // 复用 rt0 的 fieldslist 因为 pattern 是依附于 raw 而存在的
	ctx1 := config.ResultTableConfigIntoContext(ctx, rt1)
	backend1, err := buildBackend(ctx1, &fields.PatternES)
	if err != nil {
		return nil, err
	}

	// 统一使用 rt0 的配置
	// 使用 chainConnector 构建成多端 backend 具体处理差异由 backend 自行处理 不在 processor 体现
	if err := chainNode(ctx0, callbacks[1], NewChainConnector(ctx, []Node{backend0, backend1})); err != nil {
		return nil, err
	}

	logging.Debugf("pipeline %v layout: %v", pipeConfig.DataID, b)
	return b.Finish()
}
