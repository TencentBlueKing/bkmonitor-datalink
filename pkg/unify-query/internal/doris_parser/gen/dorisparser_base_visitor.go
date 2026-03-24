// Code generated from DorisParser.g4 by ANTLR 4.13.2. DO NOT EDIT.

package gen // DorisParser
import "github.com/antlr4-go/antlr/v4"

type BaseDorisParserVisitor struct {
	*antlr.BaseParseTreeVisitor
}

func (v *BaseDorisParserVisitor) VisitMultiStatements(ctx *MultiStatementsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSingleStatement(ctx *SingleStatementContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitStatementBaseAlias(ctx *StatementBaseAliasContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCallProcedure(ctx *CallProcedureContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateProcedure(ctx *CreateProcedureContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropProcedure(ctx *DropProcedureContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowProcedureStatus(ctx *ShowProcedureStatusContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowCreateProcedure(ctx *ShowCreateProcedureContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowConfig(ctx *ShowConfigContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitStatementDefault(ctx *StatementDefaultContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSupportedDmlStatementAlias(ctx *SupportedDmlStatementAliasContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSupportedCreateStatementAlias(ctx *SupportedCreateStatementAliasContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSupportedAlterStatementAlias(ctx *SupportedAlterStatementAliasContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitMaterializedViewStatementAlias(ctx *MaterializedViewStatementAliasContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSupportedJobStatementAlias(ctx *SupportedJobStatementAliasContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitConstraintStatementAlias(ctx *ConstraintStatementAliasContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSupportedCleanStatementAlias(ctx *SupportedCleanStatementAliasContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSupportedDescribeStatementAlias(ctx *SupportedDescribeStatementAliasContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSupportedDropStatementAlias(ctx *SupportedDropStatementAliasContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSupportedSetStatementAlias(ctx *SupportedSetStatementAliasContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSupportedUnsetStatementAlias(ctx *SupportedUnsetStatementAliasContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSupportedRefreshStatementAlias(ctx *SupportedRefreshStatementAliasContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSupportedShowStatementAlias(ctx *SupportedShowStatementAliasContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSupportedLoadStatementAlias(ctx *SupportedLoadStatementAliasContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSupportedCancelStatementAlias(ctx *SupportedCancelStatementAliasContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSupportedRecoverStatementAlias(ctx *SupportedRecoverStatementAliasContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSupportedAdminStatementAlias(ctx *SupportedAdminStatementAliasContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSupportedUseStatementAlias(ctx *SupportedUseStatementAliasContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSupportedOtherStatementAlias(ctx *SupportedOtherStatementAliasContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSupportedKillStatementAlias(ctx *SupportedKillStatementAliasContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSupportedStatsStatementAlias(ctx *SupportedStatsStatementAliasContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSupportedTransactionStatementAlias(ctx *SupportedTransactionStatementAliasContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSupportedGrantRevokeStatementAlias(ctx *SupportedGrantRevokeStatementAliasContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitUnsupported(ctx *UnsupportedContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitUnsupportedStatement(ctx *UnsupportedStatementContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateMTMV(ctx *CreateMTMVContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRefreshMTMV(ctx *RefreshMTMVContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterMTMV(ctx *AlterMTMVContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropMTMV(ctx *DropMTMVContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPauseMTMV(ctx *PauseMTMVContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitResumeMTMV(ctx *ResumeMTMVContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCancelMTMVTask(ctx *CancelMTMVTaskContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowCreateMTMV(ctx *ShowCreateMTMVContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateScheduledJob(ctx *CreateScheduledJobContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPauseJob(ctx *PauseJobContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropJob(ctx *DropJobContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitResumeJob(ctx *ResumeJobContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCancelJobTask(ctx *CancelJobTaskContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAddConstraint(ctx *AddConstraintContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropConstraint(ctx *DropConstraintContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowConstraint(ctx *ShowConstraintContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitInsertTable(ctx *InsertTableContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitUpdate(ctx *UpdateContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDelete(ctx *DeleteContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitLoad(ctx *LoadContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitExport(ctx *ExportContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitReplay(ctx *ReplayContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCopyInto(ctx *CopyIntoContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitTruncateTable(ctx *TruncateTableContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateTable(ctx *CreateTableContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateView(ctx *CreateViewContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateFile(ctx *CreateFileContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateTableLike(ctx *CreateTableLikeContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateRole(ctx *CreateRoleContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateWorkloadGroup(ctx *CreateWorkloadGroupContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateCatalog(ctx *CreateCatalogContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateRowPolicy(ctx *CreateRowPolicyContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateStoragePolicy(ctx *CreateStoragePolicyContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitBuildIndex(ctx *BuildIndexContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateIndex(ctx *CreateIndexContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateWorkloadPolicy(ctx *CreateWorkloadPolicyContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateSqlBlockRule(ctx *CreateSqlBlockRuleContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateEncryptkey(ctx *CreateEncryptkeyContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateUserDefineFunction(ctx *CreateUserDefineFunctionContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateAliasFunction(ctx *CreateAliasFunctionContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateUser(ctx *CreateUserContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateDatabase(ctx *CreateDatabaseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateRepository(ctx *CreateRepositoryContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateResource(ctx *CreateResourceContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateDictionary(ctx *CreateDictionaryContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateStage(ctx *CreateStageContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateStorageVault(ctx *CreateStorageVaultContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateIndexAnalyzer(ctx *CreateIndexAnalyzerContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateIndexTokenizer(ctx *CreateIndexTokenizerContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateIndexTokenFilter(ctx *CreateIndexTokenFilterContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDictionaryColumnDefs(ctx *DictionaryColumnDefsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDictionaryColumnDef(ctx *DictionaryColumnDefContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterSystem(ctx *AlterSystemContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterView(ctx *AlterViewContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterCatalogRename(ctx *AlterCatalogRenameContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterRole(ctx *AlterRoleContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterStorageVault(ctx *AlterStorageVaultContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterWorkloadGroup(ctx *AlterWorkloadGroupContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterCatalogProperties(ctx *AlterCatalogPropertiesContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterWorkloadPolicy(ctx *AlterWorkloadPolicyContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterSqlBlockRule(ctx *AlterSqlBlockRuleContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterCatalogComment(ctx *AlterCatalogCommentContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterDatabaseRename(ctx *AlterDatabaseRenameContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterStoragePolicy(ctx *AlterStoragePolicyContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterTable(ctx *AlterTableContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterTableAddRollup(ctx *AlterTableAddRollupContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterTableDropRollup(ctx *AlterTableDropRollupContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterTableProperties(ctx *AlterTablePropertiesContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterDatabaseSetQuota(ctx *AlterDatabaseSetQuotaContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterDatabaseProperties(ctx *AlterDatabasePropertiesContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterSystemRenameComputeGroup(ctx *AlterSystemRenameComputeGroupContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterResource(ctx *AlterResourceContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterRepository(ctx *AlterRepositoryContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterRoutineLoad(ctx *AlterRoutineLoadContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterColocateGroup(ctx *AlterColocateGroupContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterUser(ctx *AlterUserContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropCatalogRecycleBin(ctx *DropCatalogRecycleBinContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropEncryptkey(ctx *DropEncryptkeyContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropRole(ctx *DropRoleContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropSqlBlockRule(ctx *DropSqlBlockRuleContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropUser(ctx *DropUserContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropStoragePolicy(ctx *DropStoragePolicyContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropWorkloadGroup(ctx *DropWorkloadGroupContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropCatalog(ctx *DropCatalogContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropFile(ctx *DropFileContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropWorkloadPolicy(ctx *DropWorkloadPolicyContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropRepository(ctx *DropRepositoryContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropTable(ctx *DropTableContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropDatabase(ctx *DropDatabaseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropFunction(ctx *DropFunctionContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropIndex(ctx *DropIndexContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropResource(ctx *DropResourceContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropRowPolicy(ctx *DropRowPolicyContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropDictionary(ctx *DropDictionaryContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropStage(ctx *DropStageContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropView(ctx *DropViewContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropIndexAnalyzer(ctx *DropIndexAnalyzerContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropIndexTokenizer(ctx *DropIndexTokenizerContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropIndexTokenFilter(ctx *DropIndexTokenFilterContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowVariables(ctx *ShowVariablesContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowAuthors(ctx *ShowAuthorsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowAlterTable(ctx *ShowAlterTableContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowCreateDatabase(ctx *ShowCreateDatabaseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowBackup(ctx *ShowBackupContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowBroker(ctx *ShowBrokerContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowBuildIndex(ctx *ShowBuildIndexContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowDynamicPartition(ctx *ShowDynamicPartitionContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowEvents(ctx *ShowEventsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowExport(ctx *ShowExportContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowLastInsert(ctx *ShowLastInsertContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowCharset(ctx *ShowCharsetContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowDelete(ctx *ShowDeleteContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowCreateFunction(ctx *ShowCreateFunctionContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowFunctions(ctx *ShowFunctionsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowGlobalFunctions(ctx *ShowGlobalFunctionsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowGrants(ctx *ShowGrantsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowGrantsForUser(ctx *ShowGrantsForUserContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowCreateUser(ctx *ShowCreateUserContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowSnapshot(ctx *ShowSnapshotContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowLoadProfile(ctx *ShowLoadProfileContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowCreateRepository(ctx *ShowCreateRepositoryContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowView(ctx *ShowViewContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowPlugins(ctx *ShowPluginsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowStorageVault(ctx *ShowStorageVaultContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowRepositories(ctx *ShowRepositoriesContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowEncryptKeys(ctx *ShowEncryptKeysContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowCreateTable(ctx *ShowCreateTableContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowProcessList(ctx *ShowProcessListContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowPartitions(ctx *ShowPartitionsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowRestore(ctx *ShowRestoreContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowRoles(ctx *ShowRolesContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowPartitionId(ctx *ShowPartitionIdContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowPrivileges(ctx *ShowPrivilegesContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowProc(ctx *ShowProcContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowSmallFiles(ctx *ShowSmallFilesContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowStorageEngines(ctx *ShowStorageEnginesContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowCreateCatalog(ctx *ShowCreateCatalogContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowCatalog(ctx *ShowCatalogContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowCatalogs(ctx *ShowCatalogsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowUserProperties(ctx *ShowUserPropertiesContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowAllProperties(ctx *ShowAllPropertiesContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowCollation(ctx *ShowCollationContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowRowPolicy(ctx *ShowRowPolicyContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowStoragePolicy(ctx *ShowStoragePolicyContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowSqlBlockRule(ctx *ShowSqlBlockRuleContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowCreateView(ctx *ShowCreateViewContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowDataTypes(ctx *ShowDataTypesContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowData(ctx *ShowDataContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowCreateMaterializedView(ctx *ShowCreateMaterializedViewContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowWarningErrors(ctx *ShowWarningErrorsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowWarningErrorCount(ctx *ShowWarningErrorCountContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowBackends(ctx *ShowBackendsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowStages(ctx *ShowStagesContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowReplicaDistribution(ctx *ShowReplicaDistributionContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowResources(ctx *ShowResourcesContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowLoad(ctx *ShowLoadContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowLoadWarings(ctx *ShowLoadWaringsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowTriggers(ctx *ShowTriggersContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowDiagnoseTablet(ctx *ShowDiagnoseTabletContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowOpenTables(ctx *ShowOpenTablesContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowFrontends(ctx *ShowFrontendsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowDatabaseId(ctx *ShowDatabaseIdContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowColumns(ctx *ShowColumnsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowTableId(ctx *ShowTableIdContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowTrash(ctx *ShowTrashContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowTypeCast(ctx *ShowTypeCastContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowClusters(ctx *ShowClustersContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowStatus(ctx *ShowStatusContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowWhitelist(ctx *ShowWhitelistContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowTabletsBelong(ctx *ShowTabletsBelongContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowDataSkew(ctx *ShowDataSkewContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowTableCreation(ctx *ShowTableCreationContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowTabletStorageFormat(ctx *ShowTabletStorageFormatContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowQueryProfile(ctx *ShowQueryProfileContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowConvertLsc(ctx *ShowConvertLscContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowTables(ctx *ShowTablesContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowViews(ctx *ShowViewsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowTableStatus(ctx *ShowTableStatusContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowDatabases(ctx *ShowDatabasesContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowTabletsFromTable(ctx *ShowTabletsFromTableContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowCatalogRecycleBin(ctx *ShowCatalogRecycleBinContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowTabletId(ctx *ShowTabletIdContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowDictionaries(ctx *ShowDictionariesContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowTransaction(ctx *ShowTransactionContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowReplicaStatus(ctx *ShowReplicaStatusContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowWorkloadGroups(ctx *ShowWorkloadGroupsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowCopy(ctx *ShowCopyContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowQueryStats(ctx *ShowQueryStatsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowIndex(ctx *ShowIndexContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowWarmUpJob(ctx *ShowWarmUpJobContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSync(ctx *SyncContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateRoutineLoadAlias(ctx *CreateRoutineLoadAliasContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowCreateRoutineLoad(ctx *ShowCreateRoutineLoadContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPauseRoutineLoad(ctx *PauseRoutineLoadContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPauseAllRoutineLoad(ctx *PauseAllRoutineLoadContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitResumeRoutineLoad(ctx *ResumeRoutineLoadContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitResumeAllRoutineLoad(ctx *ResumeAllRoutineLoadContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitStopRoutineLoad(ctx *StopRoutineLoadContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowRoutineLoad(ctx *ShowRoutineLoadContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowRoutineLoadTask(ctx *ShowRoutineLoadTaskContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowIndexAnalyzer(ctx *ShowIndexAnalyzerContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowIndexTokenizer(ctx *ShowIndexTokenizerContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowIndexTokenFilter(ctx *ShowIndexTokenFilterContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitKillConnection(ctx *KillConnectionContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitKillQuery(ctx *KillQueryContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitHelp(ctx *HelpContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitUnlockTables(ctx *UnlockTablesContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitInstallPlugin(ctx *InstallPluginContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitUninstallPlugin(ctx *UninstallPluginContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitLockTables(ctx *LockTablesContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRestore(ctx *RestoreContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitWarmUpCluster(ctx *WarmUpClusterContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitBackup(ctx *BackupContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitUnsupportedStartTransaction(ctx *UnsupportedStartTransactionContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitWarmUpItem(ctx *WarmUpItemContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitLockTable(ctx *LockTableContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateRoutineLoad(ctx *CreateRoutineLoadContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitMysqlLoad(ctx *MysqlLoadContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowCreateLoad(ctx *ShowCreateLoadContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSeparator(ctx *SeparatorContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitImportColumns(ctx *ImportColumnsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitImportPrecedingFilter(ctx *ImportPrecedingFilterContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitImportWhere(ctx *ImportWhereContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitImportDeleteOn(ctx *ImportDeleteOnContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitImportSequence(ctx *ImportSequenceContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitImportPartitions(ctx *ImportPartitionsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitImportSequenceStatement(ctx *ImportSequenceStatementContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitImportDeleteOnStatement(ctx *ImportDeleteOnStatementContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitImportWhereStatement(ctx *ImportWhereStatementContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitImportPrecedingFilterStatement(ctx *ImportPrecedingFilterStatementContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitImportColumnsStatement(ctx *ImportColumnsStatementContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitImportColumnDesc(ctx *ImportColumnDescContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRefreshCatalog(ctx *RefreshCatalogContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRefreshDatabase(ctx *RefreshDatabaseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRefreshTable(ctx *RefreshTableContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRefreshDictionary(ctx *RefreshDictionaryContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRefreshLdap(ctx *RefreshLdapContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCleanAllProfile(ctx *CleanAllProfileContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCleanLabel(ctx *CleanLabelContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCleanQueryStats(ctx *CleanQueryStatsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCleanAllQueryStats(ctx *CleanAllQueryStatsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCancelLoad(ctx *CancelLoadContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCancelExport(ctx *CancelExportContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCancelWarmUpJob(ctx *CancelWarmUpJobContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCancelDecommisionBackend(ctx *CancelDecommisionBackendContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCancelBackup(ctx *CancelBackupContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCancelRestore(ctx *CancelRestoreContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCancelBuildIndex(ctx *CancelBuildIndexContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCancelAlterTable(ctx *CancelAlterTableContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAdminShowReplicaDistribution(ctx *AdminShowReplicaDistributionContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAdminRebalanceDisk(ctx *AdminRebalanceDiskContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAdminCancelRebalanceDisk(ctx *AdminCancelRebalanceDiskContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAdminDiagnoseTablet(ctx *AdminDiagnoseTabletContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAdminShowReplicaStatus(ctx *AdminShowReplicaStatusContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAdminCompactTable(ctx *AdminCompactTableContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAdminCheckTablets(ctx *AdminCheckTabletsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAdminShowTabletStorageFormat(ctx *AdminShowTabletStorageFormatContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAdminSetFrontendConfig(ctx *AdminSetFrontendConfigContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAdminCleanTrash(ctx *AdminCleanTrashContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAdminSetReplicaVersion(ctx *AdminSetReplicaVersionContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAdminSetTableStatus(ctx *AdminSetTableStatusContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAdminSetReplicaStatus(ctx *AdminSetReplicaStatusContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAdminRepairTable(ctx *AdminRepairTableContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAdminCancelRepairTable(ctx *AdminCancelRepairTableContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAdminCopyTablet(ctx *AdminCopyTabletContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRecoverDatabase(ctx *RecoverDatabaseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRecoverTable(ctx *RecoverTableContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRecoverPartition(ctx *RecoverPartitionContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAdminSetPartitionVersion(ctx *AdminSetPartitionVersionContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitBaseTableRef(ctx *BaseTableRefContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitWildWhere(ctx *WildWhereContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitTransactionBegin(ctx *TransactionBeginContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitTranscationCommit(ctx *TranscationCommitContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitTransactionRollback(ctx *TransactionRollbackContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitGrantTablePrivilege(ctx *GrantTablePrivilegeContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitGrantResourcePrivilege(ctx *GrantResourcePrivilegeContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitGrantRole(ctx *GrantRoleContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRevokeRole(ctx *RevokeRoleContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRevokeResourcePrivilege(ctx *RevokeResourcePrivilegeContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRevokeTablePrivilege(ctx *RevokeTablePrivilegeContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPrivilege(ctx *PrivilegeContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPrivilegeList(ctx *PrivilegeListContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAddBackendClause(ctx *AddBackendClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropBackendClause(ctx *DropBackendClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDecommissionBackendClause(ctx *DecommissionBackendClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAddObserverClause(ctx *AddObserverClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropObserverClause(ctx *DropObserverClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAddFollowerClause(ctx *AddFollowerClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropFollowerClause(ctx *DropFollowerClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAddBrokerClause(ctx *AddBrokerClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropBrokerClause(ctx *DropBrokerClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropAllBrokerClause(ctx *DropAllBrokerClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterLoadErrorUrlClause(ctx *AlterLoadErrorUrlClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitModifyBackendClause(ctx *ModifyBackendClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitModifyFrontendOrBackendHostNameClause(ctx *ModifyFrontendOrBackendHostNameClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropRollupClause(ctx *DropRollupClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAddRollupClause(ctx *AddRollupClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAddColumnClause(ctx *AddColumnClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAddColumnsClause(ctx *AddColumnsClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropColumnClause(ctx *DropColumnClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitModifyColumnClause(ctx *ModifyColumnClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitReorderColumnsClause(ctx *ReorderColumnsClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAddPartitionClause(ctx *AddPartitionClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropPartitionClause(ctx *DropPartitionClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitModifyPartitionClause(ctx *ModifyPartitionClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitReplacePartitionClause(ctx *ReplacePartitionClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitReplaceTableClause(ctx *ReplaceTableClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRenameClause(ctx *RenameClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRenameRollupClause(ctx *RenameRollupClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRenamePartitionClause(ctx *RenamePartitionClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRenameColumnClause(ctx *RenameColumnClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAddIndexClause(ctx *AddIndexClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropIndexClause(ctx *DropIndexClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitEnableFeatureClause(ctx *EnableFeatureClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitModifyDistributionClause(ctx *ModifyDistributionClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitModifyTableCommentClause(ctx *ModifyTableCommentClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitModifyColumnCommentClause(ctx *ModifyColumnCommentClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitModifyEngineClause(ctx *ModifyEngineClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterMultiPartitionClause(ctx *AlterMultiPartitionClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateOrReplaceTagClauses(ctx *CreateOrReplaceTagClausesContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateOrReplaceBranchClauses(ctx *CreateOrReplaceBranchClausesContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropBranchClauses(ctx *DropBranchClausesContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropTagClauses(ctx *DropTagClausesContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateOrReplaceTagClause(ctx *CreateOrReplaceTagClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCreateOrReplaceBranchClause(ctx *CreateOrReplaceBranchClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitTagOptions(ctx *TagOptionsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitBranchOptions(ctx *BranchOptionsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRetainTime(ctx *RetainTimeContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRetentionSnapshot(ctx *RetentionSnapshotContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitMinSnapshotsToKeep(ctx *MinSnapshotsToKeepContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitTimeValueWithUnit(ctx *TimeValueWithUnitContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropBranchClause(ctx *DropBranchClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropTagClause(ctx *DropTagClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitColumnPosition(ctx *ColumnPositionContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitToRollup(ctx *ToRollupContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitFromRollup(ctx *FromRollupContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowAnalyze(ctx *ShowAnalyzeContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowQueuedAnalyzeJobs(ctx *ShowQueuedAnalyzeJobsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowColumnHistogramStats(ctx *ShowColumnHistogramStatsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAnalyzeDatabase(ctx *AnalyzeDatabaseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAnalyzeTable(ctx *AnalyzeTableContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterTableStats(ctx *AlterTableStatsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAlterColumnStats(ctx *AlterColumnStatsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowIndexStats(ctx *ShowIndexStatsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropStats(ctx *DropStatsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropCachedStats(ctx *DropCachedStatsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropExpiredStats(ctx *DropExpiredStatsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitKillAnalyzeJob(ctx *KillAnalyzeJobContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDropAnalyzeJob(ctx *DropAnalyzeJobContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowTableStats(ctx *ShowTableStatsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowColumnStats(ctx *ShowColumnStatsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitShowAnalyzeTask(ctx *ShowAnalyzeTaskContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAnalyzeProperties(ctx *AnalyzePropertiesContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitWorkloadPolicyActions(ctx *WorkloadPolicyActionsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitWorkloadPolicyAction(ctx *WorkloadPolicyActionContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitWorkloadPolicyConditions(ctx *WorkloadPolicyConditionsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitWorkloadPolicyCondition(ctx *WorkloadPolicyConditionContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitStorageBackend(ctx *StorageBackendContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPasswordOption(ctx *PasswordOptionContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitFunctionArguments(ctx *FunctionArgumentsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDataTypeList(ctx *DataTypeListContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSetOptions(ctx *SetOptionsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSetDefaultStorageVault(ctx *SetDefaultStorageVaultContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSetUserProperties(ctx *SetUserPropertiesContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSetTransaction(ctx *SetTransactionContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSetVariableWithType(ctx *SetVariableWithTypeContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSetNames(ctx *SetNamesContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSetCharset(ctx *SetCharsetContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSetCollate(ctx *SetCollateContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSetPassword(ctx *SetPasswordContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSetLdapAdminPassword(ctx *SetLdapAdminPasswordContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSetVariableWithoutType(ctx *SetVariableWithoutTypeContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSetSystemVariable(ctx *SetSystemVariableContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSetUserVariable(ctx *SetUserVariableContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitTransactionAccessMode(ctx *TransactionAccessModeContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitIsolationLevel(ctx *IsolationLevelContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSupportedUnsetStatement(ctx *SupportedUnsetStatementContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSwitchCatalog(ctx *SwitchCatalogContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitUseDatabase(ctx *UseDatabaseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitUseCloudCluster(ctx *UseCloudClusterContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitStageAndPattern(ctx *StageAndPatternContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDescribeTableValuedFunction(ctx *DescribeTableValuedFunctionContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDescribeTableAll(ctx *DescribeTableAllContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDescribeTable(ctx *DescribeTableContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDescribeDictionary(ctx *DescribeDictionaryContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitConstraint(ctx *ConstraintContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPartitionSpec(ctx *PartitionSpecContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPartitionTable(ctx *PartitionTableContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitIdentityOrFunctionList(ctx *IdentityOrFunctionListContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitIdentityOrFunction(ctx *IdentityOrFunctionContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDataDesc(ctx *DataDescContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitStatementScope(ctx *StatementScopeContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitBuildMode(ctx *BuildModeContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRefreshTrigger(ctx *RefreshTriggerContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRefreshSchedule(ctx *RefreshScheduleContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRefreshMethod(ctx *RefreshMethodContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitMvPartition(ctx *MvPartitionContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitIdentifierOrText(ctx *IdentifierOrTextContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitIdentifierOrTextOrAsterisk(ctx *IdentifierOrTextOrAsteriskContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitMultipartIdentifierOrAsterisk(ctx *MultipartIdentifierOrAsteriskContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitIdentifierOrAsterisk(ctx *IdentifierOrAsteriskContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitUserIdentify(ctx *UserIdentifyContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitGrantUserIdentify(ctx *GrantUserIdentifyContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitExplain(ctx *ExplainContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitExplainCommand(ctx *ExplainCommandContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPlanType(ctx *PlanTypeContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitReplayCommand(ctx *ReplayCommandContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitReplayType(ctx *ReplayTypeContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitMergeType(ctx *MergeTypeContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPreFilterClause(ctx *PreFilterClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDeleteOnClause(ctx *DeleteOnClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSequenceColClause(ctx *SequenceColClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitColFromPath(ctx *ColFromPathContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitColMappingList(ctx *ColMappingListContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitMappingExpr(ctx *MappingExprContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitWithRemoteStorageSystem(ctx *WithRemoteStorageSystemContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitResourceDesc(ctx *ResourceDescContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitMysqlDataDesc(ctx *MysqlDataDescContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSkipLines(ctx *SkipLinesContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitOutFileClause(ctx *OutFileClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitQuery(ctx *QueryContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitQueryTermDefault(ctx *QueryTermDefaultContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSetOperation(ctx *SetOperationContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSetQuantifier(ctx *SetQuantifierContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitQueryPrimaryDefault(ctx *QueryPrimaryDefaultContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSubquery(ctx *SubqueryContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitValuesTable(ctx *ValuesTableContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRegularQuerySpecification(ctx *RegularQuerySpecificationContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCte(ctx *CteContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAliasQuery(ctx *AliasQueryContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitColumnAliases(ctx *ColumnAliasesContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSelectClause(ctx *SelectClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSelectColumnClause(ctx *SelectColumnClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitWhereClause(ctx *WhereClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitFromClause(ctx *FromClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitIntoClause(ctx *IntoClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitBulkCollectClause(ctx *BulkCollectClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitTableRow(ctx *TableRowContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRelations(ctx *RelationsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRelation(ctx *RelationContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitJoinRelation(ctx *JoinRelationContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitBracketDistributeType(ctx *BracketDistributeTypeContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCommentDistributeType(ctx *CommentDistributeTypeContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitBracketRelationHint(ctx *BracketRelationHintContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCommentRelationHint(ctx *CommentRelationHintContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAggClause(ctx *AggClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitGroupingElement(ctx *GroupingElementContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitGroupingSet(ctx *GroupingSetContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitHavingClause(ctx *HavingClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitQualifyClause(ctx *QualifyClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSelectHint(ctx *SelectHintContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitHintStatement(ctx *HintStatementContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitHintAssignment(ctx *HintAssignmentContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitUpdateAssignment(ctx *UpdateAssignmentContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitUpdateAssignmentSeq(ctx *UpdateAssignmentSeqContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitLateralView(ctx *LateralViewContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitQueryOrganization(ctx *QueryOrganizationContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSortClause(ctx *SortClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSortItem(ctx *SortItemContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitLimitClause(ctx *LimitClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPartitionClause(ctx *PartitionClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitJoinType(ctx *JoinTypeContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitJoinCriteria(ctx *JoinCriteriaContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitIdentifierList(ctx *IdentifierListContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitIdentifierSeq(ctx *IdentifierSeqContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitOptScanParams(ctx *OptScanParamsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitTableName(ctx *TableNameContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAliasedQuery(ctx *AliasedQueryContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitTableValuedFunction(ctx *TableValuedFunctionContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRelationList(ctx *RelationListContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitMaterializedViewName(ctx *MaterializedViewNameContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPropertyClause(ctx *PropertyClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPropertyItemList(ctx *PropertyItemListContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPropertyItem(ctx *PropertyItemContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPropertyKey(ctx *PropertyKeyContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPropertyValue(ctx *PropertyValueContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitTableAlias(ctx *TableAliasContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitMultipartIdentifier(ctx *MultipartIdentifierContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSimpleColumnDefs(ctx *SimpleColumnDefsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSimpleColumnDef(ctx *SimpleColumnDefContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitColumnDefs(ctx *ColumnDefsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitColumnDef(ctx *ColumnDefContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitIndexDefs(ctx *IndexDefsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitIndexDef(ctx *IndexDefContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPartitionsDef(ctx *PartitionsDefContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPartitionDef(ctx *PartitionDefContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitLessThanPartitionDef(ctx *LessThanPartitionDefContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitFixedPartitionDef(ctx *FixedPartitionDefContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitStepPartitionDef(ctx *StepPartitionDefContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitInPartitionDef(ctx *InPartitionDefContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPartitionValueList(ctx *PartitionValueListContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPartitionValueDef(ctx *PartitionValueDefContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRollupDefs(ctx *RollupDefsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRollupDef(ctx *RollupDefContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAggTypeDef(ctx *AggTypeDefContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitTabletList(ctx *TabletListContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitInlineTable(ctx *InlineTableContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitNamedExpression(ctx *NamedExpressionContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitNamedExpressionSeq(ctx *NamedExpressionSeqContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitExpression(ctx *ExpressionContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitLambdaExpression(ctx *LambdaExpressionContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitExist(ctx *ExistContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitLogicalNot(ctx *LogicalNotContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPredicated(ctx *PredicatedContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitIsnull(ctx *IsnullContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitIs_not_null_pred(ctx *Is_not_null_predContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitLogicalBinary(ctx *LogicalBinaryContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDoublePipes(ctx *DoublePipesContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRowConstructor(ctx *RowConstructorContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRowConstructorItem(ctx *RowConstructorItemContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPredicate(ctx *PredicateContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitValueExpressionDefault(ctx *ValueExpressionDefaultContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitComparison(ctx *ComparisonContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitArithmeticBinary(ctx *ArithmeticBinaryContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitArithmeticUnary(ctx *ArithmeticUnaryContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDereference(ctx *DereferenceContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCurrentDate(ctx *CurrentDateContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCast(ctx *CastContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitParenthesizedExpression(ctx *ParenthesizedExpressionContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitUserVariable(ctx *UserVariableContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitElementAt(ctx *ElementAtContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitLocalTimestamp(ctx *LocalTimestampContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCharFunction(ctx *CharFunctionContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitIntervalLiteral(ctx *IntervalLiteralContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSimpleCase(ctx *SimpleCaseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitColumnReference(ctx *ColumnReferenceContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitStar(ctx *StarContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSessionUser(ctx *SessionUserContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitConvertType(ctx *ConvertTypeContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitConvertCharSet(ctx *ConvertCharSetContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSubqueryExpression(ctx *SubqueryExpressionContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitEncryptKey(ctx *EncryptKeyContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCurrentTime(ctx *CurrentTimeContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitLocalTime(ctx *LocalTimeContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSystemVariable(ctx *SystemVariableContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCollate(ctx *CollateContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCurrentUser(ctx *CurrentUserContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitConstantDefault(ctx *ConstantDefaultContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitExtract(ctx *ExtractContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCurrentTimestamp(ctx *CurrentTimestampContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitFunctionCall(ctx *FunctionCallContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitArraySlice(ctx *ArraySliceContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSearchedCase(ctx *SearchedCaseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitExcept(ctx *ExceptContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitReplace(ctx *ReplaceContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCastDataType(ctx *CastDataTypeContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitFunctionCallExpression(ctx *FunctionCallExpressionContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitFunctionIdentifier(ctx *FunctionIdentifierContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitFunctionNameIdentifier(ctx *FunctionNameIdentifierContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitWindowSpec(ctx *WindowSpecContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitWindowFrame(ctx *WindowFrameContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitFrameUnits(ctx *FrameUnitsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitFrameBoundary(ctx *FrameBoundaryContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitQualifiedName(ctx *QualifiedNameContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSpecifiedPartition(ctx *SpecifiedPartitionContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitNullLiteral(ctx *NullLiteralContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitTypeConstructor(ctx *TypeConstructorContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitNumericLiteral(ctx *NumericLiteralContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitBooleanLiteral(ctx *BooleanLiteralContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitStringLiteral(ctx *StringLiteralContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitArrayLiteral(ctx *ArrayLiteralContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitMapLiteral(ctx *MapLiteralContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitStructLiteral(ctx *StructLiteralContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPlaceholder(ctx *PlaceholderContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitComparisonOperator(ctx *ComparisonOperatorContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitBooleanValue(ctx *BooleanValueContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitWhenClause(ctx *WhenClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitInterval(ctx *IntervalContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitUnitIdentifier(ctx *UnitIdentifierContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDataTypeWithNullable(ctx *DataTypeWithNullableContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitComplexDataType(ctx *ComplexDataTypeContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitAggStateDataType(ctx *AggStateDataTypeContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPrimitiveDataType(ctx *PrimitiveDataTypeContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitPrimitiveColType(ctx *PrimitiveColTypeContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitComplexColTypeList(ctx *ComplexColTypeListContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitComplexColType(ctx *ComplexColTypeContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitCommentSpec(ctx *CommentSpecContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSample(ctx *SampleContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSampleByPercentile(ctx *SampleByPercentileContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitSampleByRows(ctx *SampleByRowsContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitTableSnapshot(ctx *TableSnapshotContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitErrorCapturingIdentifier(ctx *ErrorCapturingIdentifierContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitErrorIdent(ctx *ErrorIdentContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitRealIdent(ctx *RealIdentContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitIdentifier(ctx *IdentifierContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitUnquotedIdentifier(ctx *UnquotedIdentifierContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitQuotedIdentifierAlternative(ctx *QuotedIdentifierAlternativeContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitQuotedIdentifier(ctx *QuotedIdentifierContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitIntegerLiteral(ctx *IntegerLiteralContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitDecimalLiteral(ctx *DecimalLiteralContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseDorisParserVisitor) VisitNonReserved(ctx *NonReservedContext) any {
	return v.VisitChildren(ctx)
}
