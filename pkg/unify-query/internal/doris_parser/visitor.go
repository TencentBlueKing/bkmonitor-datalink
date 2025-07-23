// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package doris_parser

import (
	"context"
	"fmt"
	"sync/atomic"

	antlr "github.com/antlr4-go/antlr/v4"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/doris_parser/gen"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

var _ gen.DorisParserVisitor = (*DorisVisitor)(nil)

type DorisVisitor struct {
	gen.BaseDorisParserVisitor

	ctx context.Context
	opt DorisVisitorOption

	originalSQL string

	dexIdx *int64
	errs   []error
}

func (v *DorisVisitor) Visit(tree antlr.ParseTree) interface{} {
	return nil
}

func (v *DorisVisitor) VisitMultiStatements(ctx *gen.MultiStatementsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSingleStatement(ctx *gen.SingleStatementContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitStatementBaseAlias(ctx *gen.StatementBaseAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCallProcedure(ctx *gen.CallProcedureContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCreateProcedure(ctx *gen.CreateProcedureContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDropProcedure(ctx *gen.DropProcedureContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowProcedureStatus(ctx *gen.ShowProcedureStatusContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowCreateProcedure(ctx *gen.ShowCreateProcedureContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowConfig(ctx *gen.ShowConfigContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitStatementDefault(ctx *gen.StatementDefaultContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSupportedDmlStatementAlias(ctx *gen.SupportedDmlStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSupportedCreateStatementAlias(ctx *gen.SupportedCreateStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSupportedAlterStatementAlias(ctx *gen.SupportedAlterStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitMaterializedViewStatementAlias(ctx *gen.MaterializedViewStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSupportedJobStatementAlias(ctx *gen.SupportedJobStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitConstraintStatementAlias(ctx *gen.ConstraintStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSupportedCleanStatementAlias(ctx *gen.SupportedCleanStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSupportedDescribeStatementAlias(ctx *gen.SupportedDescribeStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSupportedDropStatementAlias(ctx *gen.SupportedDropStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSupportedSetStatementAlias(ctx *gen.SupportedSetStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSupportedUnsetStatementAlias(ctx *gen.SupportedUnsetStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSupportedRefreshStatementAlias(ctx *gen.SupportedRefreshStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSupportedShowStatementAlias(ctx *gen.SupportedShowStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSupportedLoadStatementAlias(ctx *gen.SupportedLoadStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSupportedCancelStatementAlias(ctx *gen.SupportedCancelStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSupportedRecoverStatementAlias(ctx *gen.SupportedRecoverStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSupportedAdminStatementAlias(ctx *gen.SupportedAdminStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSupportedUseStatementAlias(ctx *gen.SupportedUseStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSupportedOtherStatementAlias(ctx *gen.SupportedOtherStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSupportedKillStatementAlias(ctx *gen.SupportedKillStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSupportedStatsStatementAlias(ctx *gen.SupportedStatsStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSupportedTransactionStatementAlias(ctx *gen.SupportedTransactionStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSupportedGrantRevokeStatementAlias(ctx *gen.SupportedGrantRevokeStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitUnsupported(ctx *gen.UnsupportedContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitUnsupportedStatement(ctx *gen.UnsupportedStatementContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCreateMTMV(ctx *gen.CreateMTMVContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitRefreshMTMV(ctx *gen.RefreshMTMVContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAlterMTMV(ctx *gen.AlterMTMVContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDropMTMV(ctx *gen.DropMTMVContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitPauseMTMV(ctx *gen.PauseMTMVContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitResumeMTMV(ctx *gen.ResumeMTMVContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCancelMTMVTask(ctx *gen.CancelMTMVTaskContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowCreateMTMV(ctx *gen.ShowCreateMTMVContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCreateScheduledJob(ctx *gen.CreateScheduledJobContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitPauseJob(ctx *gen.PauseJobContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDropJob(ctx *gen.DropJobContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitResumeJob(ctx *gen.ResumeJobContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCancelJobTask(ctx *gen.CancelJobTaskContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAddConstraint(ctx *gen.AddConstraintContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDropConstraint(ctx *gen.DropConstraintContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowConstraint(ctx *gen.ShowConstraintContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitInsertTable(ctx *gen.InsertTableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitUpdate(ctx *gen.UpdateContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDelete(ctx *gen.DeleteContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitLoad(ctx *gen.LoadContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitExport(ctx *gen.ExportContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitReplay(ctx *gen.ReplayContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCopyInto(ctx *gen.CopyIntoContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitTruncateTable(ctx *gen.TruncateTableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCreateTable(ctx *gen.CreateTableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCreateView(ctx *gen.CreateViewContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCreateFile(ctx *gen.CreateFileContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCreateTableLike(ctx *gen.CreateTableLikeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCreateRole(ctx *gen.CreateRoleContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCreateWorkloadGroup(ctx *gen.CreateWorkloadGroupContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCreateCatalog(ctx *gen.CreateCatalogContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCreateRowPolicy(ctx *gen.CreateRowPolicyContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCreateStoragePolicy(ctx *gen.CreateStoragePolicyContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitBuildIndex(ctx *gen.BuildIndexContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCreateIndex(ctx *gen.CreateIndexContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCreateWorkloadPolicy(ctx *gen.CreateWorkloadPolicyContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCreateSqlBlockRule(ctx *gen.CreateSqlBlockRuleContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCreateEncryptkey(ctx *gen.CreateEncryptkeyContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCreateUserDefineFunction(ctx *gen.CreateUserDefineFunctionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCreateAliasFunction(ctx *gen.CreateAliasFunctionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCreateUser(ctx *gen.CreateUserContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCreateDatabase(ctx *gen.CreateDatabaseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCreateRepository(ctx *gen.CreateRepositoryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCreateResource(ctx *gen.CreateResourceContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCreateDictionary(ctx *gen.CreateDictionaryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCreateStage(ctx *gen.CreateStageContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCreateStorageVault(ctx *gen.CreateStorageVaultContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCreateIndexAnalyzer(ctx *gen.CreateIndexAnalyzerContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCreateIndexTokenizer(ctx *gen.CreateIndexTokenizerContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCreateIndexTokenFilter(ctx *gen.CreateIndexTokenFilterContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDictionaryColumnDefs(ctx *gen.DictionaryColumnDefsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDictionaryColumnDef(ctx *gen.DictionaryColumnDefContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAlterSystem(ctx *gen.AlterSystemContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAlterView(ctx *gen.AlterViewContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAlterCatalogRename(ctx *gen.AlterCatalogRenameContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAlterRole(ctx *gen.AlterRoleContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAlterStorageVault(ctx *gen.AlterStorageVaultContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAlterWorkloadGroup(ctx *gen.AlterWorkloadGroupContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAlterCatalogProperties(ctx *gen.AlterCatalogPropertiesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAlterWorkloadPolicy(ctx *gen.AlterWorkloadPolicyContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAlterSqlBlockRule(ctx *gen.AlterSqlBlockRuleContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAlterCatalogComment(ctx *gen.AlterCatalogCommentContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAlterDatabaseRename(ctx *gen.AlterDatabaseRenameContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAlterStoragePolicy(ctx *gen.AlterStoragePolicyContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAlterTable(ctx *gen.AlterTableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAlterTableAddRollup(ctx *gen.AlterTableAddRollupContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAlterTableDropRollup(ctx *gen.AlterTableDropRollupContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAlterTableProperties(ctx *gen.AlterTablePropertiesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAlterDatabaseSetQuota(ctx *gen.AlterDatabaseSetQuotaContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAlterDatabaseProperties(ctx *gen.AlterDatabasePropertiesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAlterSystemRenameComputeGroup(ctx *gen.AlterSystemRenameComputeGroupContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAlterResource(ctx *gen.AlterResourceContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAlterRepository(ctx *gen.AlterRepositoryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAlterRoutineLoad(ctx *gen.AlterRoutineLoadContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAlterColocateGroup(ctx *gen.AlterColocateGroupContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAlterUser(ctx *gen.AlterUserContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDropCatalogRecycleBin(ctx *gen.DropCatalogRecycleBinContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDropEncryptkey(ctx *gen.DropEncryptkeyContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDropRole(ctx *gen.DropRoleContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDropSqlBlockRule(ctx *gen.DropSqlBlockRuleContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDropUser(ctx *gen.DropUserContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDropStoragePolicy(ctx *gen.DropStoragePolicyContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDropWorkloadGroup(ctx *gen.DropWorkloadGroupContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDropCatalog(ctx *gen.DropCatalogContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDropFile(ctx *gen.DropFileContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDropWorkloadPolicy(ctx *gen.DropWorkloadPolicyContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDropRepository(ctx *gen.DropRepositoryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDropTable(ctx *gen.DropTableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDropDatabase(ctx *gen.DropDatabaseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDropFunction(ctx *gen.DropFunctionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDropIndex(ctx *gen.DropIndexContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDropResource(ctx *gen.DropResourceContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDropRowPolicy(ctx *gen.DropRowPolicyContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDropDictionary(ctx *gen.DropDictionaryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDropStage(ctx *gen.DropStageContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDropView(ctx *gen.DropViewContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDropIndexAnalyzer(ctx *gen.DropIndexAnalyzerContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDropIndexTokenizer(ctx *gen.DropIndexTokenizerContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDropIndexTokenFilter(ctx *gen.DropIndexTokenFilterContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowVariables(ctx *gen.ShowVariablesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowAuthors(ctx *gen.ShowAuthorsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowAlterTable(ctx *gen.ShowAlterTableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowCreateDatabase(ctx *gen.ShowCreateDatabaseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowBackup(ctx *gen.ShowBackupContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowBroker(ctx *gen.ShowBrokerContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowBuildIndex(ctx *gen.ShowBuildIndexContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowDynamicPartition(ctx *gen.ShowDynamicPartitionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowEvents(ctx *gen.ShowEventsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowExport(ctx *gen.ShowExportContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowLastInsert(ctx *gen.ShowLastInsertContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowCharset(ctx *gen.ShowCharsetContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowDelete(ctx *gen.ShowDeleteContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowCreateFunction(ctx *gen.ShowCreateFunctionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowFunctions(ctx *gen.ShowFunctionsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowGlobalFunctions(ctx *gen.ShowGlobalFunctionsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowGrants(ctx *gen.ShowGrantsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowGrantsForUser(ctx *gen.ShowGrantsForUserContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowCreateUser(ctx *gen.ShowCreateUserContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowSnapshot(ctx *gen.ShowSnapshotContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowLoadProfile(ctx *gen.ShowLoadProfileContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowCreateRepository(ctx *gen.ShowCreateRepositoryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowView(ctx *gen.ShowViewContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowPlugins(ctx *gen.ShowPluginsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowStorageVault(ctx *gen.ShowStorageVaultContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowRepositories(ctx *gen.ShowRepositoriesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowEncryptKeys(ctx *gen.ShowEncryptKeysContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowCreateTable(ctx *gen.ShowCreateTableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowProcessList(ctx *gen.ShowProcessListContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowPartitions(ctx *gen.ShowPartitionsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowRestore(ctx *gen.ShowRestoreContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowRoles(ctx *gen.ShowRolesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowPartitionId(ctx *gen.ShowPartitionIdContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowPrivileges(ctx *gen.ShowPrivilegesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowProc(ctx *gen.ShowProcContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowSmallFiles(ctx *gen.ShowSmallFilesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowStorageEngines(ctx *gen.ShowStorageEnginesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowCreateCatalog(ctx *gen.ShowCreateCatalogContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowCatalog(ctx *gen.ShowCatalogContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowCatalogs(ctx *gen.ShowCatalogsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowUserProperties(ctx *gen.ShowUserPropertiesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowAllProperties(ctx *gen.ShowAllPropertiesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowCollation(ctx *gen.ShowCollationContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowRowPolicy(ctx *gen.ShowRowPolicyContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowStoragePolicy(ctx *gen.ShowStoragePolicyContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowSqlBlockRule(ctx *gen.ShowSqlBlockRuleContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowCreateView(ctx *gen.ShowCreateViewContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowDataTypes(ctx *gen.ShowDataTypesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowData(ctx *gen.ShowDataContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowCreateMaterializedView(ctx *gen.ShowCreateMaterializedViewContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowWarningErrors(ctx *gen.ShowWarningErrorsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowWarningErrorCount(ctx *gen.ShowWarningErrorCountContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowBackends(ctx *gen.ShowBackendsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowStages(ctx *gen.ShowStagesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowReplicaDistribution(ctx *gen.ShowReplicaDistributionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowResources(ctx *gen.ShowResourcesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowLoad(ctx *gen.ShowLoadContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowLoadWarings(ctx *gen.ShowLoadWaringsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowTriggers(ctx *gen.ShowTriggersContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowDiagnoseTablet(ctx *gen.ShowDiagnoseTabletContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowOpenTables(ctx *gen.ShowOpenTablesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowFrontends(ctx *gen.ShowFrontendsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowDatabaseId(ctx *gen.ShowDatabaseIdContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowColumns(ctx *gen.ShowColumnsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowTableId(ctx *gen.ShowTableIdContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowTrash(ctx *gen.ShowTrashContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowTypeCast(ctx *gen.ShowTypeCastContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowClusters(ctx *gen.ShowClustersContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowStatus(ctx *gen.ShowStatusContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowWhitelist(ctx *gen.ShowWhitelistContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowTabletsBelong(ctx *gen.ShowTabletsBelongContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowDataSkew(ctx *gen.ShowDataSkewContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowTableCreation(ctx *gen.ShowTableCreationContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowTabletStorageFormat(ctx *gen.ShowTabletStorageFormatContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowQueryProfile(ctx *gen.ShowQueryProfileContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowConvertLsc(ctx *gen.ShowConvertLscContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowTables(ctx *gen.ShowTablesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowViews(ctx *gen.ShowViewsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowTableStatus(ctx *gen.ShowTableStatusContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowDatabases(ctx *gen.ShowDatabasesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowTabletsFromTable(ctx *gen.ShowTabletsFromTableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowCatalogRecycleBin(ctx *gen.ShowCatalogRecycleBinContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowTabletId(ctx *gen.ShowTabletIdContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowDictionaries(ctx *gen.ShowDictionariesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowTransaction(ctx *gen.ShowTransactionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowReplicaStatus(ctx *gen.ShowReplicaStatusContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowWorkloadGroups(ctx *gen.ShowWorkloadGroupsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowCopy(ctx *gen.ShowCopyContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowQueryStats(ctx *gen.ShowQueryStatsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowIndex(ctx *gen.ShowIndexContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowWarmUpJob(ctx *gen.ShowWarmUpJobContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSync(ctx *gen.SyncContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCreateRoutineLoadAlias(ctx *gen.CreateRoutineLoadAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowCreateRoutineLoad(ctx *gen.ShowCreateRoutineLoadContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitPauseRoutineLoad(ctx *gen.PauseRoutineLoadContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitPauseAllRoutineLoad(ctx *gen.PauseAllRoutineLoadContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitResumeRoutineLoad(ctx *gen.ResumeRoutineLoadContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitResumeAllRoutineLoad(ctx *gen.ResumeAllRoutineLoadContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitStopRoutineLoad(ctx *gen.StopRoutineLoadContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowRoutineLoad(ctx *gen.ShowRoutineLoadContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowRoutineLoadTask(ctx *gen.ShowRoutineLoadTaskContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowIndexAnalyzer(ctx *gen.ShowIndexAnalyzerContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowIndexTokenizer(ctx *gen.ShowIndexTokenizerContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowIndexTokenFilter(ctx *gen.ShowIndexTokenFilterContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitKillConnection(ctx *gen.KillConnectionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitKillQuery(ctx *gen.KillQueryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitHelp(ctx *gen.HelpContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitUnlockTables(ctx *gen.UnlockTablesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitInstallPlugin(ctx *gen.InstallPluginContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitUninstallPlugin(ctx *gen.UninstallPluginContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitLockTables(ctx *gen.LockTablesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitRestore(ctx *gen.RestoreContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitWarmUpCluster(ctx *gen.WarmUpClusterContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitBackup(ctx *gen.BackupContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitUnsupportedStartTransaction(ctx *gen.UnsupportedStartTransactionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitWarmUpItem(ctx *gen.WarmUpItemContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitLockTable(ctx *gen.LockTableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCreateRoutineLoad(ctx *gen.CreateRoutineLoadContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitMysqlLoad(ctx *gen.MysqlLoadContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowCreateLoad(ctx *gen.ShowCreateLoadContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSeparator(ctx *gen.SeparatorContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitImportColumns(ctx *gen.ImportColumnsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitImportPrecedingFilter(ctx *gen.ImportPrecedingFilterContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitImportWhere(ctx *gen.ImportWhereContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitImportDeleteOn(ctx *gen.ImportDeleteOnContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitImportSequence(ctx *gen.ImportSequenceContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitImportPartitions(ctx *gen.ImportPartitionsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitImportSequenceStatement(ctx *gen.ImportSequenceStatementContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitImportDeleteOnStatement(ctx *gen.ImportDeleteOnStatementContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitImportWhereStatement(ctx *gen.ImportWhereStatementContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitImportPrecedingFilterStatement(ctx *gen.ImportPrecedingFilterStatementContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitImportColumnsStatement(ctx *gen.ImportColumnsStatementContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitImportColumnDesc(ctx *gen.ImportColumnDescContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitRefreshCatalog(ctx *gen.RefreshCatalogContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitRefreshDatabase(ctx *gen.RefreshDatabaseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitRefreshTable(ctx *gen.RefreshTableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitRefreshDictionary(ctx *gen.RefreshDictionaryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitRefreshLdap(ctx *gen.RefreshLdapContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCleanAllProfile(ctx *gen.CleanAllProfileContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCleanLabel(ctx *gen.CleanLabelContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCleanQueryStats(ctx *gen.CleanQueryStatsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCleanAllQueryStats(ctx *gen.CleanAllQueryStatsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCancelLoad(ctx *gen.CancelLoadContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCancelExport(ctx *gen.CancelExportContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCancelWarmUpJob(ctx *gen.CancelWarmUpJobContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCancelDecommisionBackend(ctx *gen.CancelDecommisionBackendContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCancelBackup(ctx *gen.CancelBackupContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCancelRestore(ctx *gen.CancelRestoreContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCancelBuildIndex(ctx *gen.CancelBuildIndexContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCancelAlterTable(ctx *gen.CancelAlterTableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAdminShowReplicaDistribution(ctx *gen.AdminShowReplicaDistributionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAdminRebalanceDisk(ctx *gen.AdminRebalanceDiskContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAdminCancelRebalanceDisk(ctx *gen.AdminCancelRebalanceDiskContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAdminDiagnoseTablet(ctx *gen.AdminDiagnoseTabletContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAdminShowReplicaStatus(ctx *gen.AdminShowReplicaStatusContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAdminCompactTable(ctx *gen.AdminCompactTableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAdminCheckTablets(ctx *gen.AdminCheckTabletsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAdminShowTabletStorageFormat(ctx *gen.AdminShowTabletStorageFormatContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAdminSetFrontendConfig(ctx *gen.AdminSetFrontendConfigContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAdminCleanTrash(ctx *gen.AdminCleanTrashContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAdminSetReplicaVersion(ctx *gen.AdminSetReplicaVersionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAdminSetTableStatus(ctx *gen.AdminSetTableStatusContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAdminSetReplicaStatus(ctx *gen.AdminSetReplicaStatusContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAdminRepairTable(ctx *gen.AdminRepairTableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAdminCancelRepairTable(ctx *gen.AdminCancelRepairTableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAdminCopyTablet(ctx *gen.AdminCopyTabletContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitRecoverDatabase(ctx *gen.RecoverDatabaseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitRecoverTable(ctx *gen.RecoverTableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitRecoverPartition(ctx *gen.RecoverPartitionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAdminSetPartitionVersion(ctx *gen.AdminSetPartitionVersionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitBaseTableRef(ctx *gen.BaseTableRefContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitWildWhere(ctx *gen.WildWhereContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitTransactionBegin(ctx *gen.TransactionBeginContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitTranscationCommit(ctx *gen.TranscationCommitContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitTransactionRollback(ctx *gen.TransactionRollbackContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitGrantTablePrivilege(ctx *gen.GrantTablePrivilegeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitGrantResourcePrivilege(ctx *gen.GrantResourcePrivilegeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitGrantRole(ctx *gen.GrantRoleContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitRevokeRole(ctx *gen.RevokeRoleContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitRevokeResourcePrivilege(ctx *gen.RevokeResourcePrivilegeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitRevokeTablePrivilege(ctx *gen.RevokeTablePrivilegeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitPrivilege(ctx *gen.PrivilegeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitPrivilegeList(ctx *gen.PrivilegeListContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAddBackendClause(ctx *gen.AddBackendClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDropBackendClause(ctx *gen.DropBackendClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDecommissionBackendClause(ctx *gen.DecommissionBackendClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAddObserverClause(ctx *gen.AddObserverClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDropObserverClause(ctx *gen.DropObserverClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAddFollowerClause(ctx *gen.AddFollowerClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDropFollowerClause(ctx *gen.DropFollowerClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAddBrokerClause(ctx *gen.AddBrokerClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDropBrokerClause(ctx *gen.DropBrokerClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDropAllBrokerClause(ctx *gen.DropAllBrokerClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAlterLoadErrorUrlClause(ctx *gen.AlterLoadErrorUrlClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitModifyBackendClause(ctx *gen.ModifyBackendClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitModifyFrontendOrBackendHostNameClause(ctx *gen.ModifyFrontendOrBackendHostNameClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDropRollupClause(ctx *gen.DropRollupClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAddRollupClause(ctx *gen.AddRollupClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAddColumnClause(ctx *gen.AddColumnClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAddColumnsClause(ctx *gen.AddColumnsClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDropColumnClause(ctx *gen.DropColumnClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitModifyColumnClause(ctx *gen.ModifyColumnClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitReorderColumnsClause(ctx *gen.ReorderColumnsClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAddPartitionClause(ctx *gen.AddPartitionClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDropPartitionClause(ctx *gen.DropPartitionClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitModifyPartitionClause(ctx *gen.ModifyPartitionClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitReplacePartitionClause(ctx *gen.ReplacePartitionClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitReplaceTableClause(ctx *gen.ReplaceTableClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitRenameClause(ctx *gen.RenameClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitRenameRollupClause(ctx *gen.RenameRollupClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitRenamePartitionClause(ctx *gen.RenamePartitionClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitRenameColumnClause(ctx *gen.RenameColumnClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAddIndexClause(ctx *gen.AddIndexClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDropIndexClause(ctx *gen.DropIndexClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitEnableFeatureClause(ctx *gen.EnableFeatureClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitModifyDistributionClause(ctx *gen.ModifyDistributionClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitModifyTableCommentClause(ctx *gen.ModifyTableCommentClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitModifyColumnCommentClause(ctx *gen.ModifyColumnCommentClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitModifyEngineClause(ctx *gen.ModifyEngineClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAlterMultiPartitionClause(ctx *gen.AlterMultiPartitionClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCreateOrReplaceTagClauses(ctx *gen.CreateOrReplaceTagClausesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCreateOrReplaceBranchClauses(ctx *gen.CreateOrReplaceBranchClausesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDropBranchClauses(ctx *gen.DropBranchClausesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDropTagClauses(ctx *gen.DropTagClausesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCreateOrReplaceTagClause(ctx *gen.CreateOrReplaceTagClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCreateOrReplaceBranchClause(ctx *gen.CreateOrReplaceBranchClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitTagOptions(ctx *gen.TagOptionsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitBranchOptions(ctx *gen.BranchOptionsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitRetainTime(ctx *gen.RetainTimeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitRetentionSnapshot(ctx *gen.RetentionSnapshotContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitMinSnapshotsToKeep(ctx *gen.MinSnapshotsToKeepContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitTimeValueWithUnit(ctx *gen.TimeValueWithUnitContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDropBranchClause(ctx *gen.DropBranchClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDropTagClause(ctx *gen.DropTagClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitColumnPosition(ctx *gen.ColumnPositionContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitToRollup(ctx *gen.ToRollupContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitFromRollup(ctx *gen.FromRollupContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowAnalyze(ctx *gen.ShowAnalyzeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowQueuedAnalyzeJobs(ctx *gen.ShowQueuedAnalyzeJobsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowColumnHistogramStats(ctx *gen.ShowColumnHistogramStatsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAnalyzeDatabase(ctx *gen.AnalyzeDatabaseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAnalyzeTable(ctx *gen.AnalyzeTableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAlterTableStats(ctx *gen.AlterTableStatsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAlterColumnStats(ctx *gen.AlterColumnStatsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowIndexStats(ctx *gen.ShowIndexStatsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDropStats(ctx *gen.DropStatsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDropCachedStats(ctx *gen.DropCachedStatsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDropExpiredStats(ctx *gen.DropExpiredStatsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitKillAnalyzeJob(ctx *gen.KillAnalyzeJobContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDropAnalyzeJob(ctx *gen.DropAnalyzeJobContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowTableStats(ctx *gen.ShowTableStatsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowColumnStats(ctx *gen.ShowColumnStatsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitShowAnalyzeTask(ctx *gen.ShowAnalyzeTaskContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAnalyzeProperties(ctx *gen.AnalyzePropertiesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitWorkloadPolicyActions(ctx *gen.WorkloadPolicyActionsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitWorkloadPolicyAction(ctx *gen.WorkloadPolicyActionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitWorkloadPolicyConditions(ctx *gen.WorkloadPolicyConditionsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitWorkloadPolicyCondition(ctx *gen.WorkloadPolicyConditionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitStorageBackend(ctx *gen.StorageBackendContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitPasswordOption(ctx *gen.PasswordOptionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitFunctionArguments(ctx *gen.FunctionArgumentsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDataTypeList(ctx *gen.DataTypeListContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSetOptions(ctx *gen.SetOptionsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSetDefaultStorageVault(ctx *gen.SetDefaultStorageVaultContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSetUserProperties(ctx *gen.SetUserPropertiesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSetTransaction(ctx *gen.SetTransactionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSetVariableWithType(ctx *gen.SetVariableWithTypeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSetNames(ctx *gen.SetNamesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSetCharset(ctx *gen.SetCharsetContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSetCollate(ctx *gen.SetCollateContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSetPassword(ctx *gen.SetPasswordContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSetLdapAdminPassword(ctx *gen.SetLdapAdminPasswordContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSetVariableWithoutType(ctx *gen.SetVariableWithoutTypeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSetSystemVariable(ctx *gen.SetSystemVariableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSetUserVariable(ctx *gen.SetUserVariableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitTransactionAccessMode(ctx *gen.TransactionAccessModeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitIsolationLevel(ctx *gen.IsolationLevelContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSupportedUnsetStatement(ctx *gen.SupportedUnsetStatementContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSwitchCatalog(ctx *gen.SwitchCatalogContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitUseDatabase(ctx *gen.UseDatabaseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitUseCloudCluster(ctx *gen.UseCloudClusterContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitStageAndPattern(ctx *gen.StageAndPatternContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDescribeTableValuedFunction(ctx *gen.DescribeTableValuedFunctionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDescribeTableAll(ctx *gen.DescribeTableAllContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDescribeTable(ctx *gen.DescribeTableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDescribeDictionary(ctx *gen.DescribeDictionaryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitConstraint(ctx *gen.ConstraintContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitPartitionSpec(ctx *gen.PartitionSpecContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitPartitionTable(ctx *gen.PartitionTableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitIdentityOrFunctionList(ctx *gen.IdentityOrFunctionListContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitIdentityOrFunction(ctx *gen.IdentityOrFunctionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDataDesc(ctx *gen.DataDescContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitStatementScope(ctx *gen.StatementScopeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitBuildMode(ctx *gen.BuildModeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitRefreshTrigger(ctx *gen.RefreshTriggerContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitRefreshSchedule(ctx *gen.RefreshScheduleContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitRefreshMethod(ctx *gen.RefreshMethodContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitMvPartition(ctx *gen.MvPartitionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitIdentifierOrText(ctx *gen.IdentifierOrTextContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitIdentifierOrTextOrAsterisk(ctx *gen.IdentifierOrTextOrAsteriskContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitMultipartIdentifierOrAsterisk(ctx *gen.MultipartIdentifierOrAsteriskContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitIdentifierOrAsterisk(ctx *gen.IdentifierOrAsteriskContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitUserIdentify(ctx *gen.UserIdentifyContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitGrantUserIdentify(ctx *gen.GrantUserIdentifyContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitExplain(ctx *gen.ExplainContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitExplainCommand(ctx *gen.ExplainCommandContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitPlanType(ctx *gen.PlanTypeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitReplayCommand(ctx *gen.ReplayCommandContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitReplayType(ctx *gen.ReplayTypeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitMergeType(ctx *gen.MergeTypeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitPreFilterClause(ctx *gen.PreFilterClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDeleteOnClause(ctx *gen.DeleteOnClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSequenceColClause(ctx *gen.SequenceColClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitColFromPath(ctx *gen.ColFromPathContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitColMappingList(ctx *gen.ColMappingListContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitMappingExpr(ctx *gen.MappingExprContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitWithRemoteStorageSystem(ctx *gen.WithRemoteStorageSystemContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitResourceDesc(ctx *gen.ResourceDescContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitMysqlDataDesc(ctx *gen.MysqlDataDescContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSkipLines(ctx *gen.SkipLinesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitOutFileClause(ctx *gen.OutFileClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitQuery(ctx *gen.QueryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitQueryTermDefault(ctx *gen.QueryTermDefaultContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSetOperation(ctx *gen.SetOperationContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSetQuantifier(ctx *gen.SetQuantifierContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitQueryPrimaryDefault(ctx *gen.QueryPrimaryDefaultContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSubquery(ctx *gen.SubqueryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitValuesTable(ctx *gen.ValuesTableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitRegularQuerySpecification(ctx *gen.RegularQuerySpecificationContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCte(ctx *gen.CteContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAliasQuery(ctx *gen.AliasQueryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitColumnAliases(ctx *gen.ColumnAliasesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitWhereClause(ctx *gen.WhereClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitFromClause(ctx *gen.FromClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitIntoClause(ctx *gen.IntoClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitBulkCollectClause(ctx *gen.BulkCollectClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitTableRow(ctx *gen.TableRowContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitRelations(ctx *gen.RelationsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitRelation(ctx *gen.RelationContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitJoinRelation(ctx *gen.JoinRelationContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitBracketDistributeType(ctx *gen.BracketDistributeTypeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCommentDistributeType(ctx *gen.CommentDistributeTypeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitBracketRelationHint(ctx *gen.BracketRelationHintContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCommentRelationHint(ctx *gen.CommentRelationHintContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAggClause(ctx *gen.AggClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitGroupingElement(ctx *gen.GroupingElementContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitGroupingSet(ctx *gen.GroupingSetContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitHavingClause(ctx *gen.HavingClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitQualifyClause(ctx *gen.QualifyClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSelectHint(ctx *gen.SelectHintContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitHintStatement(ctx *gen.HintStatementContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitHintAssignment(ctx *gen.HintAssignmentContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitUpdateAssignment(ctx *gen.UpdateAssignmentContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitUpdateAssignmentSeq(ctx *gen.UpdateAssignmentSeqContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitLateralView(ctx *gen.LateralViewContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitQueryOrganization(ctx *gen.QueryOrganizationContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSortClause(ctx *gen.SortClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSortItem(ctx *gen.SortItemContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitLimitClause(ctx *gen.LimitClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitPartitionClause(ctx *gen.PartitionClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitJoinType(ctx *gen.JoinTypeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitJoinCriteria(ctx *gen.JoinCriteriaContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitIdentifierList(ctx *gen.IdentifierListContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitIdentifierSeq(ctx *gen.IdentifierSeqContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitOptScanParams(ctx *gen.OptScanParamsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitTableName(ctx *gen.TableNameContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAliasedQuery(ctx *gen.AliasedQueryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitTableValuedFunction(ctx *gen.TableValuedFunctionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitRelationList(ctx *gen.RelationListContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitMaterializedViewName(ctx *gen.MaterializedViewNameContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitPropertyClause(ctx *gen.PropertyClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitPropertyItemList(ctx *gen.PropertyItemListContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitPropertyItem(ctx *gen.PropertyItemContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitPropertyKey(ctx *gen.PropertyKeyContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitPropertyValue(ctx *gen.PropertyValueContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitTableAlias(ctx *gen.TableAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitMultipartIdentifier(ctx *gen.MultipartIdentifierContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSimpleColumnDefs(ctx *gen.SimpleColumnDefsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSimpleColumnDef(ctx *gen.SimpleColumnDefContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitColumnDefs(ctx *gen.ColumnDefsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitColumnDef(ctx *gen.ColumnDefContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitIndexDefs(ctx *gen.IndexDefsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitIndexDef(ctx *gen.IndexDefContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitPartitionsDef(ctx *gen.PartitionsDefContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitPartitionDef(ctx *gen.PartitionDefContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitLessThanPartitionDef(ctx *gen.LessThanPartitionDefContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitFixedPartitionDef(ctx *gen.FixedPartitionDefContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitStepPartitionDef(ctx *gen.StepPartitionDefContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitInPartitionDef(ctx *gen.InPartitionDefContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitPartitionValueList(ctx *gen.PartitionValueListContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitPartitionValueDef(ctx *gen.PartitionValueDefContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitRollupDefs(ctx *gen.RollupDefsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitRollupDef(ctx *gen.RollupDefContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAggTypeDef(ctx *gen.AggTypeDefContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitTabletList(ctx *gen.TabletListContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitInlineTable(ctx *gen.InlineTableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitNamedExpression(ctx *gen.NamedExpressionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitNamedExpressionSeq(ctx *gen.NamedExpressionSeqContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitExpression(ctx *gen.ExpressionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitLambdaExpression(ctx *gen.LambdaExpressionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitExist(ctx *gen.ExistContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitLogicalNot(ctx *gen.LogicalNotContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitPredicated(ctx *gen.PredicatedContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitIsnull(ctx *gen.IsnullContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitIs_not_null_pred(ctx *gen.Is_not_null_predContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitLogicalBinary(ctx *gen.LogicalBinaryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDoublePipes(ctx *gen.DoublePipesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitRowConstructor(ctx *gen.RowConstructorContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitRowConstructorItem(ctx *gen.RowConstructorItemContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitPredicate(ctx *gen.PredicateContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitValueExpressionDefault(ctx *gen.ValueExpressionDefaultContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitComparison(ctx *gen.ComparisonContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitArithmeticBinary(ctx *gen.ArithmeticBinaryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitArithmeticUnary(ctx *gen.ArithmeticUnaryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDereference(ctx *gen.DereferenceContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCurrentDate(ctx *gen.CurrentDateContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCast(ctx *gen.CastContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitParenthesizedExpression(ctx *gen.ParenthesizedExpressionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitUserVariable(ctx *gen.UserVariableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitElementAt(ctx *gen.ElementAtContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitLocalTimestamp(ctx *gen.LocalTimestampContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCharFunction(ctx *gen.CharFunctionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitIntervalLiteral(ctx *gen.IntervalLiteralContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSimpleCase(ctx *gen.SimpleCaseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitColumnReference(ctx *gen.ColumnReferenceContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitStar(ctx *gen.StarContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSessionUser(ctx *gen.SessionUserContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitConvertType(ctx *gen.ConvertTypeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitConvertCharSet(ctx *gen.ConvertCharSetContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSubqueryExpression(ctx *gen.SubqueryExpressionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitEncryptKey(ctx *gen.EncryptKeyContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCurrentTime(ctx *gen.CurrentTimeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitLocalTime(ctx *gen.LocalTimeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSystemVariable(ctx *gen.SystemVariableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCollate(ctx *gen.CollateContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCurrentUser(ctx *gen.CurrentUserContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitConstantDefault(ctx *gen.ConstantDefaultContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitExtract(ctx *gen.ExtractContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCurrentTimestamp(ctx *gen.CurrentTimestampContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitFunctionCall(ctx *gen.FunctionCallContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitArraySlice(ctx *gen.ArraySliceContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSearchedCase(ctx *gen.SearchedCaseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitExcept(ctx *gen.ExceptContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitReplace(ctx *gen.ReplaceContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCastDataType(ctx *gen.CastDataTypeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitFunctionCallExpression(ctx *gen.FunctionCallExpressionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitFunctionNameIdentifier(ctx *gen.FunctionNameIdentifierContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitWindowSpec(ctx *gen.WindowSpecContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitWindowFrame(ctx *gen.WindowFrameContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitFrameUnits(ctx *gen.FrameUnitsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitFrameBoundary(ctx *gen.FrameBoundaryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitQualifiedName(ctx *gen.QualifiedNameContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSpecifiedPartition(ctx *gen.SpecifiedPartitionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitNullLiteral(ctx *gen.NullLiteralContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitTypeConstructor(ctx *gen.TypeConstructorContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitNumericLiteral(ctx *gen.NumericLiteralContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitBooleanLiteral(ctx *gen.BooleanLiteralContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitStringLiteral(ctx *gen.StringLiteralContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitArrayLiteral(ctx *gen.ArrayLiteralContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitMapLiteral(ctx *gen.MapLiteralContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitStructLiteral(ctx *gen.StructLiteralContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitPlaceholder(ctx *gen.PlaceholderContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitComparisonOperator(ctx *gen.ComparisonOperatorContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitBooleanValue(ctx *gen.BooleanValueContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitWhenClause(ctx *gen.WhenClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitInterval(ctx *gen.IntervalContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitUnitIdentifier(ctx *gen.UnitIdentifierContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDataTypeWithNullable(ctx *gen.DataTypeWithNullableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitComplexDataType(ctx *gen.ComplexDataTypeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitAggStateDataType(ctx *gen.AggStateDataTypeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitPrimitiveDataType(ctx *gen.PrimitiveDataTypeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitPrimitiveColType(ctx *gen.PrimitiveColTypeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitComplexColTypeList(ctx *gen.ComplexColTypeListContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitComplexColType(ctx *gen.ComplexColTypeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitCommentSpec(ctx *gen.CommentSpecContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSample(ctx *gen.SampleContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSampleByPercentile(ctx *gen.SampleByPercentileContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitSampleByRows(ctx *gen.SampleByRowsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitTableSnapshot(ctx *gen.TableSnapshotContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitErrorCapturingIdentifier(ctx *gen.ErrorCapturingIdentifierContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitErrorIdent(ctx *gen.ErrorIdentContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitRealIdent(ctx *gen.RealIdentContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitIdentifier(ctx *gen.IdentifierContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitUnquotedIdentifier(ctx *gen.UnquotedIdentifierContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitQuotedIdentifierAlternative(ctx *gen.QuotedIdentifierAlternativeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitQuotedIdentifier(ctx *gen.QuotedIdentifierContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitIntegerLiteral(ctx *gen.IntegerLiteralContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitDecimalLiteral(ctx *gen.DecimalLiteralContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitNonReserved(ctx *gen.NonReservedContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) error(err error) {
	v.errs = append(v.errs, err)
}

// VisitChildren 安全访问子节点
func (v *DorisVisitor) VisitChildren(node antlr.RuleNode) interface{} {
	atomic.AddInt64(v.dexIdx, 1)
	for _, child := range node.GetChildren() {
		if parseTree, ok := child.(antlr.ParseTree); ok {
			log.Debugf(v.ctx, `"%d","ENTER","%T","%s"`, *v.dexIdx, parseTree, parseTree.GetText())
			parseTree.Accept(v)
			log.Debugf(v.ctx, `"%d","EXIT","%T","%s"`, *v.dexIdx, parseTree, parseTree.GetText())
		} else {
			v.error(fmt.Errorf("无效的子节点类型: %T", child))
			return nil
		}
	}
	atomic.AddInt64(v.dexIdx, -1)
	return nil
}

func (v *DorisVisitor) VisitSelectClause(ctx *gen.SelectClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitFunctionIdentifier(ctx *gen.FunctionIdentifierContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *DorisVisitor) VisitTerminal(node antlr.TerminalNode) interface{} {

	return nil
}

func (v *DorisVisitor) VisitErrorNode(node antlr.ErrorNode) interface{} {
	v.error(fmt.Errorf("visit node error: %s", node.GetText()))
	return nil
}

func (v *DorisVisitor) VisitSelectColumnClause(ctx *gen.SelectColumnClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

type DorisVisitorOption struct {
	DimensionTransform func(s string) (string, bool)
}

func (v *DorisVisitor) WithOptions(opt DorisVisitorOption) *DorisVisitor {
	v.opt = opt
	return v
}

// NewDorisVisitor 创建带Token流的Visitor
func NewDorisVisitor(ctx context.Context, input string) *DorisVisitor {

	return &DorisVisitor{
		ctx:         ctx,
		originalSQL: input,
		dexIdx:      new(int64),
	}
}
