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
	"regexp"
	"strings"

	antlr "github.com/antlr4-go/antlr/v4"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/doris_parser/gen"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

var _ gen.DorisParserVisitor = (*DorisVisitor)(nil)

type DorisVisitor struct {
	gen.BaseDorisParserVisitor
	Ctx context.Context

	opt DorisVisitorOption

	OriginalSQL string                   // 原始SQL
	ModifiedSQL string                   // 修改后的SQL
	Tokens      *antlr.CommonTokenStream // 用于修改token

	Select string // 解析出的 SELECT 表达式集合
	Table  string // 解析出的表名集合

	Where  string // 解析出的 WHERE 表达式集合
	Group  string
	Having string
	Order  string

	LimitOffset string

	Err error // 解析过程中的错误
}

func (v *DorisVisitor) VisitMultiStatements(ctx *gen.MultiStatementsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSingleStatement(ctx *gen.SingleStatementContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitStatementBaseAlias(ctx *gen.StatementBaseAliasContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCallProcedure(ctx *gen.CallProcedureContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCreateProcedure(ctx *gen.CreateProcedureContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDropProcedure(ctx *gen.DropProcedureContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowProcedureStatus(ctx *gen.ShowProcedureStatusContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowCreateProcedure(ctx *gen.ShowCreateProcedureContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowConfig(ctx *gen.ShowConfigContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitStatementDefault(ctx *gen.StatementDefaultContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSupportedDmlStatementAlias(ctx *gen.SupportedDmlStatementAliasContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSupportedCreateStatementAlias(ctx *gen.SupportedCreateStatementAliasContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSupportedAlterStatementAlias(ctx *gen.SupportedAlterStatementAliasContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitMaterializedViewStatementAlias(ctx *gen.MaterializedViewStatementAliasContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSupportedJobStatementAlias(ctx *gen.SupportedJobStatementAliasContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitConstraintStatementAlias(ctx *gen.ConstraintStatementAliasContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSupportedCleanStatementAlias(ctx *gen.SupportedCleanStatementAliasContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSupportedDescribeStatementAlias(ctx *gen.SupportedDescribeStatementAliasContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSupportedDropStatementAlias(ctx *gen.SupportedDropStatementAliasContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSupportedSetStatementAlias(ctx *gen.SupportedSetStatementAliasContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSupportedUnsetStatementAlias(ctx *gen.SupportedUnsetStatementAliasContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSupportedRefreshStatementAlias(ctx *gen.SupportedRefreshStatementAliasContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSupportedShowStatementAlias(ctx *gen.SupportedShowStatementAliasContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSupportedLoadStatementAlias(ctx *gen.SupportedLoadStatementAliasContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSupportedCancelStatementAlias(ctx *gen.SupportedCancelStatementAliasContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSupportedRecoverStatementAlias(ctx *gen.SupportedRecoverStatementAliasContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSupportedAdminStatementAlias(ctx *gen.SupportedAdminStatementAliasContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSupportedUseStatementAlias(ctx *gen.SupportedUseStatementAliasContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSupportedOtherStatementAlias(ctx *gen.SupportedOtherStatementAliasContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSupportedKillStatementAlias(ctx *gen.SupportedKillStatementAliasContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSupportedStatsStatementAlias(ctx *gen.SupportedStatsStatementAliasContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSupportedTransactionStatementAlias(ctx *gen.SupportedTransactionStatementAliasContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSupportedGrantRevokeStatementAlias(ctx *gen.SupportedGrantRevokeStatementAliasContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitUnsupported(ctx *gen.UnsupportedContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitUnsupportedStatement(ctx *gen.UnsupportedStatementContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCreateMTMV(ctx *gen.CreateMTMVContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitRefreshMTMV(ctx *gen.RefreshMTMVContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAlterMTMV(ctx *gen.AlterMTMVContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDropMTMV(ctx *gen.DropMTMVContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitPauseMTMV(ctx *gen.PauseMTMVContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitResumeMTMV(ctx *gen.ResumeMTMVContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCancelMTMVTask(ctx *gen.CancelMTMVTaskContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowCreateMTMV(ctx *gen.ShowCreateMTMVContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCreateScheduledJob(ctx *gen.CreateScheduledJobContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitPauseJob(ctx *gen.PauseJobContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDropJob(ctx *gen.DropJobContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitResumeJob(ctx *gen.ResumeJobContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCancelJobTask(ctx *gen.CancelJobTaskContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAddConstraint(ctx *gen.AddConstraintContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDropConstraint(ctx *gen.DropConstraintContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowConstraint(ctx *gen.ShowConstraintContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitInsertTable(ctx *gen.InsertTableContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitUpdate(ctx *gen.UpdateContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDelete(ctx *gen.DeleteContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitLoad(ctx *gen.LoadContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitExport(ctx *gen.ExportContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitReplay(ctx *gen.ReplayContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCopyInto(ctx *gen.CopyIntoContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitTruncateTable(ctx *gen.TruncateTableContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCreateTable(ctx *gen.CreateTableContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCreateView(ctx *gen.CreateViewContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCreateFile(ctx *gen.CreateFileContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCreateTableLike(ctx *gen.CreateTableLikeContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCreateRole(ctx *gen.CreateRoleContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCreateWorkloadGroup(ctx *gen.CreateWorkloadGroupContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCreateCatalog(ctx *gen.CreateCatalogContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCreateRowPolicy(ctx *gen.CreateRowPolicyContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCreateStoragePolicy(ctx *gen.CreateStoragePolicyContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitBuildIndex(ctx *gen.BuildIndexContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCreateIndex(ctx *gen.CreateIndexContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCreateWorkloadPolicy(ctx *gen.CreateWorkloadPolicyContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCreateSqlBlockRule(ctx *gen.CreateSqlBlockRuleContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCreateEncryptkey(ctx *gen.CreateEncryptkeyContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCreateUserDefineFunction(ctx *gen.CreateUserDefineFunctionContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCreateAliasFunction(ctx *gen.CreateAliasFunctionContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCreateUser(ctx *gen.CreateUserContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCreateDatabase(ctx *gen.CreateDatabaseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCreateRepository(ctx *gen.CreateRepositoryContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCreateResource(ctx *gen.CreateResourceContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCreateDictionary(ctx *gen.CreateDictionaryContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCreateStage(ctx *gen.CreateStageContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCreateStorageVault(ctx *gen.CreateStorageVaultContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCreateIndexAnalyzer(ctx *gen.CreateIndexAnalyzerContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCreateIndexTokenizer(ctx *gen.CreateIndexTokenizerContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCreateIndexTokenFilter(ctx *gen.CreateIndexTokenFilterContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDictionaryColumnDefs(ctx *gen.DictionaryColumnDefsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDictionaryColumnDef(ctx *gen.DictionaryColumnDefContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAlterSystem(ctx *gen.AlterSystemContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAlterView(ctx *gen.AlterViewContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAlterCatalogRename(ctx *gen.AlterCatalogRenameContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAlterRole(ctx *gen.AlterRoleContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAlterStorageVault(ctx *gen.AlterStorageVaultContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAlterWorkloadGroup(ctx *gen.AlterWorkloadGroupContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAlterCatalogProperties(ctx *gen.AlterCatalogPropertiesContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAlterWorkloadPolicy(ctx *gen.AlterWorkloadPolicyContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAlterSqlBlockRule(ctx *gen.AlterSqlBlockRuleContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAlterCatalogComment(ctx *gen.AlterCatalogCommentContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAlterDatabaseRename(ctx *gen.AlterDatabaseRenameContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAlterStoragePolicy(ctx *gen.AlterStoragePolicyContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAlterTable(ctx *gen.AlterTableContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAlterTableAddRollup(ctx *gen.AlterTableAddRollupContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAlterTableDropRollup(ctx *gen.AlterTableDropRollupContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAlterTableProperties(ctx *gen.AlterTablePropertiesContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAlterDatabaseSetQuota(ctx *gen.AlterDatabaseSetQuotaContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAlterDatabaseProperties(ctx *gen.AlterDatabasePropertiesContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAlterSystemRenameComputeGroup(ctx *gen.AlterSystemRenameComputeGroupContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAlterResource(ctx *gen.AlterResourceContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAlterRepository(ctx *gen.AlterRepositoryContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAlterRoutineLoad(ctx *gen.AlterRoutineLoadContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAlterColocateGroup(ctx *gen.AlterColocateGroupContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAlterUser(ctx *gen.AlterUserContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDropCatalogRecycleBin(ctx *gen.DropCatalogRecycleBinContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDropEncryptkey(ctx *gen.DropEncryptkeyContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDropRole(ctx *gen.DropRoleContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDropSqlBlockRule(ctx *gen.DropSqlBlockRuleContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDropUser(ctx *gen.DropUserContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDropStoragePolicy(ctx *gen.DropStoragePolicyContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDropWorkloadGroup(ctx *gen.DropWorkloadGroupContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDropCatalog(ctx *gen.DropCatalogContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDropFile(ctx *gen.DropFileContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDropWorkloadPolicy(ctx *gen.DropWorkloadPolicyContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDropRepository(ctx *gen.DropRepositoryContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDropTable(ctx *gen.DropTableContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDropDatabase(ctx *gen.DropDatabaseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDropFunction(ctx *gen.DropFunctionContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDropIndex(ctx *gen.DropIndexContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDropResource(ctx *gen.DropResourceContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDropRowPolicy(ctx *gen.DropRowPolicyContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDropDictionary(ctx *gen.DropDictionaryContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDropStage(ctx *gen.DropStageContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDropView(ctx *gen.DropViewContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDropIndexAnalyzer(ctx *gen.DropIndexAnalyzerContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDropIndexTokenizer(ctx *gen.DropIndexTokenizerContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDropIndexTokenFilter(ctx *gen.DropIndexTokenFilterContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowVariables(ctx *gen.ShowVariablesContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowAuthors(ctx *gen.ShowAuthorsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowAlterTable(ctx *gen.ShowAlterTableContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowCreateDatabase(ctx *gen.ShowCreateDatabaseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowBackup(ctx *gen.ShowBackupContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowBroker(ctx *gen.ShowBrokerContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowBuildIndex(ctx *gen.ShowBuildIndexContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowDynamicPartition(ctx *gen.ShowDynamicPartitionContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowEvents(ctx *gen.ShowEventsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowExport(ctx *gen.ShowExportContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowLastInsert(ctx *gen.ShowLastInsertContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowCharset(ctx *gen.ShowCharsetContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowDelete(ctx *gen.ShowDeleteContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowCreateFunction(ctx *gen.ShowCreateFunctionContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowFunctions(ctx *gen.ShowFunctionsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowGlobalFunctions(ctx *gen.ShowGlobalFunctionsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowGrants(ctx *gen.ShowGrantsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowGrantsForUser(ctx *gen.ShowGrantsForUserContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowCreateUser(ctx *gen.ShowCreateUserContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowSnapshot(ctx *gen.ShowSnapshotContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowLoadProfile(ctx *gen.ShowLoadProfileContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowCreateRepository(ctx *gen.ShowCreateRepositoryContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowView(ctx *gen.ShowViewContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowPlugins(ctx *gen.ShowPluginsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowStorageVault(ctx *gen.ShowStorageVaultContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowRepositories(ctx *gen.ShowRepositoriesContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowEncryptKeys(ctx *gen.ShowEncryptKeysContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowCreateTable(ctx *gen.ShowCreateTableContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowProcessList(ctx *gen.ShowProcessListContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowPartitions(ctx *gen.ShowPartitionsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowRestore(ctx *gen.ShowRestoreContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowRoles(ctx *gen.ShowRolesContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowPartitionId(ctx *gen.ShowPartitionIdContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowPrivileges(ctx *gen.ShowPrivilegesContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowProc(ctx *gen.ShowProcContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowSmallFiles(ctx *gen.ShowSmallFilesContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowStorageEngines(ctx *gen.ShowStorageEnginesContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowCreateCatalog(ctx *gen.ShowCreateCatalogContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowCatalog(ctx *gen.ShowCatalogContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowCatalogs(ctx *gen.ShowCatalogsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowUserProperties(ctx *gen.ShowUserPropertiesContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowAllProperties(ctx *gen.ShowAllPropertiesContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowCollation(ctx *gen.ShowCollationContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowRowPolicy(ctx *gen.ShowRowPolicyContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowStoragePolicy(ctx *gen.ShowStoragePolicyContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowSqlBlockRule(ctx *gen.ShowSqlBlockRuleContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowCreateView(ctx *gen.ShowCreateViewContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowDataTypes(ctx *gen.ShowDataTypesContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowData(ctx *gen.ShowDataContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowCreateMaterializedView(ctx *gen.ShowCreateMaterializedViewContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowWarningErrors(ctx *gen.ShowWarningErrorsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowWarningErrorCount(ctx *gen.ShowWarningErrorCountContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowBackends(ctx *gen.ShowBackendsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowStages(ctx *gen.ShowStagesContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowReplicaDistribution(ctx *gen.ShowReplicaDistributionContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowResources(ctx *gen.ShowResourcesContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowLoad(ctx *gen.ShowLoadContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowLoadWarings(ctx *gen.ShowLoadWaringsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowTriggers(ctx *gen.ShowTriggersContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowDiagnoseTablet(ctx *gen.ShowDiagnoseTabletContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowOpenTables(ctx *gen.ShowOpenTablesContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowFrontends(ctx *gen.ShowFrontendsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowDatabaseId(ctx *gen.ShowDatabaseIdContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowColumns(ctx *gen.ShowColumnsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowTableId(ctx *gen.ShowTableIdContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowTrash(ctx *gen.ShowTrashContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowTypeCast(ctx *gen.ShowTypeCastContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowClusters(ctx *gen.ShowClustersContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowStatus(ctx *gen.ShowStatusContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowWhitelist(ctx *gen.ShowWhitelistContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowTabletsBelong(ctx *gen.ShowTabletsBelongContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowDataSkew(ctx *gen.ShowDataSkewContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowTableCreation(ctx *gen.ShowTableCreationContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowTabletStorageFormat(ctx *gen.ShowTabletStorageFormatContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowQueryProfile(ctx *gen.ShowQueryProfileContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowConvertLsc(ctx *gen.ShowConvertLscContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowTables(ctx *gen.ShowTablesContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowViews(ctx *gen.ShowViewsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowTableStatus(ctx *gen.ShowTableStatusContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowDatabases(ctx *gen.ShowDatabasesContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowTabletsFromTable(ctx *gen.ShowTabletsFromTableContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowCatalogRecycleBin(ctx *gen.ShowCatalogRecycleBinContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowTabletId(ctx *gen.ShowTabletIdContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowDictionaries(ctx *gen.ShowDictionariesContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowTransaction(ctx *gen.ShowTransactionContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowReplicaStatus(ctx *gen.ShowReplicaStatusContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowWorkloadGroups(ctx *gen.ShowWorkloadGroupsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowCopy(ctx *gen.ShowCopyContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowQueryStats(ctx *gen.ShowQueryStatsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowIndex(ctx *gen.ShowIndexContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowWarmUpJob(ctx *gen.ShowWarmUpJobContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSync(ctx *gen.SyncContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCreateRoutineLoadAlias(ctx *gen.CreateRoutineLoadAliasContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowCreateRoutineLoad(ctx *gen.ShowCreateRoutineLoadContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitPauseRoutineLoad(ctx *gen.PauseRoutineLoadContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitPauseAllRoutineLoad(ctx *gen.PauseAllRoutineLoadContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitResumeRoutineLoad(ctx *gen.ResumeRoutineLoadContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitResumeAllRoutineLoad(ctx *gen.ResumeAllRoutineLoadContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitStopRoutineLoad(ctx *gen.StopRoutineLoadContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowRoutineLoad(ctx *gen.ShowRoutineLoadContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowRoutineLoadTask(ctx *gen.ShowRoutineLoadTaskContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowIndexAnalyzer(ctx *gen.ShowIndexAnalyzerContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowIndexTokenizer(ctx *gen.ShowIndexTokenizerContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowIndexTokenFilter(ctx *gen.ShowIndexTokenFilterContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitKillConnection(ctx *gen.KillConnectionContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitKillQuery(ctx *gen.KillQueryContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitHelp(ctx *gen.HelpContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitUnlockTables(ctx *gen.UnlockTablesContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitInstallPlugin(ctx *gen.InstallPluginContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitUninstallPlugin(ctx *gen.UninstallPluginContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitLockTables(ctx *gen.LockTablesContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitRestore(ctx *gen.RestoreContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitWarmUpCluster(ctx *gen.WarmUpClusterContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitBackup(ctx *gen.BackupContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitUnsupportedStartTransaction(ctx *gen.UnsupportedStartTransactionContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitWarmUpItem(ctx *gen.WarmUpItemContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitLockTable(ctx *gen.LockTableContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCreateRoutineLoad(ctx *gen.CreateRoutineLoadContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitMysqlLoad(ctx *gen.MysqlLoadContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowCreateLoad(ctx *gen.ShowCreateLoadContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSeparator(ctx *gen.SeparatorContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitImportColumns(ctx *gen.ImportColumnsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitImportPrecedingFilter(ctx *gen.ImportPrecedingFilterContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitImportWhere(ctx *gen.ImportWhereContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitImportDeleteOn(ctx *gen.ImportDeleteOnContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitImportSequence(ctx *gen.ImportSequenceContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitImportPartitions(ctx *gen.ImportPartitionsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitImportSequenceStatement(ctx *gen.ImportSequenceStatementContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitImportDeleteOnStatement(ctx *gen.ImportDeleteOnStatementContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitImportWhereStatement(ctx *gen.ImportWhereStatementContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitImportPrecedingFilterStatement(ctx *gen.ImportPrecedingFilterStatementContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitImportColumnsStatement(ctx *gen.ImportColumnsStatementContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitImportColumnDesc(ctx *gen.ImportColumnDescContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitRefreshCatalog(ctx *gen.RefreshCatalogContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitRefreshDatabase(ctx *gen.RefreshDatabaseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitRefreshTable(ctx *gen.RefreshTableContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitRefreshDictionary(ctx *gen.RefreshDictionaryContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitRefreshLdap(ctx *gen.RefreshLdapContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCleanAllProfile(ctx *gen.CleanAllProfileContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCleanLabel(ctx *gen.CleanLabelContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCleanQueryStats(ctx *gen.CleanQueryStatsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCleanAllQueryStats(ctx *gen.CleanAllQueryStatsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCancelLoad(ctx *gen.CancelLoadContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCancelExport(ctx *gen.CancelExportContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCancelWarmUpJob(ctx *gen.CancelWarmUpJobContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCancelDecommisionBackend(ctx *gen.CancelDecommisionBackendContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCancelBackup(ctx *gen.CancelBackupContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCancelRestore(ctx *gen.CancelRestoreContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCancelBuildIndex(ctx *gen.CancelBuildIndexContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCancelAlterTable(ctx *gen.CancelAlterTableContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAdminShowReplicaDistribution(ctx *gen.AdminShowReplicaDistributionContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAdminRebalanceDisk(ctx *gen.AdminRebalanceDiskContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAdminCancelRebalanceDisk(ctx *gen.AdminCancelRebalanceDiskContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAdminDiagnoseTablet(ctx *gen.AdminDiagnoseTabletContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAdminShowReplicaStatus(ctx *gen.AdminShowReplicaStatusContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAdminCompactTable(ctx *gen.AdminCompactTableContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAdminCheckTablets(ctx *gen.AdminCheckTabletsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAdminShowTabletStorageFormat(ctx *gen.AdminShowTabletStorageFormatContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAdminSetFrontendConfig(ctx *gen.AdminSetFrontendConfigContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAdminCleanTrash(ctx *gen.AdminCleanTrashContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAdminSetReplicaVersion(ctx *gen.AdminSetReplicaVersionContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAdminSetTableStatus(ctx *gen.AdminSetTableStatusContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAdminSetReplicaStatus(ctx *gen.AdminSetReplicaStatusContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAdminRepairTable(ctx *gen.AdminRepairTableContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAdminCancelRepairTable(ctx *gen.AdminCancelRepairTableContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAdminCopyTablet(ctx *gen.AdminCopyTabletContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitRecoverDatabase(ctx *gen.RecoverDatabaseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitRecoverTable(ctx *gen.RecoverTableContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitRecoverPartition(ctx *gen.RecoverPartitionContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAdminSetPartitionVersion(ctx *gen.AdminSetPartitionVersionContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitBaseTableRef(ctx *gen.BaseTableRefContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitWildWhere(ctx *gen.WildWhereContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitTransactionBegin(ctx *gen.TransactionBeginContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitTranscationCommit(ctx *gen.TranscationCommitContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitTransactionRollback(ctx *gen.TransactionRollbackContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitGrantTablePrivilege(ctx *gen.GrantTablePrivilegeContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitGrantResourcePrivilege(ctx *gen.GrantResourcePrivilegeContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitGrantRole(ctx *gen.GrantRoleContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitRevokeRole(ctx *gen.RevokeRoleContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitRevokeResourcePrivilege(ctx *gen.RevokeResourcePrivilegeContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitRevokeTablePrivilege(ctx *gen.RevokeTablePrivilegeContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitPrivilege(ctx *gen.PrivilegeContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitPrivilegeList(ctx *gen.PrivilegeListContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAddBackendClause(ctx *gen.AddBackendClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDropBackendClause(ctx *gen.DropBackendClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDecommissionBackendClause(ctx *gen.DecommissionBackendClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAddObserverClause(ctx *gen.AddObserverClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDropObserverClause(ctx *gen.DropObserverClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAddFollowerClause(ctx *gen.AddFollowerClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDropFollowerClause(ctx *gen.DropFollowerClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAddBrokerClause(ctx *gen.AddBrokerClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDropBrokerClause(ctx *gen.DropBrokerClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDropAllBrokerClause(ctx *gen.DropAllBrokerClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAlterLoadErrorUrlClause(ctx *gen.AlterLoadErrorUrlClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitModifyBackendClause(ctx *gen.ModifyBackendClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitModifyFrontendOrBackendHostNameClause(ctx *gen.ModifyFrontendOrBackendHostNameClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDropRollupClause(ctx *gen.DropRollupClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAddRollupClause(ctx *gen.AddRollupClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAddColumnClause(ctx *gen.AddColumnClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAddColumnsClause(ctx *gen.AddColumnsClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDropColumnClause(ctx *gen.DropColumnClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitModifyColumnClause(ctx *gen.ModifyColumnClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitReorderColumnsClause(ctx *gen.ReorderColumnsClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAddPartitionClause(ctx *gen.AddPartitionClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDropPartitionClause(ctx *gen.DropPartitionClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitModifyPartitionClause(ctx *gen.ModifyPartitionClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitReplacePartitionClause(ctx *gen.ReplacePartitionClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitReplaceTableClause(ctx *gen.ReplaceTableClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitRenameClause(ctx *gen.RenameClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitRenameRollupClause(ctx *gen.RenameRollupClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitRenamePartitionClause(ctx *gen.RenamePartitionClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitRenameColumnClause(ctx *gen.RenameColumnClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAddIndexClause(ctx *gen.AddIndexClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDropIndexClause(ctx *gen.DropIndexClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitEnableFeatureClause(ctx *gen.EnableFeatureClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitModifyDistributionClause(ctx *gen.ModifyDistributionClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitModifyTableCommentClause(ctx *gen.ModifyTableCommentClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitModifyColumnCommentClause(ctx *gen.ModifyColumnCommentClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitModifyEngineClause(ctx *gen.ModifyEngineClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAlterMultiPartitionClause(ctx *gen.AlterMultiPartitionClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCreateOrReplaceTagClauses(ctx *gen.CreateOrReplaceTagClausesContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCreateOrReplaceBranchClauses(ctx *gen.CreateOrReplaceBranchClausesContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDropBranchClauses(ctx *gen.DropBranchClausesContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDropTagClauses(ctx *gen.DropTagClausesContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCreateOrReplaceTagClause(ctx *gen.CreateOrReplaceTagClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCreateOrReplaceBranchClause(ctx *gen.CreateOrReplaceBranchClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitTagOptions(ctx *gen.TagOptionsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitBranchOptions(ctx *gen.BranchOptionsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitRetainTime(ctx *gen.RetainTimeContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitRetentionSnapshot(ctx *gen.RetentionSnapshotContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitMinSnapshotsToKeep(ctx *gen.MinSnapshotsToKeepContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitTimeValueWithUnit(ctx *gen.TimeValueWithUnitContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDropBranchClause(ctx *gen.DropBranchClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDropTagClause(ctx *gen.DropTagClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitColumnPosition(ctx *gen.ColumnPositionContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitToRollup(ctx *gen.ToRollupContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitFromRollup(ctx *gen.FromRollupContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowAnalyze(ctx *gen.ShowAnalyzeContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowQueuedAnalyzeJobs(ctx *gen.ShowQueuedAnalyzeJobsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowColumnHistogramStats(ctx *gen.ShowColumnHistogramStatsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAnalyzeDatabase(ctx *gen.AnalyzeDatabaseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAnalyzeTable(ctx *gen.AnalyzeTableContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAlterTableStats(ctx *gen.AlterTableStatsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAlterColumnStats(ctx *gen.AlterColumnStatsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowIndexStats(ctx *gen.ShowIndexStatsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDropStats(ctx *gen.DropStatsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDropCachedStats(ctx *gen.DropCachedStatsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDropExpiredStats(ctx *gen.DropExpiredStatsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitKillAnalyzeJob(ctx *gen.KillAnalyzeJobContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDropAnalyzeJob(ctx *gen.DropAnalyzeJobContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowTableStats(ctx *gen.ShowTableStatsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowColumnStats(ctx *gen.ShowColumnStatsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitShowAnalyzeTask(ctx *gen.ShowAnalyzeTaskContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAnalyzeProperties(ctx *gen.AnalyzePropertiesContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitWorkloadPolicyActions(ctx *gen.WorkloadPolicyActionsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitWorkloadPolicyAction(ctx *gen.WorkloadPolicyActionContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitWorkloadPolicyConditions(ctx *gen.WorkloadPolicyConditionsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitWorkloadPolicyCondition(ctx *gen.WorkloadPolicyConditionContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitStorageBackend(ctx *gen.StorageBackendContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitPasswordOption(ctx *gen.PasswordOptionContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitFunctionArguments(ctx *gen.FunctionArgumentsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDataTypeList(ctx *gen.DataTypeListContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSetOptions(ctx *gen.SetOptionsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSetDefaultStorageVault(ctx *gen.SetDefaultStorageVaultContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSetUserProperties(ctx *gen.SetUserPropertiesContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSetTransaction(ctx *gen.SetTransactionContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSetVariableWithType(ctx *gen.SetVariableWithTypeContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSetNames(ctx *gen.SetNamesContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSetCharset(ctx *gen.SetCharsetContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSetCollate(ctx *gen.SetCollateContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSetPassword(ctx *gen.SetPasswordContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSetLdapAdminPassword(ctx *gen.SetLdapAdminPasswordContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSetVariableWithoutType(ctx *gen.SetVariableWithoutTypeContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSetSystemVariable(ctx *gen.SetSystemVariableContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSetUserVariable(ctx *gen.SetUserVariableContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitTransactionAccessMode(ctx *gen.TransactionAccessModeContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitIsolationLevel(ctx *gen.IsolationLevelContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSupportedUnsetStatement(ctx *gen.SupportedUnsetStatementContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSwitchCatalog(ctx *gen.SwitchCatalogContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitUseDatabase(ctx *gen.UseDatabaseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitUseCloudCluster(ctx *gen.UseCloudClusterContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitStageAndPattern(ctx *gen.StageAndPatternContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDescribeTableValuedFunction(ctx *gen.DescribeTableValuedFunctionContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDescribeTableAll(ctx *gen.DescribeTableAllContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDescribeTable(ctx *gen.DescribeTableContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDescribeDictionary(ctx *gen.DescribeDictionaryContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitConstraint(ctx *gen.ConstraintContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitPartitionSpec(ctx *gen.PartitionSpecContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitPartitionTable(ctx *gen.PartitionTableContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitIdentityOrFunctionList(ctx *gen.IdentityOrFunctionListContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitIdentityOrFunction(ctx *gen.IdentityOrFunctionContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDataDesc(ctx *gen.DataDescContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitStatementScope(ctx *gen.StatementScopeContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitBuildMode(ctx *gen.BuildModeContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitRefreshTrigger(ctx *gen.RefreshTriggerContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitRefreshSchedule(ctx *gen.RefreshScheduleContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitRefreshMethod(ctx *gen.RefreshMethodContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitMvPartition(ctx *gen.MvPartitionContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitIdentifierOrText(ctx *gen.IdentifierOrTextContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitIdentifierOrTextOrAsterisk(ctx *gen.IdentifierOrTextOrAsteriskContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitMultipartIdentifierOrAsterisk(ctx *gen.MultipartIdentifierOrAsteriskContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitIdentifierOrAsterisk(ctx *gen.IdentifierOrAsteriskContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitUserIdentify(ctx *gen.UserIdentifyContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitGrantUserIdentify(ctx *gen.GrantUserIdentifyContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitExplain(ctx *gen.ExplainContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitExplainCommand(ctx *gen.ExplainCommandContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitPlanType(ctx *gen.PlanTypeContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitReplayCommand(ctx *gen.ReplayCommandContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitReplayType(ctx *gen.ReplayTypeContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitMergeType(ctx *gen.MergeTypeContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitPreFilterClause(ctx *gen.PreFilterClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDeleteOnClause(ctx *gen.DeleteOnClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSequenceColClause(ctx *gen.SequenceColClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitColFromPath(ctx *gen.ColFromPathContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitColMappingList(ctx *gen.ColMappingListContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitMappingExpr(ctx *gen.MappingExprContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitWithRemoteStorageSystem(ctx *gen.WithRemoteStorageSystemContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitResourceDesc(ctx *gen.ResourceDescContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitMysqlDataDesc(ctx *gen.MysqlDataDescContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSkipLines(ctx *gen.SkipLinesContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitOutFileClause(ctx *gen.OutFileClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitQuery(ctx *gen.QueryContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitQueryTermDefault(ctx *gen.QueryTermDefaultContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSetOperation(ctx *gen.SetOperationContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSetQuantifier(ctx *gen.SetQuantifierContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitQueryPrimaryDefault(ctx *gen.QueryPrimaryDefaultContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSubquery(ctx *gen.SubqueryContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitValuesTable(ctx *gen.ValuesTableContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitRegularQuerySpecification(ctx *gen.RegularQuerySpecificationContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCte(ctx *gen.CteContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAliasQuery(ctx *gen.AliasQueryContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitColumnAliases(ctx *gen.ColumnAliasesContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSelectClause(ctx *gen.SelectClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSelectColumnClause(ctx *gen.SelectColumnClauseContext) interface{} {
	// 先访问子节点，确保字段名已被修改
	result := v.VisitChildren(ctx)
	v.Select = ctx.GetText()
	return result
}

func (v *DorisVisitor) VisitWhereClause(ctx *gen.WhereClauseContext) interface{} {
	// 先访问子节点，确保字段名已被修改
	result := v.VisitChildren(ctx)
	v.Where = ctx.GetText()
	return result
}

func (v *DorisVisitor) VisitFromClause(ctx *gen.FromClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitIntoClause(ctx *gen.IntoClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitBulkCollectClause(ctx *gen.BulkCollectClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitTableRow(ctx *gen.TableRowContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitRelations(ctx *gen.RelationsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitRelation(ctx *gen.RelationContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitJoinRelation(ctx *gen.JoinRelationContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitBracketDistributeType(ctx *gen.BracketDistributeTypeContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCommentDistributeType(ctx *gen.CommentDistributeTypeContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitBracketRelationHint(ctx *gen.BracketRelationHintContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCommentRelationHint(ctx *gen.CommentRelationHintContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAggClause(ctx *gen.AggClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitGroupingElement(ctx *gen.GroupingElementContext) interface{} {
	// 先访问子节点，确保字段名已被修改
	result := v.VisitChildren(ctx)
	v.Group = ctx.GetText()
	return result
}

func (v *DorisVisitor) VisitGroupingSet(ctx *gen.GroupingSetContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitHavingClause(ctx *gen.HavingClauseContext) interface{} {
	// 先访问子节点，确保字段名已被修改
	result := v.VisitChildren(ctx)
	v.Having = ctx.GetText()
	return result
}

func (v *DorisVisitor) VisitQualifyClause(ctx *gen.QualifyClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSelectHint(ctx *gen.SelectHintContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitHintStatement(ctx *gen.HintStatementContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitHintAssignment(ctx *gen.HintAssignmentContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitUpdateAssignment(ctx *gen.UpdateAssignmentContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitUpdateAssignmentSeq(ctx *gen.UpdateAssignmentSeqContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitLateralView(ctx *gen.LateralViewContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitQueryOrganization(ctx *gen.QueryOrganizationContext) interface{} {
	a := ctx.GetText()
	fmt.Println(a)
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSortClause(ctx *gen.SortClauseContext) interface{} {
	// 先访问子节点，确保字段名已被修改
	result := v.VisitChildren(ctx)
	v.Order = ctx.GetText()
	return result
}

func (v *DorisVisitor) VisitSortItem(ctx *gen.SortItemContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitLimitClause(ctx *gen.LimitClauseContext) interface{} {
	v.LimitOffset = ctx.GetText()
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitPartitionClause(ctx *gen.PartitionClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitJoinType(ctx *gen.JoinTypeContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitJoinCriteria(ctx *gen.JoinCriteriaContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitIdentifierList(ctx *gen.IdentifierListContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitIdentifierSeq(ctx *gen.IdentifierSeqContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitOptScanParams(ctx *gen.OptScanParamsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitTableName(ctx *gen.TableNameContext) interface{} {
	// 先访问子节点，确保字段名已被修改
	result := v.VisitChildren(ctx)
	v.Table = ctx.GetText()
	return result
}

func (v *DorisVisitor) VisitAliasedQuery(ctx *gen.AliasedQueryContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitTableValuedFunction(ctx *gen.TableValuedFunctionContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitRelationList(ctx *gen.RelationListContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitMaterializedViewName(ctx *gen.MaterializedViewNameContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitPropertyClause(ctx *gen.PropertyClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitPropertyItemList(ctx *gen.PropertyItemListContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitPropertyItem(ctx *gen.PropertyItemContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitPropertyKey(ctx *gen.PropertyKeyContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitPropertyValue(ctx *gen.PropertyValueContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitTableAlias(ctx *gen.TableAliasContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitMultipartIdentifier(ctx *gen.MultipartIdentifierContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSimpleColumnDefs(ctx *gen.SimpleColumnDefsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSimpleColumnDef(ctx *gen.SimpleColumnDefContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitColumnDefs(ctx *gen.ColumnDefsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitColumnDef(ctx *gen.ColumnDefContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitIndexDefs(ctx *gen.IndexDefsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitIndexDef(ctx *gen.IndexDefContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitPartitionsDef(ctx *gen.PartitionsDefContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitPartitionDef(ctx *gen.PartitionDefContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitLessThanPartitionDef(ctx *gen.LessThanPartitionDefContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitFixedPartitionDef(ctx *gen.FixedPartitionDefContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitStepPartitionDef(ctx *gen.StepPartitionDefContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitInPartitionDef(ctx *gen.InPartitionDefContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitPartitionValueList(ctx *gen.PartitionValueListContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitPartitionValueDef(ctx *gen.PartitionValueDefContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitRollupDefs(ctx *gen.RollupDefsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitRollupDef(ctx *gen.RollupDefContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAggTypeDef(ctx *gen.AggTypeDefContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitTabletList(ctx *gen.TabletListContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitInlineTable(ctx *gen.InlineTableContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitNamedExpression(ctx *gen.NamedExpressionContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitNamedExpressionSeq(ctx *gen.NamedExpressionSeqContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitExpression(ctx *gen.ExpressionContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitLambdaExpression(ctx *gen.LambdaExpressionContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitExist(ctx *gen.ExistContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitLogicalNot(ctx *gen.LogicalNotContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitPredicated(ctx *gen.PredicatedContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitIsnull(ctx *gen.IsnullContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitIs_not_null_pred(ctx *gen.Is_not_null_predContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitLogicalBinary(ctx *gen.LogicalBinaryContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDoublePipes(ctx *gen.DoublePipesContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitRowConstructor(ctx *gen.RowConstructorContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitRowConstructorItem(ctx *gen.RowConstructorItemContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitPredicate(ctx *gen.PredicateContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitValueExpressionDefault(ctx *gen.ValueExpressionDefaultContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitComparison(ctx *gen.ComparisonContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitArithmeticBinary(ctx *gen.ArithmeticBinaryContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitArithmeticUnary(ctx *gen.ArithmeticUnaryContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDereference(ctx *gen.DereferenceContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCurrentDate(ctx *gen.CurrentDateContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCast(ctx *gen.CastContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitParenthesizedExpression(ctx *gen.ParenthesizedExpressionContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitUserVariable(ctx *gen.UserVariableContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitElementAt(ctx *gen.ElementAtContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitLocalTimestamp(ctx *gen.LocalTimestampContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCharFunction(ctx *gen.CharFunctionContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitIntervalLiteral(ctx *gen.IntervalLiteralContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSimpleCase(ctx *gen.SimpleCaseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitColumnReference(ctx *gen.ColumnReferenceContext) interface{} {
	originalName := ctx.GetText()

	// 别名修改
	if v.opt.DimensionTransform != nil {
		alias := v.opt.DimensionTransform(originalName)
		// 如果别名修改成功
		if alias != originalName {
			// 如果有别名映射则修改token
			start := ctx.GetStart().GetTokenIndex()
			stop := ctx.GetStop().GetTokenIndex()

			// 替换token流中的内容
			for i := start; i <= stop; i++ {
				tokenText := v.Tokens.Get(i).GetText()
				if tokenText == originalName {
					v.Tokens.Get(i).SetText(alias)
					log.Infof(v.Ctx, "修改字段引用: %s → %s", originalName, alias)
				}
			}
		}
	}

	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitStar(ctx *gen.StarContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSessionUser(ctx *gen.SessionUserContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitConvertType(ctx *gen.ConvertTypeContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitConvertCharSet(ctx *gen.ConvertCharSetContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSubqueryExpression(ctx *gen.SubqueryExpressionContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitEncryptKey(ctx *gen.EncryptKeyContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCurrentTime(ctx *gen.CurrentTimeContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitLocalTime(ctx *gen.LocalTimeContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSystemVariable(ctx *gen.SystemVariableContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCollate(ctx *gen.CollateContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCurrentUser(ctx *gen.CurrentUserContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitConstantDefault(ctx *gen.ConstantDefaultContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitExtract(ctx *gen.ExtractContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCurrentTimestamp(ctx *gen.CurrentTimestampContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitFunctionCall(ctx *gen.FunctionCallContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitArraySlice(ctx *gen.ArraySliceContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSearchedCase(ctx *gen.SearchedCaseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitExcept(ctx *gen.ExceptContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitReplace(ctx *gen.ReplaceContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCastDataType(ctx *gen.CastDataTypeContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitFunctionCallExpression(ctx *gen.FunctionCallExpressionContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitFunctionIdentifier(ctx *gen.FunctionIdentifierContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitFunctionNameIdentifier(ctx *gen.FunctionNameIdentifierContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitWindowSpec(ctx *gen.WindowSpecContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitWindowFrame(ctx *gen.WindowFrameContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitFrameUnits(ctx *gen.FrameUnitsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitFrameBoundary(ctx *gen.FrameBoundaryContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitQualifiedName(ctx *gen.QualifiedNameContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSpecifiedPartition(ctx *gen.SpecifiedPartitionContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitNullLiteral(ctx *gen.NullLiteralContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitTypeConstructor(ctx *gen.TypeConstructorContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitNumericLiteral(ctx *gen.NumericLiteralContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitBooleanLiteral(ctx *gen.BooleanLiteralContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitStringLiteral(ctx *gen.StringLiteralContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitArrayLiteral(ctx *gen.ArrayLiteralContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitMapLiteral(ctx *gen.MapLiteralContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitStructLiteral(ctx *gen.StructLiteralContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitPlaceholder(ctx *gen.PlaceholderContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitComparisonOperator(ctx *gen.ComparisonOperatorContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitBooleanValue(ctx *gen.BooleanValueContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitWhenClause(ctx *gen.WhenClauseContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitInterval(ctx *gen.IntervalContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitUnitIdentifier(ctx *gen.UnitIdentifierContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDataTypeWithNullable(ctx *gen.DataTypeWithNullableContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitComplexDataType(ctx *gen.ComplexDataTypeContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitAggStateDataType(ctx *gen.AggStateDataTypeContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitPrimitiveDataType(ctx *gen.PrimitiveDataTypeContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitPrimitiveColType(ctx *gen.PrimitiveColTypeContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitComplexColTypeList(ctx *gen.ComplexColTypeListContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitComplexColType(ctx *gen.ComplexColTypeContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitCommentSpec(ctx *gen.CommentSpecContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSample(ctx *gen.SampleContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSampleByPercentile(ctx *gen.SampleByPercentileContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitSampleByRows(ctx *gen.SampleByRowsContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitTableSnapshot(ctx *gen.TableSnapshotContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitErrorCapturingIdentifier(ctx *gen.ErrorCapturingIdentifierContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitErrorIdent(ctx *gen.ErrorIdentContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitRealIdent(ctx *gen.RealIdentContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitIdentifier(ctx *gen.IdentifierContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitUnquotedIdentifier(ctx *gen.UnquotedIdentifierContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitQuotedIdentifierAlternative(ctx *gen.QuotedIdentifierAlternativeContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitQuotedIdentifier(ctx *gen.QuotedIdentifierContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitIntegerLiteral(ctx *gen.IntegerLiteralContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitDecimalLiteral(ctx *gen.DecimalLiteralContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

func (v *DorisVisitor) VisitNonReserved(ctx *gen.NonReservedContext) interface{} {
	result := v.VisitChildren(ctx)
	return result
}

// VisitChildren 安全访问子节点
func (v *DorisVisitor) VisitChildren(node antlr.RuleNode) interface{} {
	children := node.GetChildren()
	for _, child := range children {
		if parseTree, ok := child.(antlr.ParseTree); ok {
			switch parseTree.(type) {
			case *antlr.ErrorNodeImpl:
				v.Err = fmt.Errorf("sql: %s, parse error: %+v", v.OriginalSQL, parseTree)
				return nil
			default:
				log.Debugf(v.Ctx, "执行子节点 %T %s", parseTree, parseTree.GetText())
				parseTree.Accept(v)
			}
		} else {
			v.Err = fmt.Errorf("无效的子节点类型: %T", child)
		}
	}
	return nil
}

func (v *DorisVisitor) trimPrefix(val, key string) string {
	val = strings.TrimPrefix(val, strings.ToUpper(key))
	val = strings.TrimPrefix(val, strings.ToLower(key))
	return val
}

func (v *DorisVisitor) splitByKey(val, key string) []string {
	if val == "" {
		return nil
	}
	val = strings.TrimPrefix(val, key)
	return strings.Split(val, ",")
}

// GetSelects 获取 select 列表
func (v *DorisVisitor) GetSelects() []string {
	if v.Select == "" {
		return nil
	}
	return v.splitByKey(v.Select, "select")
}

// GetWhere 获取 where
func (v *DorisVisitor) GetWhere() string {
	return v.trimPrefix(v.Where, "where")
}

// GetOrders 获取 order 列表
func (v *DorisVisitor) GetOrders() []string {
	if v.Order == "" {
		return nil
	}
	return v.splitByKey(v.Order, "order")
}

// GetGroups 获取 Group 列表
func (v *DorisVisitor) GetGroups() []string {
	if v.Group == "" {
		return nil
	}
	return v.splitByKey(v.Group, "group")
}

// GetHavings 获取 Having 列表
func (v *DorisVisitor) GetHavings() []string {
	if v.Having == "" {
		return nil
	}
	return v.splitByKey(v.Having, "having")
}

// GetTable 获取 table
func (v *DorisVisitor) GetTable() string {
	return v.Table
}

// GetLimitOffset 获取 limit 和
func (v *DorisVisitor) GetLimitOffset() (limit, offset string) {
	// 使用忽略大小写的正则表达式匹配 LIMIT 和 OFFSET
	re := regexp.MustCompile(`(?i)(limit\s*(\d+))?\s*(offset\s*(\d+))?`)
	matches := re.FindStringSubmatch(v.LimitOffset)

	if len(matches) > 2 && matches[2] != "" {
		limit = strings.TrimSpace(matches[2])
	}
	if len(matches) > 4 && matches[4] != "" {
		offset = strings.TrimSpace(matches[4])
	}
	// 解析 limit1000offset999
	return limit, offset
}

// GetModifiedSQL 获取最终修改后的SQL
func (v *DorisVisitor) GetModifiedSQL() string {
	if v.Tokens == nil {
		return v.OriginalSQL
	}

	var builder strings.Builder
	for i := 0; i < v.Tokens.Size(); i++ {
		token := v.Tokens.Get(i)
		if token.GetTokenType() == antlr.TokenEOF {
			break
		}

		if token.GetChannel() == antlr.TokenDefaultChannel {
			builder.WriteString(token.GetText())
			if token.GetTokenType() != antlr.TokenEOF {
				builder.WriteString(" ")
			}
		}
	}

	v.ModifiedSQL = builder.String()
	return v.ModifiedSQL
}

type DorisVisitorOption struct {
	DimensionTransform func(s string) string
}

func (v *DorisVisitor) WithOptions(opt DorisVisitorOption) {
	v.opt = opt
}

// NewDorisVisitor 创建带Token流的Visitor
func NewDorisVisitor(ctx context.Context, input string) *DorisVisitor {

	// 创建输入流
	is := antlr.NewInputStream(input)

	// 创建词法分析器
	lexer := gen.NewDorisLexer(is)

	// 创建Token流
	tokens := antlr.NewCommonTokenStream(lexer, antlr.TokenDefaultChannel)

	return &DorisVisitor{
		Ctx:         ctx,
		OriginalSQL: input,
		Tokens:      tokens,
	}
}
