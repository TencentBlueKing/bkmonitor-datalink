// Code generated from DorisParser.g4 by ANTLR 4.13.1. DO NOT EDIT.

package gen // DorisParser
import "github.com/antlr4-go/antlr/v4"

type BaseDorisParserVisitor struct {
	*antlr.BaseParseTreeVisitor
}

func (v *BaseDorisParserVisitor) VisitMultiStatements(ctx *MultiStatementsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSingleStatement(ctx *SingleStatementContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitStatementBaseAlias(ctx *StatementBaseAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCallProcedure(ctx *CallProcedureContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateProcedure(ctx *CreateProcedureContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropProcedure(ctx *DropProcedureContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowProcedureStatus(ctx *ShowProcedureStatusContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowCreateProcedure(ctx *ShowCreateProcedureContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowConfig(ctx *ShowConfigContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitStatementDefault(ctx *StatementDefaultContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSupportedDmlStatementAlias(ctx *SupportedDmlStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSupportedCreateStatementAlias(ctx *SupportedCreateStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSupportedAlterStatementAlias(ctx *SupportedAlterStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitMaterializedViewStatementAlias(ctx *MaterializedViewStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSupportedJobStatementAlias(ctx *SupportedJobStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitConstraintStatementAlias(ctx *ConstraintStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSupportedCleanStatementAlias(ctx *SupportedCleanStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSupportedDescribeStatementAlias(ctx *SupportedDescribeStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSupportedDropStatementAlias(ctx *SupportedDropStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSupportedSetStatementAlias(ctx *SupportedSetStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSupportedUnsetStatementAlias(ctx *SupportedUnsetStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSupportedRefreshStatementAlias(ctx *SupportedRefreshStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSupportedShowStatementAlias(ctx *SupportedShowStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSupportedLoadStatementAlias(ctx *SupportedLoadStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSupportedCancelStatementAlias(ctx *SupportedCancelStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSupportedRecoverStatementAlias(ctx *SupportedRecoverStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSupportedAdminStatementAlias(ctx *SupportedAdminStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSupportedUseStatementAlias(ctx *SupportedUseStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSupportedOtherStatementAlias(ctx *SupportedOtherStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSupportedKillStatementAlias(ctx *SupportedKillStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSupportedStatsStatementAlias(ctx *SupportedStatsStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSupportedTransactionStatementAlias(ctx *SupportedTransactionStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSupportedGrantRevokeStatementAlias(ctx *SupportedGrantRevokeStatementAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitUnsupported(ctx *UnsupportedContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitUnsupportedStatement(ctx *UnsupportedStatementContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateMTMV(ctx *CreateMTMVContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRefreshMTMV(ctx *RefreshMTMVContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterMTMV(ctx *AlterMTMVContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropMTMV(ctx *DropMTMVContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPauseMTMV(ctx *PauseMTMVContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitResumeMTMV(ctx *ResumeMTMVContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCancelMTMVTask(ctx *CancelMTMVTaskContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowCreateMTMV(ctx *ShowCreateMTMVContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateScheduledJob(ctx *CreateScheduledJobContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPauseJob(ctx *PauseJobContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropJob(ctx *DropJobContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitResumeJob(ctx *ResumeJobContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCancelJobTask(ctx *CancelJobTaskContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAddConstraint(ctx *AddConstraintContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropConstraint(ctx *DropConstraintContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowConstraint(ctx *ShowConstraintContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitInsertTable(ctx *InsertTableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitUpdate(ctx *UpdateContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDelete(ctx *DeleteContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitLoad(ctx *LoadContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitExport(ctx *ExportContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitReplay(ctx *ReplayContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCopyInto(ctx *CopyIntoContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitTruncateTable(ctx *TruncateTableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateTable(ctx *CreateTableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateView(ctx *CreateViewContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateFile(ctx *CreateFileContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateTableLike(ctx *CreateTableLikeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateRole(ctx *CreateRoleContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateWorkloadGroup(ctx *CreateWorkloadGroupContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateCatalog(ctx *CreateCatalogContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateRowPolicy(ctx *CreateRowPolicyContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateStoragePolicy(ctx *CreateStoragePolicyContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitBuildIndex(ctx *BuildIndexContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateIndex(ctx *CreateIndexContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateWorkloadPolicy(ctx *CreateWorkloadPolicyContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateSqlBlockRule(ctx *CreateSqlBlockRuleContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateEncryptkey(ctx *CreateEncryptkeyContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateUserDefineFunction(ctx *CreateUserDefineFunctionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateAliasFunction(ctx *CreateAliasFunctionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateUser(ctx *CreateUserContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateDatabase(ctx *CreateDatabaseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateRepository(ctx *CreateRepositoryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateResource(ctx *CreateResourceContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateDictionary(ctx *CreateDictionaryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateStage(ctx *CreateStageContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateStorageVault(ctx *CreateStorageVaultContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateIndexAnalyzer(ctx *CreateIndexAnalyzerContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateIndexTokenizer(ctx *CreateIndexTokenizerContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateIndexTokenFilter(ctx *CreateIndexTokenFilterContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDictionaryColumnDefs(ctx *DictionaryColumnDefsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDictionaryColumnDef(ctx *DictionaryColumnDefContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterSystem(ctx *AlterSystemContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterView(ctx *AlterViewContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterCatalogRename(ctx *AlterCatalogRenameContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterRole(ctx *AlterRoleContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterStorageVault(ctx *AlterStorageVaultContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterWorkloadGroup(ctx *AlterWorkloadGroupContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterCatalogProperties(ctx *AlterCatalogPropertiesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterWorkloadPolicy(ctx *AlterWorkloadPolicyContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterSqlBlockRule(ctx *AlterSqlBlockRuleContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterCatalogComment(ctx *AlterCatalogCommentContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterDatabaseRename(ctx *AlterDatabaseRenameContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterStoragePolicy(ctx *AlterStoragePolicyContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterTable(ctx *AlterTableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterTableAddRollup(ctx *AlterTableAddRollupContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterTableDropRollup(ctx *AlterTableDropRollupContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterTableProperties(ctx *AlterTablePropertiesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterDatabaseSetQuota(ctx *AlterDatabaseSetQuotaContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterDatabaseProperties(ctx *AlterDatabasePropertiesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterSystemRenameComputeGroup(ctx *AlterSystemRenameComputeGroupContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterResource(ctx *AlterResourceContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterRepository(ctx *AlterRepositoryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterRoutineLoad(ctx *AlterRoutineLoadContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterColocateGroup(ctx *AlterColocateGroupContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterUser(ctx *AlterUserContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropCatalogRecycleBin(ctx *DropCatalogRecycleBinContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropEncryptkey(ctx *DropEncryptkeyContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropRole(ctx *DropRoleContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropSqlBlockRule(ctx *DropSqlBlockRuleContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropUser(ctx *DropUserContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropStoragePolicy(ctx *DropStoragePolicyContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropWorkloadGroup(ctx *DropWorkloadGroupContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropCatalog(ctx *DropCatalogContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropFile(ctx *DropFileContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropWorkloadPolicy(ctx *DropWorkloadPolicyContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropRepository(ctx *DropRepositoryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropTable(ctx *DropTableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropDatabase(ctx *DropDatabaseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropFunction(ctx *DropFunctionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropIndex(ctx *DropIndexContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropResource(ctx *DropResourceContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropRowPolicy(ctx *DropRowPolicyContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropDictionary(ctx *DropDictionaryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropStage(ctx *DropStageContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropView(ctx *DropViewContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropIndexAnalyzer(ctx *DropIndexAnalyzerContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropIndexTokenizer(ctx *DropIndexTokenizerContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropIndexTokenFilter(ctx *DropIndexTokenFilterContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowVariables(ctx *ShowVariablesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowAuthors(ctx *ShowAuthorsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowAlterTable(ctx *ShowAlterTableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowCreateDatabase(ctx *ShowCreateDatabaseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowBackup(ctx *ShowBackupContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowBroker(ctx *ShowBrokerContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowBuildIndex(ctx *ShowBuildIndexContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowDynamicPartition(ctx *ShowDynamicPartitionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowEvents(ctx *ShowEventsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowExport(ctx *ShowExportContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowLastInsert(ctx *ShowLastInsertContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowCharset(ctx *ShowCharsetContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowDelete(ctx *ShowDeleteContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowCreateFunction(ctx *ShowCreateFunctionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowFunctions(ctx *ShowFunctionsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowGlobalFunctions(ctx *ShowGlobalFunctionsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowGrants(ctx *ShowGrantsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowGrantsForUser(ctx *ShowGrantsForUserContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowCreateUser(ctx *ShowCreateUserContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowSnapshot(ctx *ShowSnapshotContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowLoadProfile(ctx *ShowLoadProfileContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowCreateRepository(ctx *ShowCreateRepositoryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowView(ctx *ShowViewContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowPlugins(ctx *ShowPluginsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowStorageVault(ctx *ShowStorageVaultContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowRepositories(ctx *ShowRepositoriesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowEncryptKeys(ctx *ShowEncryptKeysContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowCreateTable(ctx *ShowCreateTableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowProcessList(ctx *ShowProcessListContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowPartitions(ctx *ShowPartitionsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowRestore(ctx *ShowRestoreContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowRoles(ctx *ShowRolesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowPartitionId(ctx *ShowPartitionIdContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowPrivileges(ctx *ShowPrivilegesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowProc(ctx *ShowProcContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowSmallFiles(ctx *ShowSmallFilesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowStorageEngines(ctx *ShowStorageEnginesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowCreateCatalog(ctx *ShowCreateCatalogContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowCatalog(ctx *ShowCatalogContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowCatalogs(ctx *ShowCatalogsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowUserProperties(ctx *ShowUserPropertiesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowAllProperties(ctx *ShowAllPropertiesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowCollation(ctx *ShowCollationContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowRowPolicy(ctx *ShowRowPolicyContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowStoragePolicy(ctx *ShowStoragePolicyContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowSqlBlockRule(ctx *ShowSqlBlockRuleContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowCreateView(ctx *ShowCreateViewContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowDataTypes(ctx *ShowDataTypesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowData(ctx *ShowDataContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowCreateMaterializedView(ctx *ShowCreateMaterializedViewContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowWarningErrors(ctx *ShowWarningErrorsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowWarningErrorCount(ctx *ShowWarningErrorCountContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowBackends(ctx *ShowBackendsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowStages(ctx *ShowStagesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowReplicaDistribution(ctx *ShowReplicaDistributionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowResources(ctx *ShowResourcesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowLoad(ctx *ShowLoadContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowLoadWarings(ctx *ShowLoadWaringsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowTriggers(ctx *ShowTriggersContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowDiagnoseTablet(ctx *ShowDiagnoseTabletContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowOpenTables(ctx *ShowOpenTablesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowFrontends(ctx *ShowFrontendsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowDatabaseId(ctx *ShowDatabaseIdContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowColumns(ctx *ShowColumnsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowTableId(ctx *ShowTableIdContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowTrash(ctx *ShowTrashContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowTypeCast(ctx *ShowTypeCastContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowClusters(ctx *ShowClustersContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowStatus(ctx *ShowStatusContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowWhitelist(ctx *ShowWhitelistContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowTabletsBelong(ctx *ShowTabletsBelongContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowDataSkew(ctx *ShowDataSkewContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowTableCreation(ctx *ShowTableCreationContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowTabletStorageFormat(ctx *ShowTabletStorageFormatContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowQueryProfile(ctx *ShowQueryProfileContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowConvertLsc(ctx *ShowConvertLscContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowTables(ctx *ShowTablesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowViews(ctx *ShowViewsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowTableStatus(ctx *ShowTableStatusContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowDatabases(ctx *ShowDatabasesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowTabletsFromTable(ctx *ShowTabletsFromTableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowCatalogRecycleBin(ctx *ShowCatalogRecycleBinContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowTabletId(ctx *ShowTabletIdContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowDictionaries(ctx *ShowDictionariesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowTransaction(ctx *ShowTransactionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowReplicaStatus(ctx *ShowReplicaStatusContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowWorkloadGroups(ctx *ShowWorkloadGroupsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowCopy(ctx *ShowCopyContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowQueryStats(ctx *ShowQueryStatsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowIndex(ctx *ShowIndexContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowWarmUpJob(ctx *ShowWarmUpJobContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSync(ctx *SyncContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateRoutineLoadAlias(ctx *CreateRoutineLoadAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowCreateRoutineLoad(ctx *ShowCreateRoutineLoadContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPauseRoutineLoad(ctx *PauseRoutineLoadContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPauseAllRoutineLoad(ctx *PauseAllRoutineLoadContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitResumeRoutineLoad(ctx *ResumeRoutineLoadContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitResumeAllRoutineLoad(ctx *ResumeAllRoutineLoadContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitStopRoutineLoad(ctx *StopRoutineLoadContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowRoutineLoad(ctx *ShowRoutineLoadContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowRoutineLoadTask(ctx *ShowRoutineLoadTaskContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowIndexAnalyzer(ctx *ShowIndexAnalyzerContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowIndexTokenizer(ctx *ShowIndexTokenizerContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowIndexTokenFilter(ctx *ShowIndexTokenFilterContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitKillConnection(ctx *KillConnectionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitKillQuery(ctx *KillQueryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitHelp(ctx *HelpContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitUnlockTables(ctx *UnlockTablesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitInstallPlugin(ctx *InstallPluginContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitUninstallPlugin(ctx *UninstallPluginContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitLockTables(ctx *LockTablesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRestore(ctx *RestoreContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitWarmUpCluster(ctx *WarmUpClusterContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitBackup(ctx *BackupContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitUnsupportedStartTransaction(ctx *UnsupportedStartTransactionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitWarmUpItem(ctx *WarmUpItemContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitLockTable(ctx *LockTableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateRoutineLoad(ctx *CreateRoutineLoadContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitMysqlLoad(ctx *MysqlLoadContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowCreateLoad(ctx *ShowCreateLoadContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSeparator(ctx *SeparatorContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitImportColumns(ctx *ImportColumnsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitImportPrecedingFilter(ctx *ImportPrecedingFilterContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitImportWhere(ctx *ImportWhereContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitImportDeleteOn(ctx *ImportDeleteOnContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitImportSequence(ctx *ImportSequenceContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitImportPartitions(ctx *ImportPartitionsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitImportSequenceStatement(ctx *ImportSequenceStatementContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitImportDeleteOnStatement(ctx *ImportDeleteOnStatementContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitImportWhereStatement(ctx *ImportWhereStatementContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitImportPrecedingFilterStatement(ctx *ImportPrecedingFilterStatementContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitImportColumnsStatement(ctx *ImportColumnsStatementContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitImportColumnDesc(ctx *ImportColumnDescContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRefreshCatalog(ctx *RefreshCatalogContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRefreshDatabase(ctx *RefreshDatabaseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRefreshTable(ctx *RefreshTableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRefreshDictionary(ctx *RefreshDictionaryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRefreshLdap(ctx *RefreshLdapContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCleanAllProfile(ctx *CleanAllProfileContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCleanLabel(ctx *CleanLabelContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCleanQueryStats(ctx *CleanQueryStatsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCleanAllQueryStats(ctx *CleanAllQueryStatsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCancelLoad(ctx *CancelLoadContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCancelExport(ctx *CancelExportContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCancelWarmUpJob(ctx *CancelWarmUpJobContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCancelDecommisionBackend(ctx *CancelDecommisionBackendContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCancelBackup(ctx *CancelBackupContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCancelRestore(ctx *CancelRestoreContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCancelBuildIndex(ctx *CancelBuildIndexContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCancelAlterTable(ctx *CancelAlterTableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAdminShowReplicaDistribution(ctx *AdminShowReplicaDistributionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAdminRebalanceDisk(ctx *AdminRebalanceDiskContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAdminCancelRebalanceDisk(ctx *AdminCancelRebalanceDiskContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAdminDiagnoseTablet(ctx *AdminDiagnoseTabletContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAdminShowReplicaStatus(ctx *AdminShowReplicaStatusContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAdminCompactTable(ctx *AdminCompactTableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAdminCheckTablets(ctx *AdminCheckTabletsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAdminShowTabletStorageFormat(ctx *AdminShowTabletStorageFormatContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAdminSetFrontendConfig(ctx *AdminSetFrontendConfigContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAdminCleanTrash(ctx *AdminCleanTrashContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAdminSetReplicaVersion(ctx *AdminSetReplicaVersionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAdminSetTableStatus(ctx *AdminSetTableStatusContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAdminSetReplicaStatus(ctx *AdminSetReplicaStatusContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAdminRepairTable(ctx *AdminRepairTableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAdminCancelRepairTable(ctx *AdminCancelRepairTableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAdminCopyTablet(ctx *AdminCopyTabletContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRecoverDatabase(ctx *RecoverDatabaseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRecoverTable(ctx *RecoverTableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRecoverPartition(ctx *RecoverPartitionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAdminSetPartitionVersion(ctx *AdminSetPartitionVersionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitBaseTableRef(ctx *BaseTableRefContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitWildWhere(ctx *WildWhereContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitTransactionBegin(ctx *TransactionBeginContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitTranscationCommit(ctx *TranscationCommitContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitTransactionRollback(ctx *TransactionRollbackContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitGrantTablePrivilege(ctx *GrantTablePrivilegeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitGrantResourcePrivilege(ctx *GrantResourcePrivilegeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitGrantRole(ctx *GrantRoleContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRevokeRole(ctx *RevokeRoleContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRevokeResourcePrivilege(ctx *RevokeResourcePrivilegeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRevokeTablePrivilege(ctx *RevokeTablePrivilegeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPrivilege(ctx *PrivilegeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPrivilegeList(ctx *PrivilegeListContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAddBackendClause(ctx *AddBackendClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropBackendClause(ctx *DropBackendClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDecommissionBackendClause(ctx *DecommissionBackendClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAddObserverClause(ctx *AddObserverClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropObserverClause(ctx *DropObserverClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAddFollowerClause(ctx *AddFollowerClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropFollowerClause(ctx *DropFollowerClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAddBrokerClause(ctx *AddBrokerClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropBrokerClause(ctx *DropBrokerClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropAllBrokerClause(ctx *DropAllBrokerClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterLoadErrorUrlClause(ctx *AlterLoadErrorUrlClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitModifyBackendClause(ctx *ModifyBackendClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitModifyFrontendOrBackendHostNameClause(ctx *ModifyFrontendOrBackendHostNameClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropRollupClause(ctx *DropRollupClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAddRollupClause(ctx *AddRollupClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAddColumnClause(ctx *AddColumnClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAddColumnsClause(ctx *AddColumnsClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropColumnClause(ctx *DropColumnClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitModifyColumnClause(ctx *ModifyColumnClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitReorderColumnsClause(ctx *ReorderColumnsClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAddPartitionClause(ctx *AddPartitionClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropPartitionClause(ctx *DropPartitionClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitModifyPartitionClause(ctx *ModifyPartitionClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitReplacePartitionClause(ctx *ReplacePartitionClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitReplaceTableClause(ctx *ReplaceTableClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRenameClause(ctx *RenameClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRenameRollupClause(ctx *RenameRollupClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRenamePartitionClause(ctx *RenamePartitionClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRenameColumnClause(ctx *RenameColumnClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAddIndexClause(ctx *AddIndexClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropIndexClause(ctx *DropIndexClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitEnableFeatureClause(ctx *EnableFeatureClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitModifyDistributionClause(ctx *ModifyDistributionClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitModifyTableCommentClause(ctx *ModifyTableCommentClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitModifyColumnCommentClause(ctx *ModifyColumnCommentClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitModifyEngineClause(ctx *ModifyEngineClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterMultiPartitionClause(ctx *AlterMultiPartitionClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateOrReplaceTagClauses(ctx *CreateOrReplaceTagClausesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateOrReplaceBranchClauses(ctx *CreateOrReplaceBranchClausesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropBranchClauses(ctx *DropBranchClausesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropTagClauses(ctx *DropTagClausesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateOrReplaceTagClause(ctx *CreateOrReplaceTagClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateOrReplaceBranchClause(ctx *CreateOrReplaceBranchClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitTagOptions(ctx *TagOptionsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitBranchOptions(ctx *BranchOptionsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRetainTime(ctx *RetainTimeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRetentionSnapshot(ctx *RetentionSnapshotContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitMinSnapshotsToKeep(ctx *MinSnapshotsToKeepContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitTimeValueWithUnit(ctx *TimeValueWithUnitContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropBranchClause(ctx *DropBranchClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropTagClause(ctx *DropTagClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitColumnPosition(ctx *ColumnPositionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitToRollup(ctx *ToRollupContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitFromRollup(ctx *FromRollupContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowAnalyze(ctx *ShowAnalyzeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowQueuedAnalyzeJobs(ctx *ShowQueuedAnalyzeJobsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowColumnHistogramStats(ctx *ShowColumnHistogramStatsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAnalyzeDatabase(ctx *AnalyzeDatabaseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAnalyzeTable(ctx *AnalyzeTableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterTableStats(ctx *AlterTableStatsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterColumnStats(ctx *AlterColumnStatsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowIndexStats(ctx *ShowIndexStatsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropStats(ctx *DropStatsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropCachedStats(ctx *DropCachedStatsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropExpiredStats(ctx *DropExpiredStatsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitKillAnalyzeJob(ctx *KillAnalyzeJobContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropAnalyzeJob(ctx *DropAnalyzeJobContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowTableStats(ctx *ShowTableStatsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowColumnStats(ctx *ShowColumnStatsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowAnalyzeTask(ctx *ShowAnalyzeTaskContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAnalyzeProperties(ctx *AnalyzePropertiesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitWorkloadPolicyActions(ctx *WorkloadPolicyActionsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitWorkloadPolicyAction(ctx *WorkloadPolicyActionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitWorkloadPolicyConditions(ctx *WorkloadPolicyConditionsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitWorkloadPolicyCondition(ctx *WorkloadPolicyConditionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitStorageBackend(ctx *StorageBackendContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPasswordOption(ctx *PasswordOptionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitFunctionArguments(ctx *FunctionArgumentsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDataTypeList(ctx *DataTypeListContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSetOptions(ctx *SetOptionsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSetDefaultStorageVault(ctx *SetDefaultStorageVaultContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSetUserProperties(ctx *SetUserPropertiesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSetTransaction(ctx *SetTransactionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSetVariableWithType(ctx *SetVariableWithTypeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSetNames(ctx *SetNamesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSetCharset(ctx *SetCharsetContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSetCollate(ctx *SetCollateContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSetPassword(ctx *SetPasswordContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSetLdapAdminPassword(ctx *SetLdapAdminPasswordContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSetVariableWithoutType(ctx *SetVariableWithoutTypeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSetSystemVariable(ctx *SetSystemVariableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSetUserVariable(ctx *SetUserVariableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitTransactionAccessMode(ctx *TransactionAccessModeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitIsolationLevel(ctx *IsolationLevelContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSupportedUnsetStatement(ctx *SupportedUnsetStatementContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSwitchCatalog(ctx *SwitchCatalogContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitUseDatabase(ctx *UseDatabaseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitUseCloudCluster(ctx *UseCloudClusterContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitStageAndPattern(ctx *StageAndPatternContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDescribeTableValuedFunction(ctx *DescribeTableValuedFunctionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDescribeTableAll(ctx *DescribeTableAllContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDescribeTable(ctx *DescribeTableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDescribeDictionary(ctx *DescribeDictionaryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitConstraint(ctx *ConstraintContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPartitionSpec(ctx *PartitionSpecContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPartitionTable(ctx *PartitionTableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitIdentityOrFunctionList(ctx *IdentityOrFunctionListContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitIdentityOrFunction(ctx *IdentityOrFunctionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDataDesc(ctx *DataDescContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitStatementScope(ctx *StatementScopeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitBuildMode(ctx *BuildModeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRefreshTrigger(ctx *RefreshTriggerContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRefreshSchedule(ctx *RefreshScheduleContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRefreshMethod(ctx *RefreshMethodContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitMvPartition(ctx *MvPartitionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitIdentifierOrText(ctx *IdentifierOrTextContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitIdentifierOrTextOrAsterisk(ctx *IdentifierOrTextOrAsteriskContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitMultipartIdentifierOrAsterisk(ctx *MultipartIdentifierOrAsteriskContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitIdentifierOrAsterisk(ctx *IdentifierOrAsteriskContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitUserIdentify(ctx *UserIdentifyContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitGrantUserIdentify(ctx *GrantUserIdentifyContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitExplain(ctx *ExplainContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitExplainCommand(ctx *ExplainCommandContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPlanType(ctx *PlanTypeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitReplayCommand(ctx *ReplayCommandContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitReplayType(ctx *ReplayTypeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitMergeType(ctx *MergeTypeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPreFilterClause(ctx *PreFilterClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDeleteOnClause(ctx *DeleteOnClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSequenceColClause(ctx *SequenceColClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitColFromPath(ctx *ColFromPathContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitColMappingList(ctx *ColMappingListContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitMappingExpr(ctx *MappingExprContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitWithRemoteStorageSystem(ctx *WithRemoteStorageSystemContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitResourceDesc(ctx *ResourceDescContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitMysqlDataDesc(ctx *MysqlDataDescContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSkipLines(ctx *SkipLinesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitOutFileClause(ctx *OutFileClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitQuery(ctx *QueryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitQueryTermDefault(ctx *QueryTermDefaultContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSetOperation(ctx *SetOperationContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSetQuantifier(ctx *SetQuantifierContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitQueryPrimaryDefault(ctx *QueryPrimaryDefaultContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSubquery(ctx *SubqueryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitValuesTable(ctx *ValuesTableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRegularQuerySpecification(ctx *RegularQuerySpecificationContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCte(ctx *CteContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAliasQuery(ctx *AliasQueryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitColumnAliases(ctx *ColumnAliasesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSelectClause(ctx *SelectClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSelectColumnClause(ctx *SelectColumnClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitWhereClause(ctx *WhereClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitFromClause(ctx *FromClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitIntoClause(ctx *IntoClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitBulkCollectClause(ctx *BulkCollectClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitTableRow(ctx *TableRowContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRelations(ctx *RelationsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRelation(ctx *RelationContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitJoinRelation(ctx *JoinRelationContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitBracketDistributeType(ctx *BracketDistributeTypeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCommentDistributeType(ctx *CommentDistributeTypeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitBracketRelationHint(ctx *BracketRelationHintContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCommentRelationHint(ctx *CommentRelationHintContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAggClause(ctx *AggClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitGroupingElement(ctx *GroupingElementContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitGroupingSet(ctx *GroupingSetContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitHavingClause(ctx *HavingClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitQualifyClause(ctx *QualifyClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSelectHint(ctx *SelectHintContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitHintStatement(ctx *HintStatementContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitHintAssignment(ctx *HintAssignmentContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitUpdateAssignment(ctx *UpdateAssignmentContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitUpdateAssignmentSeq(ctx *UpdateAssignmentSeqContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitLateralView(ctx *LateralViewContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitQueryOrganization(ctx *QueryOrganizationContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSortClause(ctx *SortClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSortItem(ctx *SortItemContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitLimitClause(ctx *LimitClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPartitionClause(ctx *PartitionClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitJoinType(ctx *JoinTypeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitJoinCriteria(ctx *JoinCriteriaContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitIdentifierList(ctx *IdentifierListContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitIdentifierSeq(ctx *IdentifierSeqContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitOptScanParams(ctx *OptScanParamsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitTableName(ctx *TableNameContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAliasedQuery(ctx *AliasedQueryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitTableValuedFunction(ctx *TableValuedFunctionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRelationList(ctx *RelationListContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitMaterializedViewName(ctx *MaterializedViewNameContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPropertyClause(ctx *PropertyClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPropertyItemList(ctx *PropertyItemListContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPropertyItem(ctx *PropertyItemContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPropertyKey(ctx *PropertyKeyContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPropertyValue(ctx *PropertyValueContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitTableAlias(ctx *TableAliasContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitMultipartIdentifier(ctx *MultipartIdentifierContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSimpleColumnDefs(ctx *SimpleColumnDefsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSimpleColumnDef(ctx *SimpleColumnDefContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitColumnDefs(ctx *ColumnDefsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitColumnDef(ctx *ColumnDefContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitIndexDefs(ctx *IndexDefsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitIndexDef(ctx *IndexDefContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPartitionsDef(ctx *PartitionsDefContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPartitionDef(ctx *PartitionDefContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitLessThanPartitionDef(ctx *LessThanPartitionDefContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitFixedPartitionDef(ctx *FixedPartitionDefContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitStepPartitionDef(ctx *StepPartitionDefContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitInPartitionDef(ctx *InPartitionDefContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPartitionValueList(ctx *PartitionValueListContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPartitionValueDef(ctx *PartitionValueDefContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRollupDefs(ctx *RollupDefsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRollupDef(ctx *RollupDefContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAggTypeDef(ctx *AggTypeDefContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitTabletList(ctx *TabletListContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitInlineTable(ctx *InlineTableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitNamedExpression(ctx *NamedExpressionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitNamedExpressionSeq(ctx *NamedExpressionSeqContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitExpression(ctx *ExpressionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitLambdaExpression(ctx *LambdaExpressionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitExist(ctx *ExistContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitLogicalNot(ctx *LogicalNotContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPredicated(ctx *PredicatedContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitIsnull(ctx *IsnullContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitIs_not_null_pred(ctx *Is_not_null_predContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitLogicalBinary(ctx *LogicalBinaryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDoublePipes(ctx *DoublePipesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRowConstructor(ctx *RowConstructorContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRowConstructorItem(ctx *RowConstructorItemContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPredicate(ctx *PredicateContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitValueExpressionDefault(ctx *ValueExpressionDefaultContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitComparison(ctx *ComparisonContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitArithmeticBinary(ctx *ArithmeticBinaryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitArithmeticUnary(ctx *ArithmeticUnaryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDereference(ctx *DereferenceContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCurrentDate(ctx *CurrentDateContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCast(ctx *CastContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitParenthesizedExpression(ctx *ParenthesizedExpressionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitUserVariable(ctx *UserVariableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitElementAt(ctx *ElementAtContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitLocalTimestamp(ctx *LocalTimestampContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCharFunction(ctx *CharFunctionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitIntervalLiteral(ctx *IntervalLiteralContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSimpleCase(ctx *SimpleCaseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitColumnReference(ctx *ColumnReferenceContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitStar(ctx *StarContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSessionUser(ctx *SessionUserContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitConvertType(ctx *ConvertTypeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitConvertCharSet(ctx *ConvertCharSetContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSubqueryExpression(ctx *SubqueryExpressionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitEncryptKey(ctx *EncryptKeyContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCurrentTime(ctx *CurrentTimeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitLocalTime(ctx *LocalTimeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSystemVariable(ctx *SystemVariableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCollate(ctx *CollateContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCurrentUser(ctx *CurrentUserContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitConstantDefault(ctx *ConstantDefaultContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitExtract(ctx *ExtractContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCurrentTimestamp(ctx *CurrentTimestampContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitFunctionCall(ctx *FunctionCallContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitArraySlice(ctx *ArraySliceContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSearchedCase(ctx *SearchedCaseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitExcept(ctx *ExceptContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitReplace(ctx *ReplaceContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCastDataType(ctx *CastDataTypeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitFunctionCallExpression(ctx *FunctionCallExpressionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitFunctionIdentifier(ctx *FunctionIdentifierContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitFunctionNameIdentifier(ctx *FunctionNameIdentifierContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitWindowSpec(ctx *WindowSpecContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitWindowFrame(ctx *WindowFrameContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitFrameUnits(ctx *FrameUnitsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitFrameBoundary(ctx *FrameBoundaryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitQualifiedName(ctx *QualifiedNameContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSpecifiedPartition(ctx *SpecifiedPartitionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitNullLiteral(ctx *NullLiteralContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitTypeConstructor(ctx *TypeConstructorContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitNumericLiteral(ctx *NumericLiteralContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitBooleanLiteral(ctx *BooleanLiteralContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitStringLiteral(ctx *StringLiteralContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitArrayLiteral(ctx *ArrayLiteralContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitMapLiteral(ctx *MapLiteralContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitStructLiteral(ctx *StructLiteralContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPlaceholder(ctx *PlaceholderContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitComparisonOperator(ctx *ComparisonOperatorContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitBooleanValue(ctx *BooleanValueContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitWhenClause(ctx *WhenClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitInterval(ctx *IntervalContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitUnitIdentifier(ctx *UnitIdentifierContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDataTypeWithNullable(ctx *DataTypeWithNullableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitComplexDataType(ctx *ComplexDataTypeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAggStateDataType(ctx *AggStateDataTypeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPrimitiveDataType(ctx *PrimitiveDataTypeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPrimitiveColType(ctx *PrimitiveColTypeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitComplexColTypeList(ctx *ComplexColTypeListContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitComplexColType(ctx *ComplexColTypeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCommentSpec(ctx *CommentSpecContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSample(ctx *SampleContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSampleByPercentile(ctx *SampleByPercentileContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSampleByRows(ctx *SampleByRowsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitTableSnapshot(ctx *TableSnapshotContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitErrorCapturingIdentifier(ctx *ErrorCapturingIdentifierContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitErrorIdent(ctx *ErrorIdentContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRealIdent(ctx *RealIdentContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitIdentifier(ctx *IdentifierContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitUnquotedIdentifier(ctx *UnquotedIdentifierContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitQuotedIdentifierAlternative(ctx *QuotedIdentifierAlternativeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitQuotedIdentifier(ctx *QuotedIdentifierContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitIntegerLiteral(ctx *IntegerLiteralContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDecimalLiteral(ctx *DecimalLiteralContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitNonReserved(ctx *NonReservedContext) interface{} {
	return v.VisitChildren(ctx)
}
