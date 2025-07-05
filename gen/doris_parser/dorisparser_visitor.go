// Code generated from /Users/renjinming/go/src/github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/doris_parser/antlr4/DorisParser.g4 by ANTLR 4.13.2. DO NOT EDIT.

package doris_parser // DorisParser
import "github.com/antlr4-go/antlr/v4"


// A complete Visitor for a parse tree produced by DorisParser.
type DorisParserVisitor interface {
	antlr.ParseTreeVisitor

	// Visit a parse tree produced by DorisParser#multiStatements.
	VisitMultiStatements(ctx *MultiStatementsContext) interface{}

	// Visit a parse tree produced by DorisParser#singleStatement.
	VisitSingleStatement(ctx *SingleStatementContext) interface{}

	// Visit a parse tree produced by DorisParser#statementBaseAlias.
	VisitStatementBaseAlias(ctx *StatementBaseAliasContext) interface{}

	// Visit a parse tree produced by DorisParser#callProcedure.
	VisitCallProcedure(ctx *CallProcedureContext) interface{}

	// Visit a parse tree produced by DorisParser#createProcedure.
	VisitCreateProcedure(ctx *CreateProcedureContext) interface{}

	// Visit a parse tree produced by DorisParser#dropProcedure.
	VisitDropProcedure(ctx *DropProcedureContext) interface{}

	// Visit a parse tree produced by DorisParser#showProcedureStatus.
	VisitShowProcedureStatus(ctx *ShowProcedureStatusContext) interface{}

	// Visit a parse tree produced by DorisParser#showCreateProcedure.
	VisitShowCreateProcedure(ctx *ShowCreateProcedureContext) interface{}

	// Visit a parse tree produced by DorisParser#showConfig.
	VisitShowConfig(ctx *ShowConfigContext) interface{}

	// Visit a parse tree produced by DorisParser#statementDefault.
	VisitStatementDefault(ctx *StatementDefaultContext) interface{}

	// Visit a parse tree produced by DorisParser#supportedDmlStatementAlias.
	VisitSupportedDmlStatementAlias(ctx *SupportedDmlStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParser#supportedCreateStatementAlias.
	VisitSupportedCreateStatementAlias(ctx *SupportedCreateStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParser#supportedAlterStatementAlias.
	VisitSupportedAlterStatementAlias(ctx *SupportedAlterStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParser#materializedViewStatementAlias.
	VisitMaterializedViewStatementAlias(ctx *MaterializedViewStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParser#supportedJobStatementAlias.
	VisitSupportedJobStatementAlias(ctx *SupportedJobStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParser#constraintStatementAlias.
	VisitConstraintStatementAlias(ctx *ConstraintStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParser#supportedCleanStatementAlias.
	VisitSupportedCleanStatementAlias(ctx *SupportedCleanStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParser#supportedDescribeStatementAlias.
	VisitSupportedDescribeStatementAlias(ctx *SupportedDescribeStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParser#supportedDropStatementAlias.
	VisitSupportedDropStatementAlias(ctx *SupportedDropStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParser#supportedSetStatementAlias.
	VisitSupportedSetStatementAlias(ctx *SupportedSetStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParser#supportedUnsetStatementAlias.
	VisitSupportedUnsetStatementAlias(ctx *SupportedUnsetStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParser#supportedRefreshStatementAlias.
	VisitSupportedRefreshStatementAlias(ctx *SupportedRefreshStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParser#supportedShowStatementAlias.
	VisitSupportedShowStatementAlias(ctx *SupportedShowStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParser#supportedLoadStatementAlias.
	VisitSupportedLoadStatementAlias(ctx *SupportedLoadStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParser#supportedCancelStatementAlias.
	VisitSupportedCancelStatementAlias(ctx *SupportedCancelStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParser#supportedRecoverStatementAlias.
	VisitSupportedRecoverStatementAlias(ctx *SupportedRecoverStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParser#supportedAdminStatementAlias.
	VisitSupportedAdminStatementAlias(ctx *SupportedAdminStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParser#supportedUseStatementAlias.
	VisitSupportedUseStatementAlias(ctx *SupportedUseStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParser#supportedOtherStatementAlias.
	VisitSupportedOtherStatementAlias(ctx *SupportedOtherStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParser#supportedKillStatementAlias.
	VisitSupportedKillStatementAlias(ctx *SupportedKillStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParser#supportedStatsStatementAlias.
	VisitSupportedStatsStatementAlias(ctx *SupportedStatsStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParser#supportedTransactionStatementAlias.
	VisitSupportedTransactionStatementAlias(ctx *SupportedTransactionStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParser#supportedGrantRevokeStatementAlias.
	VisitSupportedGrantRevokeStatementAlias(ctx *SupportedGrantRevokeStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParser#unsupported.
	VisitUnsupported(ctx *UnsupportedContext) interface{}

	// Visit a parse tree produced by DorisParser#unsupportedStatement.
	VisitUnsupportedStatement(ctx *UnsupportedStatementContext) interface{}

	// Visit a parse tree produced by DorisParser#createMTMV.
	VisitCreateMTMV(ctx *CreateMTMVContext) interface{}

	// Visit a parse tree produced by DorisParser#refreshMTMV.
	VisitRefreshMTMV(ctx *RefreshMTMVContext) interface{}

	// Visit a parse tree produced by DorisParser#alterMTMV.
	VisitAlterMTMV(ctx *AlterMTMVContext) interface{}

	// Visit a parse tree produced by DorisParser#dropMTMV.
	VisitDropMTMV(ctx *DropMTMVContext) interface{}

	// Visit a parse tree produced by DorisParser#pauseMTMV.
	VisitPauseMTMV(ctx *PauseMTMVContext) interface{}

	// Visit a parse tree produced by DorisParser#resumeMTMV.
	VisitResumeMTMV(ctx *ResumeMTMVContext) interface{}

	// Visit a parse tree produced by DorisParser#cancelMTMVTask.
	VisitCancelMTMVTask(ctx *CancelMTMVTaskContext) interface{}

	// Visit a parse tree produced by DorisParser#showCreateMTMV.
	VisitShowCreateMTMV(ctx *ShowCreateMTMVContext) interface{}

	// Visit a parse tree produced by DorisParser#createScheduledJob.
	VisitCreateScheduledJob(ctx *CreateScheduledJobContext) interface{}

	// Visit a parse tree produced by DorisParser#pauseJob.
	VisitPauseJob(ctx *PauseJobContext) interface{}

	// Visit a parse tree produced by DorisParser#dropJob.
	VisitDropJob(ctx *DropJobContext) interface{}

	// Visit a parse tree produced by DorisParser#resumeJob.
	VisitResumeJob(ctx *ResumeJobContext) interface{}

	// Visit a parse tree produced by DorisParser#cancelJobTask.
	VisitCancelJobTask(ctx *CancelJobTaskContext) interface{}

	// Visit a parse tree produced by DorisParser#addConstraint.
	VisitAddConstraint(ctx *AddConstraintContext) interface{}

	// Visit a parse tree produced by DorisParser#dropConstraint.
	VisitDropConstraint(ctx *DropConstraintContext) interface{}

	// Visit a parse tree produced by DorisParser#showConstraint.
	VisitShowConstraint(ctx *ShowConstraintContext) interface{}

	// Visit a parse tree produced by DorisParser#insertTable.
	VisitInsertTable(ctx *InsertTableContext) interface{}

	// Visit a parse tree produced by DorisParser#update.
	VisitUpdate(ctx *UpdateContext) interface{}

	// Visit a parse tree produced by DorisParser#delete.
	VisitDelete(ctx *DeleteContext) interface{}

	// Visit a parse tree produced by DorisParser#load.
	VisitLoad(ctx *LoadContext) interface{}

	// Visit a parse tree produced by DorisParser#export.
	VisitExport(ctx *ExportContext) interface{}

	// Visit a parse tree produced by DorisParser#replay.
	VisitReplay(ctx *ReplayContext) interface{}

	// Visit a parse tree produced by DorisParser#copyInto.
	VisitCopyInto(ctx *CopyIntoContext) interface{}

	// Visit a parse tree produced by DorisParser#truncateTable.
	VisitTruncateTable(ctx *TruncateTableContext) interface{}

	// Visit a parse tree produced by DorisParser#createTable.
	VisitCreateTable(ctx *CreateTableContext) interface{}

	// Visit a parse tree produced by DorisParser#createView.
	VisitCreateView(ctx *CreateViewContext) interface{}

	// Visit a parse tree produced by DorisParser#createFile.
	VisitCreateFile(ctx *CreateFileContext) interface{}

	// Visit a parse tree produced by DorisParser#createTableLike.
	VisitCreateTableLike(ctx *CreateTableLikeContext) interface{}

	// Visit a parse tree produced by DorisParser#createRole.
	VisitCreateRole(ctx *CreateRoleContext) interface{}

	// Visit a parse tree produced by DorisParser#createWorkloadGroup.
	VisitCreateWorkloadGroup(ctx *CreateWorkloadGroupContext) interface{}

	// Visit a parse tree produced by DorisParser#createCatalog.
	VisitCreateCatalog(ctx *CreateCatalogContext) interface{}

	// Visit a parse tree produced by DorisParser#createRowPolicy.
	VisitCreateRowPolicy(ctx *CreateRowPolicyContext) interface{}

	// Visit a parse tree produced by DorisParser#createStoragePolicy.
	VisitCreateStoragePolicy(ctx *CreateStoragePolicyContext) interface{}

	// Visit a parse tree produced by DorisParser#buildIndex.
	VisitBuildIndex(ctx *BuildIndexContext) interface{}

	// Visit a parse tree produced by DorisParser#createIndex.
	VisitCreateIndex(ctx *CreateIndexContext) interface{}

	// Visit a parse tree produced by DorisParser#createWorkloadPolicy.
	VisitCreateWorkloadPolicy(ctx *CreateWorkloadPolicyContext) interface{}

	// Visit a parse tree produced by DorisParser#createSqlBlockRule.
	VisitCreateSqlBlockRule(ctx *CreateSqlBlockRuleContext) interface{}

	// Visit a parse tree produced by DorisParser#createEncryptkey.
	VisitCreateEncryptkey(ctx *CreateEncryptkeyContext) interface{}

	// Visit a parse tree produced by DorisParser#createUserDefineFunction.
	VisitCreateUserDefineFunction(ctx *CreateUserDefineFunctionContext) interface{}

	// Visit a parse tree produced by DorisParser#createAliasFunction.
	VisitCreateAliasFunction(ctx *CreateAliasFunctionContext) interface{}

	// Visit a parse tree produced by DorisParser#createUser.
	VisitCreateUser(ctx *CreateUserContext) interface{}

	// Visit a parse tree produced by DorisParser#createDatabase.
	VisitCreateDatabase(ctx *CreateDatabaseContext) interface{}

	// Visit a parse tree produced by DorisParser#createRepository.
	VisitCreateRepository(ctx *CreateRepositoryContext) interface{}

	// Visit a parse tree produced by DorisParser#createResource.
	VisitCreateResource(ctx *CreateResourceContext) interface{}

	// Visit a parse tree produced by DorisParser#createDictionary.
	VisitCreateDictionary(ctx *CreateDictionaryContext) interface{}

	// Visit a parse tree produced by DorisParser#createStage.
	VisitCreateStage(ctx *CreateStageContext) interface{}

	// Visit a parse tree produced by DorisParser#createStorageVault.
	VisitCreateStorageVault(ctx *CreateStorageVaultContext) interface{}

	// Visit a parse tree produced by DorisParser#createIndexAnalyzer.
	VisitCreateIndexAnalyzer(ctx *CreateIndexAnalyzerContext) interface{}

	// Visit a parse tree produced by DorisParser#createIndexTokenizer.
	VisitCreateIndexTokenizer(ctx *CreateIndexTokenizerContext) interface{}

	// Visit a parse tree produced by DorisParser#createIndexTokenFilter.
	VisitCreateIndexTokenFilter(ctx *CreateIndexTokenFilterContext) interface{}

	// Visit a parse tree produced by DorisParser#dictionaryColumnDefs.
	VisitDictionaryColumnDefs(ctx *DictionaryColumnDefsContext) interface{}

	// Visit a parse tree produced by DorisParser#dictionaryColumnDef.
	VisitDictionaryColumnDef(ctx *DictionaryColumnDefContext) interface{}

	// Visit a parse tree produced by DorisParser#alterSystem.
	VisitAlterSystem(ctx *AlterSystemContext) interface{}

	// Visit a parse tree produced by DorisParser#alterView.
	VisitAlterView(ctx *AlterViewContext) interface{}

	// Visit a parse tree produced by DorisParser#alterCatalogRename.
	VisitAlterCatalogRename(ctx *AlterCatalogRenameContext) interface{}

	// Visit a parse tree produced by DorisParser#alterRole.
	VisitAlterRole(ctx *AlterRoleContext) interface{}

	// Visit a parse tree produced by DorisParser#alterStorageVault.
	VisitAlterStorageVault(ctx *AlterStorageVaultContext) interface{}

	// Visit a parse tree produced by DorisParser#alterWorkloadGroup.
	VisitAlterWorkloadGroup(ctx *AlterWorkloadGroupContext) interface{}

	// Visit a parse tree produced by DorisParser#alterCatalogProperties.
	VisitAlterCatalogProperties(ctx *AlterCatalogPropertiesContext) interface{}

	// Visit a parse tree produced by DorisParser#alterWorkloadPolicy.
	VisitAlterWorkloadPolicy(ctx *AlterWorkloadPolicyContext) interface{}

	// Visit a parse tree produced by DorisParser#alterSqlBlockRule.
	VisitAlterSqlBlockRule(ctx *AlterSqlBlockRuleContext) interface{}

	// Visit a parse tree produced by DorisParser#alterCatalogComment.
	VisitAlterCatalogComment(ctx *AlterCatalogCommentContext) interface{}

	// Visit a parse tree produced by DorisParser#alterDatabaseRename.
	VisitAlterDatabaseRename(ctx *AlterDatabaseRenameContext) interface{}

	// Visit a parse tree produced by DorisParser#alterStoragePolicy.
	VisitAlterStoragePolicy(ctx *AlterStoragePolicyContext) interface{}

	// Visit a parse tree produced by DorisParser#alterTable.
	VisitAlterTable(ctx *AlterTableContext) interface{}

	// Visit a parse tree produced by DorisParser#alterTableAddRollup.
	VisitAlterTableAddRollup(ctx *AlterTableAddRollupContext) interface{}

	// Visit a parse tree produced by DorisParser#alterTableDropRollup.
	VisitAlterTableDropRollup(ctx *AlterTableDropRollupContext) interface{}

	// Visit a parse tree produced by DorisParser#alterTableProperties.
	VisitAlterTableProperties(ctx *AlterTablePropertiesContext) interface{}

	// Visit a parse tree produced by DorisParser#alterDatabaseSetQuota.
	VisitAlterDatabaseSetQuota(ctx *AlterDatabaseSetQuotaContext) interface{}

	// Visit a parse tree produced by DorisParser#alterDatabaseProperties.
	VisitAlterDatabaseProperties(ctx *AlterDatabasePropertiesContext) interface{}

	// Visit a parse tree produced by DorisParser#alterSystemRenameComputeGroup.
	VisitAlterSystemRenameComputeGroup(ctx *AlterSystemRenameComputeGroupContext) interface{}

	// Visit a parse tree produced by DorisParser#alterResource.
	VisitAlterResource(ctx *AlterResourceContext) interface{}

	// Visit a parse tree produced by DorisParser#alterRepository.
	VisitAlterRepository(ctx *AlterRepositoryContext) interface{}

	// Visit a parse tree produced by DorisParser#alterRoutineLoad.
	VisitAlterRoutineLoad(ctx *AlterRoutineLoadContext) interface{}

	// Visit a parse tree produced by DorisParser#alterColocateGroup.
	VisitAlterColocateGroup(ctx *AlterColocateGroupContext) interface{}

	// Visit a parse tree produced by DorisParser#alterUser.
	VisitAlterUser(ctx *AlterUserContext) interface{}

	// Visit a parse tree produced by DorisParser#dropCatalogRecycleBin.
	VisitDropCatalogRecycleBin(ctx *DropCatalogRecycleBinContext) interface{}

	// Visit a parse tree produced by DorisParser#dropEncryptkey.
	VisitDropEncryptkey(ctx *DropEncryptkeyContext) interface{}

	// Visit a parse tree produced by DorisParser#dropRole.
	VisitDropRole(ctx *DropRoleContext) interface{}

	// Visit a parse tree produced by DorisParser#dropSqlBlockRule.
	VisitDropSqlBlockRule(ctx *DropSqlBlockRuleContext) interface{}

	// Visit a parse tree produced by DorisParser#dropUser.
	VisitDropUser(ctx *DropUserContext) interface{}

	// Visit a parse tree produced by DorisParser#dropStoragePolicy.
	VisitDropStoragePolicy(ctx *DropStoragePolicyContext) interface{}

	// Visit a parse tree produced by DorisParser#dropWorkloadGroup.
	VisitDropWorkloadGroup(ctx *DropWorkloadGroupContext) interface{}

	// Visit a parse tree produced by DorisParser#dropCatalog.
	VisitDropCatalog(ctx *DropCatalogContext) interface{}

	// Visit a parse tree produced by DorisParser#dropFile.
	VisitDropFile(ctx *DropFileContext) interface{}

	// Visit a parse tree produced by DorisParser#dropWorkloadPolicy.
	VisitDropWorkloadPolicy(ctx *DropWorkloadPolicyContext) interface{}

	// Visit a parse tree produced by DorisParser#dropRepository.
	VisitDropRepository(ctx *DropRepositoryContext) interface{}

	// Visit a parse tree produced by DorisParser#dropTable.
	VisitDropTable(ctx *DropTableContext) interface{}

	// Visit a parse tree produced by DorisParser#dropDatabase.
	VisitDropDatabase(ctx *DropDatabaseContext) interface{}

	// Visit a parse tree produced by DorisParser#dropFunction.
	VisitDropFunction(ctx *DropFunctionContext) interface{}

	// Visit a parse tree produced by DorisParser#dropIndex.
	VisitDropIndex(ctx *DropIndexContext) interface{}

	// Visit a parse tree produced by DorisParser#dropResource.
	VisitDropResource(ctx *DropResourceContext) interface{}

	// Visit a parse tree produced by DorisParser#dropRowPolicy.
	VisitDropRowPolicy(ctx *DropRowPolicyContext) interface{}

	// Visit a parse tree produced by DorisParser#dropDictionary.
	VisitDropDictionary(ctx *DropDictionaryContext) interface{}

	// Visit a parse tree produced by DorisParser#dropStage.
	VisitDropStage(ctx *DropStageContext) interface{}

	// Visit a parse tree produced by DorisParser#dropView.
	VisitDropView(ctx *DropViewContext) interface{}

	// Visit a parse tree produced by DorisParser#dropIndexAnalyzer.
	VisitDropIndexAnalyzer(ctx *DropIndexAnalyzerContext) interface{}

	// Visit a parse tree produced by DorisParser#dropIndexTokenizer.
	VisitDropIndexTokenizer(ctx *DropIndexTokenizerContext) interface{}

	// Visit a parse tree produced by DorisParser#dropIndexTokenFilter.
	VisitDropIndexTokenFilter(ctx *DropIndexTokenFilterContext) interface{}

	// Visit a parse tree produced by DorisParser#showVariables.
	VisitShowVariables(ctx *ShowVariablesContext) interface{}

	// Visit a parse tree produced by DorisParser#showAuthors.
	VisitShowAuthors(ctx *ShowAuthorsContext) interface{}

	// Visit a parse tree produced by DorisParser#showAlterTable.
	VisitShowAlterTable(ctx *ShowAlterTableContext) interface{}

	// Visit a parse tree produced by DorisParser#showCreateDatabase.
	VisitShowCreateDatabase(ctx *ShowCreateDatabaseContext) interface{}

	// Visit a parse tree produced by DorisParser#showBackup.
	VisitShowBackup(ctx *ShowBackupContext) interface{}

	// Visit a parse tree produced by DorisParser#showBroker.
	VisitShowBroker(ctx *ShowBrokerContext) interface{}

	// Visit a parse tree produced by DorisParser#showBuildIndex.
	VisitShowBuildIndex(ctx *ShowBuildIndexContext) interface{}

	// Visit a parse tree produced by DorisParser#showDynamicPartition.
	VisitShowDynamicPartition(ctx *ShowDynamicPartitionContext) interface{}

	// Visit a parse tree produced by DorisParser#showEvents.
	VisitShowEvents(ctx *ShowEventsContext) interface{}

	// Visit a parse tree produced by DorisParser#showExport.
	VisitShowExport(ctx *ShowExportContext) interface{}

	// Visit a parse tree produced by DorisParser#showLastInsert.
	VisitShowLastInsert(ctx *ShowLastInsertContext) interface{}

	// Visit a parse tree produced by DorisParser#showCharset.
	VisitShowCharset(ctx *ShowCharsetContext) interface{}

	// Visit a parse tree produced by DorisParser#showDelete.
	VisitShowDelete(ctx *ShowDeleteContext) interface{}

	// Visit a parse tree produced by DorisParser#showCreateFunction.
	VisitShowCreateFunction(ctx *ShowCreateFunctionContext) interface{}

	// Visit a parse tree produced by DorisParser#showFunctions.
	VisitShowFunctions(ctx *ShowFunctionsContext) interface{}

	// Visit a parse tree produced by DorisParser#showGlobalFunctions.
	VisitShowGlobalFunctions(ctx *ShowGlobalFunctionsContext) interface{}

	// Visit a parse tree produced by DorisParser#showGrants.
	VisitShowGrants(ctx *ShowGrantsContext) interface{}

	// Visit a parse tree produced by DorisParser#showGrantsForUser.
	VisitShowGrantsForUser(ctx *ShowGrantsForUserContext) interface{}

	// Visit a parse tree produced by DorisParser#showCreateUser.
	VisitShowCreateUser(ctx *ShowCreateUserContext) interface{}

	// Visit a parse tree produced by DorisParser#showSnapshot.
	VisitShowSnapshot(ctx *ShowSnapshotContext) interface{}

	// Visit a parse tree produced by DorisParser#showLoadProfile.
	VisitShowLoadProfile(ctx *ShowLoadProfileContext) interface{}

	// Visit a parse tree produced by DorisParser#showCreateRepository.
	VisitShowCreateRepository(ctx *ShowCreateRepositoryContext) interface{}

	// Visit a parse tree produced by DorisParser#showView.
	VisitShowView(ctx *ShowViewContext) interface{}

	// Visit a parse tree produced by DorisParser#showPlugins.
	VisitShowPlugins(ctx *ShowPluginsContext) interface{}

	// Visit a parse tree produced by DorisParser#showStorageVault.
	VisitShowStorageVault(ctx *ShowStorageVaultContext) interface{}

	// Visit a parse tree produced by DorisParser#showRepositories.
	VisitShowRepositories(ctx *ShowRepositoriesContext) interface{}

	// Visit a parse tree produced by DorisParser#showEncryptKeys.
	VisitShowEncryptKeys(ctx *ShowEncryptKeysContext) interface{}

	// Visit a parse tree produced by DorisParser#showCreateTable.
	VisitShowCreateTable(ctx *ShowCreateTableContext) interface{}

	// Visit a parse tree produced by DorisParser#showProcessList.
	VisitShowProcessList(ctx *ShowProcessListContext) interface{}

	// Visit a parse tree produced by DorisParser#showPartitions.
	VisitShowPartitions(ctx *ShowPartitionsContext) interface{}

	// Visit a parse tree produced by DorisParser#showRestore.
	VisitShowRestore(ctx *ShowRestoreContext) interface{}

	// Visit a parse tree produced by DorisParser#showRoles.
	VisitShowRoles(ctx *ShowRolesContext) interface{}

	// Visit a parse tree produced by DorisParser#showPartitionId.
	VisitShowPartitionId(ctx *ShowPartitionIdContext) interface{}

	// Visit a parse tree produced by DorisParser#showPrivileges.
	VisitShowPrivileges(ctx *ShowPrivilegesContext) interface{}

	// Visit a parse tree produced by DorisParser#showProc.
	VisitShowProc(ctx *ShowProcContext) interface{}

	// Visit a parse tree produced by DorisParser#showSmallFiles.
	VisitShowSmallFiles(ctx *ShowSmallFilesContext) interface{}

	// Visit a parse tree produced by DorisParser#showStorageEngines.
	VisitShowStorageEngines(ctx *ShowStorageEnginesContext) interface{}

	// Visit a parse tree produced by DorisParser#showCreateCatalog.
	VisitShowCreateCatalog(ctx *ShowCreateCatalogContext) interface{}

	// Visit a parse tree produced by DorisParser#showCatalog.
	VisitShowCatalog(ctx *ShowCatalogContext) interface{}

	// Visit a parse tree produced by DorisParser#showCatalogs.
	VisitShowCatalogs(ctx *ShowCatalogsContext) interface{}

	// Visit a parse tree produced by DorisParser#showUserProperties.
	VisitShowUserProperties(ctx *ShowUserPropertiesContext) interface{}

	// Visit a parse tree produced by DorisParser#showAllProperties.
	VisitShowAllProperties(ctx *ShowAllPropertiesContext) interface{}

	// Visit a parse tree produced by DorisParser#showCollation.
	VisitShowCollation(ctx *ShowCollationContext) interface{}

	// Visit a parse tree produced by DorisParser#showRowPolicy.
	VisitShowRowPolicy(ctx *ShowRowPolicyContext) interface{}

	// Visit a parse tree produced by DorisParser#showStoragePolicy.
	VisitShowStoragePolicy(ctx *ShowStoragePolicyContext) interface{}

	// Visit a parse tree produced by DorisParser#showSqlBlockRule.
	VisitShowSqlBlockRule(ctx *ShowSqlBlockRuleContext) interface{}

	// Visit a parse tree produced by DorisParser#showCreateView.
	VisitShowCreateView(ctx *ShowCreateViewContext) interface{}

	// Visit a parse tree produced by DorisParser#showDataTypes.
	VisitShowDataTypes(ctx *ShowDataTypesContext) interface{}

	// Visit a parse tree produced by DorisParser#showData.
	VisitShowData(ctx *ShowDataContext) interface{}

	// Visit a parse tree produced by DorisParser#showCreateMaterializedView.
	VisitShowCreateMaterializedView(ctx *ShowCreateMaterializedViewContext) interface{}

	// Visit a parse tree produced by DorisParser#showWarningErrors.
	VisitShowWarningErrors(ctx *ShowWarningErrorsContext) interface{}

	// Visit a parse tree produced by DorisParser#showWarningErrorCount.
	VisitShowWarningErrorCount(ctx *ShowWarningErrorCountContext) interface{}

	// Visit a parse tree produced by DorisParser#showBackends.
	VisitShowBackends(ctx *ShowBackendsContext) interface{}

	// Visit a parse tree produced by DorisParser#showStages.
	VisitShowStages(ctx *ShowStagesContext) interface{}

	// Visit a parse tree produced by DorisParser#showReplicaDistribution.
	VisitShowReplicaDistribution(ctx *ShowReplicaDistributionContext) interface{}

	// Visit a parse tree produced by DorisParser#showResources.
	VisitShowResources(ctx *ShowResourcesContext) interface{}

	// Visit a parse tree produced by DorisParser#showLoad.
	VisitShowLoad(ctx *ShowLoadContext) interface{}

	// Visit a parse tree produced by DorisParser#showLoadWarings.
	VisitShowLoadWarings(ctx *ShowLoadWaringsContext) interface{}

	// Visit a parse tree produced by DorisParser#showTriggers.
	VisitShowTriggers(ctx *ShowTriggersContext) interface{}

	// Visit a parse tree produced by DorisParser#showDiagnoseTablet.
	VisitShowDiagnoseTablet(ctx *ShowDiagnoseTabletContext) interface{}

	// Visit a parse tree produced by DorisParser#showOpenTables.
	VisitShowOpenTables(ctx *ShowOpenTablesContext) interface{}

	// Visit a parse tree produced by DorisParser#showFrontends.
	VisitShowFrontends(ctx *ShowFrontendsContext) interface{}

	// Visit a parse tree produced by DorisParser#showDatabaseId.
	VisitShowDatabaseId(ctx *ShowDatabaseIdContext) interface{}

	// Visit a parse tree produced by DorisParser#showColumns.
	VisitShowColumns(ctx *ShowColumnsContext) interface{}

	// Visit a parse tree produced by DorisParser#showTableId.
	VisitShowTableId(ctx *ShowTableIdContext) interface{}

	// Visit a parse tree produced by DorisParser#showTrash.
	VisitShowTrash(ctx *ShowTrashContext) interface{}

	// Visit a parse tree produced by DorisParser#showTypeCast.
	VisitShowTypeCast(ctx *ShowTypeCastContext) interface{}

	// Visit a parse tree produced by DorisParser#showClusters.
	VisitShowClusters(ctx *ShowClustersContext) interface{}

	// Visit a parse tree produced by DorisParser#showStatus.
	VisitShowStatus(ctx *ShowStatusContext) interface{}

	// Visit a parse tree produced by DorisParser#showWhitelist.
	VisitShowWhitelist(ctx *ShowWhitelistContext) interface{}

	// Visit a parse tree produced by DorisParser#showTabletsBelong.
	VisitShowTabletsBelong(ctx *ShowTabletsBelongContext) interface{}

	// Visit a parse tree produced by DorisParser#showDataSkew.
	VisitShowDataSkew(ctx *ShowDataSkewContext) interface{}

	// Visit a parse tree produced by DorisParser#showTableCreation.
	VisitShowTableCreation(ctx *ShowTableCreationContext) interface{}

	// Visit a parse tree produced by DorisParser#showTabletStorageFormat.
	VisitShowTabletStorageFormat(ctx *ShowTabletStorageFormatContext) interface{}

	// Visit a parse tree produced by DorisParser#showQueryProfile.
	VisitShowQueryProfile(ctx *ShowQueryProfileContext) interface{}

	// Visit a parse tree produced by DorisParser#showConvertLsc.
	VisitShowConvertLsc(ctx *ShowConvertLscContext) interface{}

	// Visit a parse tree produced by DorisParser#showTables.
	VisitShowTables(ctx *ShowTablesContext) interface{}

	// Visit a parse tree produced by DorisParser#showViews.
	VisitShowViews(ctx *ShowViewsContext) interface{}

	// Visit a parse tree produced by DorisParser#showTableStatus.
	VisitShowTableStatus(ctx *ShowTableStatusContext) interface{}

	// Visit a parse tree produced by DorisParser#showDatabases.
	VisitShowDatabases(ctx *ShowDatabasesContext) interface{}

	// Visit a parse tree produced by DorisParser#showTabletsFromTable.
	VisitShowTabletsFromTable(ctx *ShowTabletsFromTableContext) interface{}

	// Visit a parse tree produced by DorisParser#showCatalogRecycleBin.
	VisitShowCatalogRecycleBin(ctx *ShowCatalogRecycleBinContext) interface{}

	// Visit a parse tree produced by DorisParser#showTabletId.
	VisitShowTabletId(ctx *ShowTabletIdContext) interface{}

	// Visit a parse tree produced by DorisParser#showDictionaries.
	VisitShowDictionaries(ctx *ShowDictionariesContext) interface{}

	// Visit a parse tree produced by DorisParser#showTransaction.
	VisitShowTransaction(ctx *ShowTransactionContext) interface{}

	// Visit a parse tree produced by DorisParser#showReplicaStatus.
	VisitShowReplicaStatus(ctx *ShowReplicaStatusContext) interface{}

	// Visit a parse tree produced by DorisParser#showWorkloadGroups.
	VisitShowWorkloadGroups(ctx *ShowWorkloadGroupsContext) interface{}

	// Visit a parse tree produced by DorisParser#showCopy.
	VisitShowCopy(ctx *ShowCopyContext) interface{}

	// Visit a parse tree produced by DorisParser#showQueryStats.
	VisitShowQueryStats(ctx *ShowQueryStatsContext) interface{}

	// Visit a parse tree produced by DorisParser#showIndex.
	VisitShowIndex(ctx *ShowIndexContext) interface{}

	// Visit a parse tree produced by DorisParser#showWarmUpJob.
	VisitShowWarmUpJob(ctx *ShowWarmUpJobContext) interface{}

	// Visit a parse tree produced by DorisParser#sync.
	VisitSync(ctx *SyncContext) interface{}

	// Visit a parse tree produced by DorisParser#createRoutineLoadAlias.
	VisitCreateRoutineLoadAlias(ctx *CreateRoutineLoadAliasContext) interface{}

	// Visit a parse tree produced by DorisParser#showCreateRoutineLoad.
	VisitShowCreateRoutineLoad(ctx *ShowCreateRoutineLoadContext) interface{}

	// Visit a parse tree produced by DorisParser#pauseRoutineLoad.
	VisitPauseRoutineLoad(ctx *PauseRoutineLoadContext) interface{}

	// Visit a parse tree produced by DorisParser#pauseAllRoutineLoad.
	VisitPauseAllRoutineLoad(ctx *PauseAllRoutineLoadContext) interface{}

	// Visit a parse tree produced by DorisParser#resumeRoutineLoad.
	VisitResumeRoutineLoad(ctx *ResumeRoutineLoadContext) interface{}

	// Visit a parse tree produced by DorisParser#resumeAllRoutineLoad.
	VisitResumeAllRoutineLoad(ctx *ResumeAllRoutineLoadContext) interface{}

	// Visit a parse tree produced by DorisParser#stopRoutineLoad.
	VisitStopRoutineLoad(ctx *StopRoutineLoadContext) interface{}

	// Visit a parse tree produced by DorisParser#showRoutineLoad.
	VisitShowRoutineLoad(ctx *ShowRoutineLoadContext) interface{}

	// Visit a parse tree produced by DorisParser#showRoutineLoadTask.
	VisitShowRoutineLoadTask(ctx *ShowRoutineLoadTaskContext) interface{}

	// Visit a parse tree produced by DorisParser#showIndexAnalyzer.
	VisitShowIndexAnalyzer(ctx *ShowIndexAnalyzerContext) interface{}

	// Visit a parse tree produced by DorisParser#showIndexTokenizer.
	VisitShowIndexTokenizer(ctx *ShowIndexTokenizerContext) interface{}

	// Visit a parse tree produced by DorisParser#showIndexTokenFilter.
	VisitShowIndexTokenFilter(ctx *ShowIndexTokenFilterContext) interface{}

	// Visit a parse tree produced by DorisParser#killConnection.
	VisitKillConnection(ctx *KillConnectionContext) interface{}

	// Visit a parse tree produced by DorisParser#killQuery.
	VisitKillQuery(ctx *KillQueryContext) interface{}

	// Visit a parse tree produced by DorisParser#help.
	VisitHelp(ctx *HelpContext) interface{}

	// Visit a parse tree produced by DorisParser#unlockTables.
	VisitUnlockTables(ctx *UnlockTablesContext) interface{}

	// Visit a parse tree produced by DorisParser#installPlugin.
	VisitInstallPlugin(ctx *InstallPluginContext) interface{}

	// Visit a parse tree produced by DorisParser#uninstallPlugin.
	VisitUninstallPlugin(ctx *UninstallPluginContext) interface{}

	// Visit a parse tree produced by DorisParser#lockTables.
	VisitLockTables(ctx *LockTablesContext) interface{}

	// Visit a parse tree produced by DorisParser#restore.
	VisitRestore(ctx *RestoreContext) interface{}

	// Visit a parse tree produced by DorisParser#warmUpCluster.
	VisitWarmUpCluster(ctx *WarmUpClusterContext) interface{}

	// Visit a parse tree produced by DorisParser#backup.
	VisitBackup(ctx *BackupContext) interface{}

	// Visit a parse tree produced by DorisParser#unsupportedStartTransaction.
	VisitUnsupportedStartTransaction(ctx *UnsupportedStartTransactionContext) interface{}

	// Visit a parse tree produced by DorisParser#warmUpItem.
	VisitWarmUpItem(ctx *WarmUpItemContext) interface{}

	// Visit a parse tree produced by DorisParser#lockTable.
	VisitLockTable(ctx *LockTableContext) interface{}

	// Visit a parse tree produced by DorisParser#createRoutineLoad.
	VisitCreateRoutineLoad(ctx *CreateRoutineLoadContext) interface{}

	// Visit a parse tree produced by DorisParser#mysqlLoad.
	VisitMysqlLoad(ctx *MysqlLoadContext) interface{}

	// Visit a parse tree produced by DorisParser#showCreateLoad.
	VisitShowCreateLoad(ctx *ShowCreateLoadContext) interface{}

	// Visit a parse tree produced by DorisParser#separator.
	VisitSeparator(ctx *SeparatorContext) interface{}

	// Visit a parse tree produced by DorisParser#importColumns.
	VisitImportColumns(ctx *ImportColumnsContext) interface{}

	// Visit a parse tree produced by DorisParser#importPrecedingFilter.
	VisitImportPrecedingFilter(ctx *ImportPrecedingFilterContext) interface{}

	// Visit a parse tree produced by DorisParser#importWhere.
	VisitImportWhere(ctx *ImportWhereContext) interface{}

	// Visit a parse tree produced by DorisParser#importDeleteOn.
	VisitImportDeleteOn(ctx *ImportDeleteOnContext) interface{}

	// Visit a parse tree produced by DorisParser#importSequence.
	VisitImportSequence(ctx *ImportSequenceContext) interface{}

	// Visit a parse tree produced by DorisParser#importPartitions.
	VisitImportPartitions(ctx *ImportPartitionsContext) interface{}

	// Visit a parse tree produced by DorisParser#importSequenceStatement.
	VisitImportSequenceStatement(ctx *ImportSequenceStatementContext) interface{}

	// Visit a parse tree produced by DorisParser#importDeleteOnStatement.
	VisitImportDeleteOnStatement(ctx *ImportDeleteOnStatementContext) interface{}

	// Visit a parse tree produced by DorisParser#importWhereStatement.
	VisitImportWhereStatement(ctx *ImportWhereStatementContext) interface{}

	// Visit a parse tree produced by DorisParser#importPrecedingFilterStatement.
	VisitImportPrecedingFilterStatement(ctx *ImportPrecedingFilterStatementContext) interface{}

	// Visit a parse tree produced by DorisParser#importColumnsStatement.
	VisitImportColumnsStatement(ctx *ImportColumnsStatementContext) interface{}

	// Visit a parse tree produced by DorisParser#importColumnDesc.
	VisitImportColumnDesc(ctx *ImportColumnDescContext) interface{}

	// Visit a parse tree produced by DorisParser#refreshCatalog.
	VisitRefreshCatalog(ctx *RefreshCatalogContext) interface{}

	// Visit a parse tree produced by DorisParser#refreshDatabase.
	VisitRefreshDatabase(ctx *RefreshDatabaseContext) interface{}

	// Visit a parse tree produced by DorisParser#refreshTable.
	VisitRefreshTable(ctx *RefreshTableContext) interface{}

	// Visit a parse tree produced by DorisParser#refreshDictionary.
	VisitRefreshDictionary(ctx *RefreshDictionaryContext) interface{}

	// Visit a parse tree produced by DorisParser#refreshLdap.
	VisitRefreshLdap(ctx *RefreshLdapContext) interface{}

	// Visit a parse tree produced by DorisParser#cleanAllProfile.
	VisitCleanAllProfile(ctx *CleanAllProfileContext) interface{}

	// Visit a parse tree produced by DorisParser#cleanLabel.
	VisitCleanLabel(ctx *CleanLabelContext) interface{}

	// Visit a parse tree produced by DorisParser#cleanQueryStats.
	VisitCleanQueryStats(ctx *CleanQueryStatsContext) interface{}

	// Visit a parse tree produced by DorisParser#cleanAllQueryStats.
	VisitCleanAllQueryStats(ctx *CleanAllQueryStatsContext) interface{}

	// Visit a parse tree produced by DorisParser#cancelLoad.
	VisitCancelLoad(ctx *CancelLoadContext) interface{}

	// Visit a parse tree produced by DorisParser#cancelExport.
	VisitCancelExport(ctx *CancelExportContext) interface{}

	// Visit a parse tree produced by DorisParser#cancelWarmUpJob.
	VisitCancelWarmUpJob(ctx *CancelWarmUpJobContext) interface{}

	// Visit a parse tree produced by DorisParser#cancelDecommisionBackend.
	VisitCancelDecommisionBackend(ctx *CancelDecommisionBackendContext) interface{}

	// Visit a parse tree produced by DorisParser#cancelBackup.
	VisitCancelBackup(ctx *CancelBackupContext) interface{}

	// Visit a parse tree produced by DorisParser#cancelRestore.
	VisitCancelRestore(ctx *CancelRestoreContext) interface{}

	// Visit a parse tree produced by DorisParser#cancelBuildIndex.
	VisitCancelBuildIndex(ctx *CancelBuildIndexContext) interface{}

	// Visit a parse tree produced by DorisParser#cancelAlterTable.
	VisitCancelAlterTable(ctx *CancelAlterTableContext) interface{}

	// Visit a parse tree produced by DorisParser#adminShowReplicaDistribution.
	VisitAdminShowReplicaDistribution(ctx *AdminShowReplicaDistributionContext) interface{}

	// Visit a parse tree produced by DorisParser#adminRebalanceDisk.
	VisitAdminRebalanceDisk(ctx *AdminRebalanceDiskContext) interface{}

	// Visit a parse tree produced by DorisParser#adminCancelRebalanceDisk.
	VisitAdminCancelRebalanceDisk(ctx *AdminCancelRebalanceDiskContext) interface{}

	// Visit a parse tree produced by DorisParser#adminDiagnoseTablet.
	VisitAdminDiagnoseTablet(ctx *AdminDiagnoseTabletContext) interface{}

	// Visit a parse tree produced by DorisParser#adminShowReplicaStatus.
	VisitAdminShowReplicaStatus(ctx *AdminShowReplicaStatusContext) interface{}

	// Visit a parse tree produced by DorisParser#adminCompactTable.
	VisitAdminCompactTable(ctx *AdminCompactTableContext) interface{}

	// Visit a parse tree produced by DorisParser#adminCheckTablets.
	VisitAdminCheckTablets(ctx *AdminCheckTabletsContext) interface{}

	// Visit a parse tree produced by DorisParser#adminShowTabletStorageFormat.
	VisitAdminShowTabletStorageFormat(ctx *AdminShowTabletStorageFormatContext) interface{}

	// Visit a parse tree produced by DorisParser#adminSetFrontendConfig.
	VisitAdminSetFrontendConfig(ctx *AdminSetFrontendConfigContext) interface{}

	// Visit a parse tree produced by DorisParser#adminCleanTrash.
	VisitAdminCleanTrash(ctx *AdminCleanTrashContext) interface{}

	// Visit a parse tree produced by DorisParser#adminSetReplicaVersion.
	VisitAdminSetReplicaVersion(ctx *AdminSetReplicaVersionContext) interface{}

	// Visit a parse tree produced by DorisParser#adminSetTableStatus.
	VisitAdminSetTableStatus(ctx *AdminSetTableStatusContext) interface{}

	// Visit a parse tree produced by DorisParser#adminSetReplicaStatus.
	VisitAdminSetReplicaStatus(ctx *AdminSetReplicaStatusContext) interface{}

	// Visit a parse tree produced by DorisParser#adminRepairTable.
	VisitAdminRepairTable(ctx *AdminRepairTableContext) interface{}

	// Visit a parse tree produced by DorisParser#adminCancelRepairTable.
	VisitAdminCancelRepairTable(ctx *AdminCancelRepairTableContext) interface{}

	// Visit a parse tree produced by DorisParser#adminCopyTablet.
	VisitAdminCopyTablet(ctx *AdminCopyTabletContext) interface{}

	// Visit a parse tree produced by DorisParser#recoverDatabase.
	VisitRecoverDatabase(ctx *RecoverDatabaseContext) interface{}

	// Visit a parse tree produced by DorisParser#recoverTable.
	VisitRecoverTable(ctx *RecoverTableContext) interface{}

	// Visit a parse tree produced by DorisParser#recoverPartition.
	VisitRecoverPartition(ctx *RecoverPartitionContext) interface{}

	// Visit a parse tree produced by DorisParser#adminSetPartitionVersion.
	VisitAdminSetPartitionVersion(ctx *AdminSetPartitionVersionContext) interface{}

	// Visit a parse tree produced by DorisParser#baseTableRef.
	VisitBaseTableRef(ctx *BaseTableRefContext) interface{}

	// Visit a parse tree produced by DorisParser#wildWhere.
	VisitWildWhere(ctx *WildWhereContext) interface{}

	// Visit a parse tree produced by DorisParser#transactionBegin.
	VisitTransactionBegin(ctx *TransactionBeginContext) interface{}

	// Visit a parse tree produced by DorisParser#transcationCommit.
	VisitTranscationCommit(ctx *TranscationCommitContext) interface{}

	// Visit a parse tree produced by DorisParser#transactionRollback.
	VisitTransactionRollback(ctx *TransactionRollbackContext) interface{}

	// Visit a parse tree produced by DorisParser#grantTablePrivilege.
	VisitGrantTablePrivilege(ctx *GrantTablePrivilegeContext) interface{}

	// Visit a parse tree produced by DorisParser#grantResourcePrivilege.
	VisitGrantResourcePrivilege(ctx *GrantResourcePrivilegeContext) interface{}

	// Visit a parse tree produced by DorisParser#grantRole.
	VisitGrantRole(ctx *GrantRoleContext) interface{}

	// Visit a parse tree produced by DorisParser#revokeRole.
	VisitRevokeRole(ctx *RevokeRoleContext) interface{}

	// Visit a parse tree produced by DorisParser#revokeResourcePrivilege.
	VisitRevokeResourcePrivilege(ctx *RevokeResourcePrivilegeContext) interface{}

	// Visit a parse tree produced by DorisParser#revokeTablePrivilege.
	VisitRevokeTablePrivilege(ctx *RevokeTablePrivilegeContext) interface{}

	// Visit a parse tree produced by DorisParser#privilege.
	VisitPrivilege(ctx *PrivilegeContext) interface{}

	// Visit a parse tree produced by DorisParser#privilegeList.
	VisitPrivilegeList(ctx *PrivilegeListContext) interface{}

	// Visit a parse tree produced by DorisParser#addBackendClause.
	VisitAddBackendClause(ctx *AddBackendClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#dropBackendClause.
	VisitDropBackendClause(ctx *DropBackendClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#decommissionBackendClause.
	VisitDecommissionBackendClause(ctx *DecommissionBackendClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#addObserverClause.
	VisitAddObserverClause(ctx *AddObserverClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#dropObserverClause.
	VisitDropObserverClause(ctx *DropObserverClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#addFollowerClause.
	VisitAddFollowerClause(ctx *AddFollowerClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#dropFollowerClause.
	VisitDropFollowerClause(ctx *DropFollowerClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#addBrokerClause.
	VisitAddBrokerClause(ctx *AddBrokerClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#dropBrokerClause.
	VisitDropBrokerClause(ctx *DropBrokerClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#dropAllBrokerClause.
	VisitDropAllBrokerClause(ctx *DropAllBrokerClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#alterLoadErrorUrlClause.
	VisitAlterLoadErrorUrlClause(ctx *AlterLoadErrorUrlClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#modifyBackendClause.
	VisitModifyBackendClause(ctx *ModifyBackendClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#modifyFrontendOrBackendHostNameClause.
	VisitModifyFrontendOrBackendHostNameClause(ctx *ModifyFrontendOrBackendHostNameClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#dropRollupClause.
	VisitDropRollupClause(ctx *DropRollupClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#addRollupClause.
	VisitAddRollupClause(ctx *AddRollupClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#addColumnClause.
	VisitAddColumnClause(ctx *AddColumnClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#addColumnsClause.
	VisitAddColumnsClause(ctx *AddColumnsClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#dropColumnClause.
	VisitDropColumnClause(ctx *DropColumnClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#modifyColumnClause.
	VisitModifyColumnClause(ctx *ModifyColumnClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#reorderColumnsClause.
	VisitReorderColumnsClause(ctx *ReorderColumnsClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#addPartitionClause.
	VisitAddPartitionClause(ctx *AddPartitionClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#dropPartitionClause.
	VisitDropPartitionClause(ctx *DropPartitionClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#modifyPartitionClause.
	VisitModifyPartitionClause(ctx *ModifyPartitionClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#replacePartitionClause.
	VisitReplacePartitionClause(ctx *ReplacePartitionClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#replaceTableClause.
	VisitReplaceTableClause(ctx *ReplaceTableClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#renameClause.
	VisitRenameClause(ctx *RenameClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#renameRollupClause.
	VisitRenameRollupClause(ctx *RenameRollupClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#renamePartitionClause.
	VisitRenamePartitionClause(ctx *RenamePartitionClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#renameColumnClause.
	VisitRenameColumnClause(ctx *RenameColumnClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#addIndexClause.
	VisitAddIndexClause(ctx *AddIndexClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#dropIndexClause.
	VisitDropIndexClause(ctx *DropIndexClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#enableFeatureClause.
	VisitEnableFeatureClause(ctx *EnableFeatureClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#modifyDistributionClause.
	VisitModifyDistributionClause(ctx *ModifyDistributionClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#modifyTableCommentClause.
	VisitModifyTableCommentClause(ctx *ModifyTableCommentClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#modifyColumnCommentClause.
	VisitModifyColumnCommentClause(ctx *ModifyColumnCommentClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#modifyEngineClause.
	VisitModifyEngineClause(ctx *ModifyEngineClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#alterMultiPartitionClause.
	VisitAlterMultiPartitionClause(ctx *AlterMultiPartitionClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#createOrReplaceTagClauses.
	VisitCreateOrReplaceTagClauses(ctx *CreateOrReplaceTagClausesContext) interface{}

	// Visit a parse tree produced by DorisParser#createOrReplaceBranchClauses.
	VisitCreateOrReplaceBranchClauses(ctx *CreateOrReplaceBranchClausesContext) interface{}

	// Visit a parse tree produced by DorisParser#dropBranchClauses.
	VisitDropBranchClauses(ctx *DropBranchClausesContext) interface{}

	// Visit a parse tree produced by DorisParser#dropTagClauses.
	VisitDropTagClauses(ctx *DropTagClausesContext) interface{}

	// Visit a parse tree produced by DorisParser#createOrReplaceTagClause.
	VisitCreateOrReplaceTagClause(ctx *CreateOrReplaceTagClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#createOrReplaceBranchClause.
	VisitCreateOrReplaceBranchClause(ctx *CreateOrReplaceBranchClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#tagOptions.
	VisitTagOptions(ctx *TagOptionsContext) interface{}

	// Visit a parse tree produced by DorisParser#branchOptions.
	VisitBranchOptions(ctx *BranchOptionsContext) interface{}

	// Visit a parse tree produced by DorisParser#retainTime.
	VisitRetainTime(ctx *RetainTimeContext) interface{}

	// Visit a parse tree produced by DorisParser#retentionSnapshot.
	VisitRetentionSnapshot(ctx *RetentionSnapshotContext) interface{}

	// Visit a parse tree produced by DorisParser#minSnapshotsToKeep.
	VisitMinSnapshotsToKeep(ctx *MinSnapshotsToKeepContext) interface{}

	// Visit a parse tree produced by DorisParser#timeValueWithUnit.
	VisitTimeValueWithUnit(ctx *TimeValueWithUnitContext) interface{}

	// Visit a parse tree produced by DorisParser#dropBranchClause.
	VisitDropBranchClause(ctx *DropBranchClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#dropTagClause.
	VisitDropTagClause(ctx *DropTagClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#columnPosition.
	VisitColumnPosition(ctx *ColumnPositionContext) interface{}

	// Visit a parse tree produced by DorisParser#toRollup.
	VisitToRollup(ctx *ToRollupContext) interface{}

	// Visit a parse tree produced by DorisParser#fromRollup.
	VisitFromRollup(ctx *FromRollupContext) interface{}

	// Visit a parse tree produced by DorisParser#showAnalyze.
	VisitShowAnalyze(ctx *ShowAnalyzeContext) interface{}

	// Visit a parse tree produced by DorisParser#showQueuedAnalyzeJobs.
	VisitShowQueuedAnalyzeJobs(ctx *ShowQueuedAnalyzeJobsContext) interface{}

	// Visit a parse tree produced by DorisParser#showColumnHistogramStats.
	VisitShowColumnHistogramStats(ctx *ShowColumnHistogramStatsContext) interface{}

	// Visit a parse tree produced by DorisParser#analyzeDatabase.
	VisitAnalyzeDatabase(ctx *AnalyzeDatabaseContext) interface{}

	// Visit a parse tree produced by DorisParser#analyzeTable.
	VisitAnalyzeTable(ctx *AnalyzeTableContext) interface{}

	// Visit a parse tree produced by DorisParser#alterTableStats.
	VisitAlterTableStats(ctx *AlterTableStatsContext) interface{}

	// Visit a parse tree produced by DorisParser#alterColumnStats.
	VisitAlterColumnStats(ctx *AlterColumnStatsContext) interface{}

	// Visit a parse tree produced by DorisParser#showIndexStats.
	VisitShowIndexStats(ctx *ShowIndexStatsContext) interface{}

	// Visit a parse tree produced by DorisParser#dropStats.
	VisitDropStats(ctx *DropStatsContext) interface{}

	// Visit a parse tree produced by DorisParser#dropCachedStats.
	VisitDropCachedStats(ctx *DropCachedStatsContext) interface{}

	// Visit a parse tree produced by DorisParser#dropExpiredStats.
	VisitDropExpiredStats(ctx *DropExpiredStatsContext) interface{}

	// Visit a parse tree produced by DorisParser#killAnalyzeJob.
	VisitKillAnalyzeJob(ctx *KillAnalyzeJobContext) interface{}

	// Visit a parse tree produced by DorisParser#dropAnalyzeJob.
	VisitDropAnalyzeJob(ctx *DropAnalyzeJobContext) interface{}

	// Visit a parse tree produced by DorisParser#showTableStats.
	VisitShowTableStats(ctx *ShowTableStatsContext) interface{}

	// Visit a parse tree produced by DorisParser#showColumnStats.
	VisitShowColumnStats(ctx *ShowColumnStatsContext) interface{}

	// Visit a parse tree produced by DorisParser#showAnalyzeTask.
	VisitShowAnalyzeTask(ctx *ShowAnalyzeTaskContext) interface{}

	// Visit a parse tree produced by DorisParser#analyzeProperties.
	VisitAnalyzeProperties(ctx *AnalyzePropertiesContext) interface{}

	// Visit a parse tree produced by DorisParser#workloadPolicyActions.
	VisitWorkloadPolicyActions(ctx *WorkloadPolicyActionsContext) interface{}

	// Visit a parse tree produced by DorisParser#workloadPolicyAction.
	VisitWorkloadPolicyAction(ctx *WorkloadPolicyActionContext) interface{}

	// Visit a parse tree produced by DorisParser#workloadPolicyConditions.
	VisitWorkloadPolicyConditions(ctx *WorkloadPolicyConditionsContext) interface{}

	// Visit a parse tree produced by DorisParser#workloadPolicyCondition.
	VisitWorkloadPolicyCondition(ctx *WorkloadPolicyConditionContext) interface{}

	// Visit a parse tree produced by DorisParser#storageBackend.
	VisitStorageBackend(ctx *StorageBackendContext) interface{}

	// Visit a parse tree produced by DorisParser#passwordOption.
	VisitPasswordOption(ctx *PasswordOptionContext) interface{}

	// Visit a parse tree produced by DorisParser#functionArguments.
	VisitFunctionArguments(ctx *FunctionArgumentsContext) interface{}

	// Visit a parse tree produced by DorisParser#dataTypeList.
	VisitDataTypeList(ctx *DataTypeListContext) interface{}

	// Visit a parse tree produced by DorisParser#setOptions.
	VisitSetOptions(ctx *SetOptionsContext) interface{}

	// Visit a parse tree produced by DorisParser#setDefaultStorageVault.
	VisitSetDefaultStorageVault(ctx *SetDefaultStorageVaultContext) interface{}

	// Visit a parse tree produced by DorisParser#setUserProperties.
	VisitSetUserProperties(ctx *SetUserPropertiesContext) interface{}

	// Visit a parse tree produced by DorisParser#setTransaction.
	VisitSetTransaction(ctx *SetTransactionContext) interface{}

	// Visit a parse tree produced by DorisParser#setVariableWithType.
	VisitSetVariableWithType(ctx *SetVariableWithTypeContext) interface{}

	// Visit a parse tree produced by DorisParser#setNames.
	VisitSetNames(ctx *SetNamesContext) interface{}

	// Visit a parse tree produced by DorisParser#setCharset.
	VisitSetCharset(ctx *SetCharsetContext) interface{}

	// Visit a parse tree produced by DorisParser#setCollate.
	VisitSetCollate(ctx *SetCollateContext) interface{}

	// Visit a parse tree produced by DorisParser#setPassword.
	VisitSetPassword(ctx *SetPasswordContext) interface{}

	// Visit a parse tree produced by DorisParser#setLdapAdminPassword.
	VisitSetLdapAdminPassword(ctx *SetLdapAdminPasswordContext) interface{}

	// Visit a parse tree produced by DorisParser#setVariableWithoutType.
	VisitSetVariableWithoutType(ctx *SetVariableWithoutTypeContext) interface{}

	// Visit a parse tree produced by DorisParser#setSystemVariable.
	VisitSetSystemVariable(ctx *SetSystemVariableContext) interface{}

	// Visit a parse tree produced by DorisParser#setUserVariable.
	VisitSetUserVariable(ctx *SetUserVariableContext) interface{}

	// Visit a parse tree produced by DorisParser#transactionAccessMode.
	VisitTransactionAccessMode(ctx *TransactionAccessModeContext) interface{}

	// Visit a parse tree produced by DorisParser#isolationLevel.
	VisitIsolationLevel(ctx *IsolationLevelContext) interface{}

	// Visit a parse tree produced by DorisParser#supportedUnsetStatement.
	VisitSupportedUnsetStatement(ctx *SupportedUnsetStatementContext) interface{}

	// Visit a parse tree produced by DorisParser#switchCatalog.
	VisitSwitchCatalog(ctx *SwitchCatalogContext) interface{}

	// Visit a parse tree produced by DorisParser#useDatabase.
	VisitUseDatabase(ctx *UseDatabaseContext) interface{}

	// Visit a parse tree produced by DorisParser#useCloudCluster.
	VisitUseCloudCluster(ctx *UseCloudClusterContext) interface{}

	// Visit a parse tree produced by DorisParser#stageAndPattern.
	VisitStageAndPattern(ctx *StageAndPatternContext) interface{}

	// Visit a parse tree produced by DorisParser#describeTableValuedFunction.
	VisitDescribeTableValuedFunction(ctx *DescribeTableValuedFunctionContext) interface{}

	// Visit a parse tree produced by DorisParser#describeTableAll.
	VisitDescribeTableAll(ctx *DescribeTableAllContext) interface{}

	// Visit a parse tree produced by DorisParser#describeTable.
	VisitDescribeTable(ctx *DescribeTableContext) interface{}

	// Visit a parse tree produced by DorisParser#describeDictionary.
	VisitDescribeDictionary(ctx *DescribeDictionaryContext) interface{}

	// Visit a parse tree produced by DorisParser#constraint.
	VisitConstraint(ctx *ConstraintContext) interface{}

	// Visit a parse tree produced by DorisParser#partitionSpec.
	VisitPartitionSpec(ctx *PartitionSpecContext) interface{}

	// Visit a parse tree produced by DorisParser#partitionTable.
	VisitPartitionTable(ctx *PartitionTableContext) interface{}

	// Visit a parse tree produced by DorisParser#identityOrFunctionList.
	VisitIdentityOrFunctionList(ctx *IdentityOrFunctionListContext) interface{}

	// Visit a parse tree produced by DorisParser#identityOrFunction.
	VisitIdentityOrFunction(ctx *IdentityOrFunctionContext) interface{}

	// Visit a parse tree produced by DorisParser#dataDesc.
	VisitDataDesc(ctx *DataDescContext) interface{}

	// Visit a parse tree produced by DorisParser#statementScope.
	VisitStatementScope(ctx *StatementScopeContext) interface{}

	// Visit a parse tree produced by DorisParser#buildMode.
	VisitBuildMode(ctx *BuildModeContext) interface{}

	// Visit a parse tree produced by DorisParser#refreshTrigger.
	VisitRefreshTrigger(ctx *RefreshTriggerContext) interface{}

	// Visit a parse tree produced by DorisParser#refreshSchedule.
	VisitRefreshSchedule(ctx *RefreshScheduleContext) interface{}

	// Visit a parse tree produced by DorisParser#refreshMethod.
	VisitRefreshMethod(ctx *RefreshMethodContext) interface{}

	// Visit a parse tree produced by DorisParser#mvPartition.
	VisitMvPartition(ctx *MvPartitionContext) interface{}

	// Visit a parse tree produced by DorisParser#identifierOrText.
	VisitIdentifierOrText(ctx *IdentifierOrTextContext) interface{}

	// Visit a parse tree produced by DorisParser#identifierOrTextOrAsterisk.
	VisitIdentifierOrTextOrAsterisk(ctx *IdentifierOrTextOrAsteriskContext) interface{}

	// Visit a parse tree produced by DorisParser#multipartIdentifierOrAsterisk.
	VisitMultipartIdentifierOrAsterisk(ctx *MultipartIdentifierOrAsteriskContext) interface{}

	// Visit a parse tree produced by DorisParser#identifierOrAsterisk.
	VisitIdentifierOrAsterisk(ctx *IdentifierOrAsteriskContext) interface{}

	// Visit a parse tree produced by DorisParser#userIdentify.
	VisitUserIdentify(ctx *UserIdentifyContext) interface{}

	// Visit a parse tree produced by DorisParser#grantUserIdentify.
	VisitGrantUserIdentify(ctx *GrantUserIdentifyContext) interface{}

	// Visit a parse tree produced by DorisParser#explain.
	VisitExplain(ctx *ExplainContext) interface{}

	// Visit a parse tree produced by DorisParser#explainCommand.
	VisitExplainCommand(ctx *ExplainCommandContext) interface{}

	// Visit a parse tree produced by DorisParser#planType.
	VisitPlanType(ctx *PlanTypeContext) interface{}

	// Visit a parse tree produced by DorisParser#replayCommand.
	VisitReplayCommand(ctx *ReplayCommandContext) interface{}

	// Visit a parse tree produced by DorisParser#replayType.
	VisitReplayType(ctx *ReplayTypeContext) interface{}

	// Visit a parse tree produced by DorisParser#mergeType.
	VisitMergeType(ctx *MergeTypeContext) interface{}

	// Visit a parse tree produced by DorisParser#preFilterClause.
	VisitPreFilterClause(ctx *PreFilterClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#deleteOnClause.
	VisitDeleteOnClause(ctx *DeleteOnClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#sequenceColClause.
	VisitSequenceColClause(ctx *SequenceColClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#colFromPath.
	VisitColFromPath(ctx *ColFromPathContext) interface{}

	// Visit a parse tree produced by DorisParser#colMappingList.
	VisitColMappingList(ctx *ColMappingListContext) interface{}

	// Visit a parse tree produced by DorisParser#mappingExpr.
	VisitMappingExpr(ctx *MappingExprContext) interface{}

	// Visit a parse tree produced by DorisParser#withRemoteStorageSystem.
	VisitWithRemoteStorageSystem(ctx *WithRemoteStorageSystemContext) interface{}

	// Visit a parse tree produced by DorisParser#resourceDesc.
	VisitResourceDesc(ctx *ResourceDescContext) interface{}

	// Visit a parse tree produced by DorisParser#mysqlDataDesc.
	VisitMysqlDataDesc(ctx *MysqlDataDescContext) interface{}

	// Visit a parse tree produced by DorisParser#skipLines.
	VisitSkipLines(ctx *SkipLinesContext) interface{}

	// Visit a parse tree produced by DorisParser#outFileClause.
	VisitOutFileClause(ctx *OutFileClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#query.
	VisitQuery(ctx *QueryContext) interface{}

	// Visit a parse tree produced by DorisParser#queryTermDefault.
	VisitQueryTermDefault(ctx *QueryTermDefaultContext) interface{}

	// Visit a parse tree produced by DorisParser#setOperation.
	VisitSetOperation(ctx *SetOperationContext) interface{}

	// Visit a parse tree produced by DorisParser#setQuantifier.
	VisitSetQuantifier(ctx *SetQuantifierContext) interface{}

	// Visit a parse tree produced by DorisParser#queryPrimaryDefault.
	VisitQueryPrimaryDefault(ctx *QueryPrimaryDefaultContext) interface{}

	// Visit a parse tree produced by DorisParser#subquery.
	VisitSubquery(ctx *SubqueryContext) interface{}

	// Visit a parse tree produced by DorisParser#valuesTable.
	VisitValuesTable(ctx *ValuesTableContext) interface{}

	// Visit a parse tree produced by DorisParser#regularQuerySpecification.
	VisitRegularQuerySpecification(ctx *RegularQuerySpecificationContext) interface{}

	// Visit a parse tree produced by DorisParser#cte.
	VisitCte(ctx *CteContext) interface{}

	// Visit a parse tree produced by DorisParser#aliasQuery.
	VisitAliasQuery(ctx *AliasQueryContext) interface{}

	// Visit a parse tree produced by DorisParser#columnAliases.
	VisitColumnAliases(ctx *ColumnAliasesContext) interface{}

	// Visit a parse tree produced by DorisParser#selectClause.
	VisitSelectClause(ctx *SelectClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#selectColumnClause.
	VisitSelectColumnClause(ctx *SelectColumnClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#whereClause.
	VisitWhereClause(ctx *WhereClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#fromClause.
	VisitFromClause(ctx *FromClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#intoClause.
	VisitIntoClause(ctx *IntoClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#bulkCollectClause.
	VisitBulkCollectClause(ctx *BulkCollectClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#tableRow.
	VisitTableRow(ctx *TableRowContext) interface{}

	// Visit a parse tree produced by DorisParser#relations.
	VisitRelations(ctx *RelationsContext) interface{}

	// Visit a parse tree produced by DorisParser#relation.
	VisitRelation(ctx *RelationContext) interface{}

	// Visit a parse tree produced by DorisParser#joinRelation.
	VisitJoinRelation(ctx *JoinRelationContext) interface{}

	// Visit a parse tree produced by DorisParser#bracketDistributeType.
	VisitBracketDistributeType(ctx *BracketDistributeTypeContext) interface{}

	// Visit a parse tree produced by DorisParser#commentDistributeType.
	VisitCommentDistributeType(ctx *CommentDistributeTypeContext) interface{}

	// Visit a parse tree produced by DorisParser#bracketRelationHint.
	VisitBracketRelationHint(ctx *BracketRelationHintContext) interface{}

	// Visit a parse tree produced by DorisParser#commentRelationHint.
	VisitCommentRelationHint(ctx *CommentRelationHintContext) interface{}

	// Visit a parse tree produced by DorisParser#aggClause.
	VisitAggClause(ctx *AggClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#groupingElement.
	VisitGroupingElement(ctx *GroupingElementContext) interface{}

	// Visit a parse tree produced by DorisParser#groupingSet.
	VisitGroupingSet(ctx *GroupingSetContext) interface{}

	// Visit a parse tree produced by DorisParser#havingClause.
	VisitHavingClause(ctx *HavingClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#qualifyClause.
	VisitQualifyClause(ctx *QualifyClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#selectHint.
	VisitSelectHint(ctx *SelectHintContext) interface{}

	// Visit a parse tree produced by DorisParser#hintStatement.
	VisitHintStatement(ctx *HintStatementContext) interface{}

	// Visit a parse tree produced by DorisParser#hintAssignment.
	VisitHintAssignment(ctx *HintAssignmentContext) interface{}

	// Visit a parse tree produced by DorisParser#updateAssignment.
	VisitUpdateAssignment(ctx *UpdateAssignmentContext) interface{}

	// Visit a parse tree produced by DorisParser#updateAssignmentSeq.
	VisitUpdateAssignmentSeq(ctx *UpdateAssignmentSeqContext) interface{}

	// Visit a parse tree produced by DorisParser#lateralView.
	VisitLateralView(ctx *LateralViewContext) interface{}

	// Visit a parse tree produced by DorisParser#queryOrganization.
	VisitQueryOrganization(ctx *QueryOrganizationContext) interface{}

	// Visit a parse tree produced by DorisParser#sortClause.
	VisitSortClause(ctx *SortClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#sortItem.
	VisitSortItem(ctx *SortItemContext) interface{}

	// Visit a parse tree produced by DorisParser#limitClause.
	VisitLimitClause(ctx *LimitClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#partitionClause.
	VisitPartitionClause(ctx *PartitionClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#joinType.
	VisitJoinType(ctx *JoinTypeContext) interface{}

	// Visit a parse tree produced by DorisParser#joinCriteria.
	VisitJoinCriteria(ctx *JoinCriteriaContext) interface{}

	// Visit a parse tree produced by DorisParser#identifierList.
	VisitIdentifierList(ctx *IdentifierListContext) interface{}

	// Visit a parse tree produced by DorisParser#identifierSeq.
	VisitIdentifierSeq(ctx *IdentifierSeqContext) interface{}

	// Visit a parse tree produced by DorisParser#optScanParams.
	VisitOptScanParams(ctx *OptScanParamsContext) interface{}

	// Visit a parse tree produced by DorisParser#tableName.
	VisitTableName(ctx *TableNameContext) interface{}

	// Visit a parse tree produced by DorisParser#aliasedQuery.
	VisitAliasedQuery(ctx *AliasedQueryContext) interface{}

	// Visit a parse tree produced by DorisParser#tableValuedFunction.
	VisitTableValuedFunction(ctx *TableValuedFunctionContext) interface{}

	// Visit a parse tree produced by DorisParser#relationList.
	VisitRelationList(ctx *RelationListContext) interface{}

	// Visit a parse tree produced by DorisParser#materializedViewName.
	VisitMaterializedViewName(ctx *MaterializedViewNameContext) interface{}

	// Visit a parse tree produced by DorisParser#propertyClause.
	VisitPropertyClause(ctx *PropertyClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#propertyItemList.
	VisitPropertyItemList(ctx *PropertyItemListContext) interface{}

	// Visit a parse tree produced by DorisParser#propertyItem.
	VisitPropertyItem(ctx *PropertyItemContext) interface{}

	// Visit a parse tree produced by DorisParser#propertyKey.
	VisitPropertyKey(ctx *PropertyKeyContext) interface{}

	// Visit a parse tree produced by DorisParser#propertyValue.
	VisitPropertyValue(ctx *PropertyValueContext) interface{}

	// Visit a parse tree produced by DorisParser#tableAlias.
	VisitTableAlias(ctx *TableAliasContext) interface{}

	// Visit a parse tree produced by DorisParser#multipartIdentifier.
	VisitMultipartIdentifier(ctx *MultipartIdentifierContext) interface{}

	// Visit a parse tree produced by DorisParser#simpleColumnDefs.
	VisitSimpleColumnDefs(ctx *SimpleColumnDefsContext) interface{}

	// Visit a parse tree produced by DorisParser#simpleColumnDef.
	VisitSimpleColumnDef(ctx *SimpleColumnDefContext) interface{}

	// Visit a parse tree produced by DorisParser#columnDefs.
	VisitColumnDefs(ctx *ColumnDefsContext) interface{}

	// Visit a parse tree produced by DorisParser#columnDef.
	VisitColumnDef(ctx *ColumnDefContext) interface{}

	// Visit a parse tree produced by DorisParser#indexDefs.
	VisitIndexDefs(ctx *IndexDefsContext) interface{}

	// Visit a parse tree produced by DorisParser#indexDef.
	VisitIndexDef(ctx *IndexDefContext) interface{}

	// Visit a parse tree produced by DorisParser#partitionsDef.
	VisitPartitionsDef(ctx *PartitionsDefContext) interface{}

	// Visit a parse tree produced by DorisParser#partitionDef.
	VisitPartitionDef(ctx *PartitionDefContext) interface{}

	// Visit a parse tree produced by DorisParser#lessThanPartitionDef.
	VisitLessThanPartitionDef(ctx *LessThanPartitionDefContext) interface{}

	// Visit a parse tree produced by DorisParser#fixedPartitionDef.
	VisitFixedPartitionDef(ctx *FixedPartitionDefContext) interface{}

	// Visit a parse tree produced by DorisParser#stepPartitionDef.
	VisitStepPartitionDef(ctx *StepPartitionDefContext) interface{}

	// Visit a parse tree produced by DorisParser#inPartitionDef.
	VisitInPartitionDef(ctx *InPartitionDefContext) interface{}

	// Visit a parse tree produced by DorisParser#partitionValueList.
	VisitPartitionValueList(ctx *PartitionValueListContext) interface{}

	// Visit a parse tree produced by DorisParser#partitionValueDef.
	VisitPartitionValueDef(ctx *PartitionValueDefContext) interface{}

	// Visit a parse tree produced by DorisParser#rollupDefs.
	VisitRollupDefs(ctx *RollupDefsContext) interface{}

	// Visit a parse tree produced by DorisParser#rollupDef.
	VisitRollupDef(ctx *RollupDefContext) interface{}

	// Visit a parse tree produced by DorisParser#aggTypeDef.
	VisitAggTypeDef(ctx *AggTypeDefContext) interface{}

	// Visit a parse tree produced by DorisParser#tabletList.
	VisitTabletList(ctx *TabletListContext) interface{}

	// Visit a parse tree produced by DorisParser#inlineTable.
	VisitInlineTable(ctx *InlineTableContext) interface{}

	// Visit a parse tree produced by DorisParser#namedExpression.
	VisitNamedExpression(ctx *NamedExpressionContext) interface{}

	// Visit a parse tree produced by DorisParser#namedExpressionSeq.
	VisitNamedExpressionSeq(ctx *NamedExpressionSeqContext) interface{}

	// Visit a parse tree produced by DorisParser#expression.
	VisitExpression(ctx *ExpressionContext) interface{}

	// Visit a parse tree produced by DorisParser#lambdaExpression.
	VisitLambdaExpression(ctx *LambdaExpressionContext) interface{}

	// Visit a parse tree produced by DorisParser#exist.
	VisitExist(ctx *ExistContext) interface{}

	// Visit a parse tree produced by DorisParser#logicalNot.
	VisitLogicalNot(ctx *LogicalNotContext) interface{}

	// Visit a parse tree produced by DorisParser#predicated.
	VisitPredicated(ctx *PredicatedContext) interface{}

	// Visit a parse tree produced by DorisParser#isnull.
	VisitIsnull(ctx *IsnullContext) interface{}

	// Visit a parse tree produced by DorisParser#is_not_null_pred.
	VisitIs_not_null_pred(ctx *Is_not_null_predContext) interface{}

	// Visit a parse tree produced by DorisParser#logicalBinary.
	VisitLogicalBinary(ctx *LogicalBinaryContext) interface{}

	// Visit a parse tree produced by DorisParser#doublePipes.
	VisitDoublePipes(ctx *DoublePipesContext) interface{}

	// Visit a parse tree produced by DorisParser#rowConstructor.
	VisitRowConstructor(ctx *RowConstructorContext) interface{}

	// Visit a parse tree produced by DorisParser#rowConstructorItem.
	VisitRowConstructorItem(ctx *RowConstructorItemContext) interface{}

	// Visit a parse tree produced by DorisParser#predicate.
	VisitPredicate(ctx *PredicateContext) interface{}

	// Visit a parse tree produced by DorisParser#valueExpressionDefault.
	VisitValueExpressionDefault(ctx *ValueExpressionDefaultContext) interface{}

	// Visit a parse tree produced by DorisParser#comparison.
	VisitComparison(ctx *ComparisonContext) interface{}

	// Visit a parse tree produced by DorisParser#arithmeticBinary.
	VisitArithmeticBinary(ctx *ArithmeticBinaryContext) interface{}

	// Visit a parse tree produced by DorisParser#arithmeticUnary.
	VisitArithmeticUnary(ctx *ArithmeticUnaryContext) interface{}

	// Visit a parse tree produced by DorisParser#dereference.
	VisitDereference(ctx *DereferenceContext) interface{}

	// Visit a parse tree produced by DorisParser#currentDate.
	VisitCurrentDate(ctx *CurrentDateContext) interface{}

	// Visit a parse tree produced by DorisParser#cast.
	VisitCast(ctx *CastContext) interface{}

	// Visit a parse tree produced by DorisParser#parenthesizedExpression.
	VisitParenthesizedExpression(ctx *ParenthesizedExpressionContext) interface{}

	// Visit a parse tree produced by DorisParser#userVariable.
	VisitUserVariable(ctx *UserVariableContext) interface{}

	// Visit a parse tree produced by DorisParser#elementAt.
	VisitElementAt(ctx *ElementAtContext) interface{}

	// Visit a parse tree produced by DorisParser#localTimestamp.
	VisitLocalTimestamp(ctx *LocalTimestampContext) interface{}

	// Visit a parse tree produced by DorisParser#charFunction.
	VisitCharFunction(ctx *CharFunctionContext) interface{}

	// Visit a parse tree produced by DorisParser#intervalLiteral.
	VisitIntervalLiteral(ctx *IntervalLiteralContext) interface{}

	// Visit a parse tree produced by DorisParser#simpleCase.
	VisitSimpleCase(ctx *SimpleCaseContext) interface{}

	// Visit a parse tree produced by DorisParser#columnReference.
	VisitColumnReference(ctx *ColumnReferenceContext) interface{}

	// Visit a parse tree produced by DorisParser#star.
	VisitStar(ctx *StarContext) interface{}

	// Visit a parse tree produced by DorisParser#sessionUser.
	VisitSessionUser(ctx *SessionUserContext) interface{}

	// Visit a parse tree produced by DorisParser#convertType.
	VisitConvertType(ctx *ConvertTypeContext) interface{}

	// Visit a parse tree produced by DorisParser#convertCharSet.
	VisitConvertCharSet(ctx *ConvertCharSetContext) interface{}

	// Visit a parse tree produced by DorisParser#subqueryExpression.
	VisitSubqueryExpression(ctx *SubqueryExpressionContext) interface{}

	// Visit a parse tree produced by DorisParser#encryptKey.
	VisitEncryptKey(ctx *EncryptKeyContext) interface{}

	// Visit a parse tree produced by DorisParser#currentTime.
	VisitCurrentTime(ctx *CurrentTimeContext) interface{}

	// Visit a parse tree produced by DorisParser#localTime.
	VisitLocalTime(ctx *LocalTimeContext) interface{}

	// Visit a parse tree produced by DorisParser#systemVariable.
	VisitSystemVariable(ctx *SystemVariableContext) interface{}

	// Visit a parse tree produced by DorisParser#collate.
	VisitCollate(ctx *CollateContext) interface{}

	// Visit a parse tree produced by DorisParser#currentUser.
	VisitCurrentUser(ctx *CurrentUserContext) interface{}

	// Visit a parse tree produced by DorisParser#constantDefault.
	VisitConstantDefault(ctx *ConstantDefaultContext) interface{}

	// Visit a parse tree produced by DorisParser#extract.
	VisitExtract(ctx *ExtractContext) interface{}

	// Visit a parse tree produced by DorisParser#currentTimestamp.
	VisitCurrentTimestamp(ctx *CurrentTimestampContext) interface{}

	// Visit a parse tree produced by DorisParser#functionCall.
	VisitFunctionCall(ctx *FunctionCallContext) interface{}

	// Visit a parse tree produced by DorisParser#arraySlice.
	VisitArraySlice(ctx *ArraySliceContext) interface{}

	// Visit a parse tree produced by DorisParser#searchedCase.
	VisitSearchedCase(ctx *SearchedCaseContext) interface{}

	// Visit a parse tree produced by DorisParser#except.
	VisitExcept(ctx *ExceptContext) interface{}

	// Visit a parse tree produced by DorisParser#replace.
	VisitReplace(ctx *ReplaceContext) interface{}

	// Visit a parse tree produced by DorisParser#castDataType.
	VisitCastDataType(ctx *CastDataTypeContext) interface{}

	// Visit a parse tree produced by DorisParser#functionCallExpression.
	VisitFunctionCallExpression(ctx *FunctionCallExpressionContext) interface{}

	// Visit a parse tree produced by DorisParser#functionIdentifier.
	VisitFunctionIdentifier(ctx *FunctionIdentifierContext) interface{}

	// Visit a parse tree produced by DorisParser#functionNameIdentifier.
	VisitFunctionNameIdentifier(ctx *FunctionNameIdentifierContext) interface{}

	// Visit a parse tree produced by DorisParser#windowSpec.
	VisitWindowSpec(ctx *WindowSpecContext) interface{}

	// Visit a parse tree produced by DorisParser#windowFrame.
	VisitWindowFrame(ctx *WindowFrameContext) interface{}

	// Visit a parse tree produced by DorisParser#frameUnits.
	VisitFrameUnits(ctx *FrameUnitsContext) interface{}

	// Visit a parse tree produced by DorisParser#frameBoundary.
	VisitFrameBoundary(ctx *FrameBoundaryContext) interface{}

	// Visit a parse tree produced by DorisParser#qualifiedName.
	VisitQualifiedName(ctx *QualifiedNameContext) interface{}

	// Visit a parse tree produced by DorisParser#specifiedPartition.
	VisitSpecifiedPartition(ctx *SpecifiedPartitionContext) interface{}

	// Visit a parse tree produced by DorisParser#nullLiteral.
	VisitNullLiteral(ctx *NullLiteralContext) interface{}

	// Visit a parse tree produced by DorisParser#typeConstructor.
	VisitTypeConstructor(ctx *TypeConstructorContext) interface{}

	// Visit a parse tree produced by DorisParser#numericLiteral.
	VisitNumericLiteral(ctx *NumericLiteralContext) interface{}

	// Visit a parse tree produced by DorisParser#booleanLiteral.
	VisitBooleanLiteral(ctx *BooleanLiteralContext) interface{}

	// Visit a parse tree produced by DorisParser#stringLiteral.
	VisitStringLiteral(ctx *StringLiteralContext) interface{}

	// Visit a parse tree produced by DorisParser#arrayLiteral.
	VisitArrayLiteral(ctx *ArrayLiteralContext) interface{}

	// Visit a parse tree produced by DorisParser#mapLiteral.
	VisitMapLiteral(ctx *MapLiteralContext) interface{}

	// Visit a parse tree produced by DorisParser#structLiteral.
	VisitStructLiteral(ctx *StructLiteralContext) interface{}

	// Visit a parse tree produced by DorisParser#placeholder.
	VisitPlaceholder(ctx *PlaceholderContext) interface{}

	// Visit a parse tree produced by DorisParser#comparisonOperator.
	VisitComparisonOperator(ctx *ComparisonOperatorContext) interface{}

	// Visit a parse tree produced by DorisParser#booleanValue.
	VisitBooleanValue(ctx *BooleanValueContext) interface{}

	// Visit a parse tree produced by DorisParser#whenClause.
	VisitWhenClause(ctx *WhenClauseContext) interface{}

	// Visit a parse tree produced by DorisParser#interval.
	VisitInterval(ctx *IntervalContext) interface{}

	// Visit a parse tree produced by DorisParser#unitIdentifier.
	VisitUnitIdentifier(ctx *UnitIdentifierContext) interface{}

	// Visit a parse tree produced by DorisParser#dataTypeWithNullable.
	VisitDataTypeWithNullable(ctx *DataTypeWithNullableContext) interface{}

	// Visit a parse tree produced by DorisParser#complexDataType.
	VisitComplexDataType(ctx *ComplexDataTypeContext) interface{}

	// Visit a parse tree produced by DorisParser#aggStateDataType.
	VisitAggStateDataType(ctx *AggStateDataTypeContext) interface{}

	// Visit a parse tree produced by DorisParser#primitiveDataType.
	VisitPrimitiveDataType(ctx *PrimitiveDataTypeContext) interface{}

	// Visit a parse tree produced by DorisParser#primitiveColType.
	VisitPrimitiveColType(ctx *PrimitiveColTypeContext) interface{}

	// Visit a parse tree produced by DorisParser#complexColTypeList.
	VisitComplexColTypeList(ctx *ComplexColTypeListContext) interface{}

	// Visit a parse tree produced by DorisParser#complexColType.
	VisitComplexColType(ctx *ComplexColTypeContext) interface{}

	// Visit a parse tree produced by DorisParser#commentSpec.
	VisitCommentSpec(ctx *CommentSpecContext) interface{}

	// Visit a parse tree produced by DorisParser#sample.
	VisitSample(ctx *SampleContext) interface{}

	// Visit a parse tree produced by DorisParser#sampleByPercentile.
	VisitSampleByPercentile(ctx *SampleByPercentileContext) interface{}

	// Visit a parse tree produced by DorisParser#sampleByRows.
	VisitSampleByRows(ctx *SampleByRowsContext) interface{}

	// Visit a parse tree produced by DorisParser#tableSnapshot.
	VisitTableSnapshot(ctx *TableSnapshotContext) interface{}

	// Visit a parse tree produced by DorisParser#errorCapturingIdentifier.
	VisitErrorCapturingIdentifier(ctx *ErrorCapturingIdentifierContext) interface{}

	// Visit a parse tree produced by DorisParser#errorIdent.
	VisitErrorIdent(ctx *ErrorIdentContext) interface{}

	// Visit a parse tree produced by DorisParser#realIdent.
	VisitRealIdent(ctx *RealIdentContext) interface{}

	// Visit a parse tree produced by DorisParser#identifier.
	VisitIdentifier(ctx *IdentifierContext) interface{}

	// Visit a parse tree produced by DorisParser#unquotedIdentifier.
	VisitUnquotedIdentifier(ctx *UnquotedIdentifierContext) interface{}

	// Visit a parse tree produced by DorisParser#quotedIdentifierAlternative.
	VisitQuotedIdentifierAlternative(ctx *QuotedIdentifierAlternativeContext) interface{}

	// Visit a parse tree produced by DorisParser#quotedIdentifier.
	VisitQuotedIdentifier(ctx *QuotedIdentifierContext) interface{}

	// Visit a parse tree produced by DorisParser#integerLiteral.
	VisitIntegerLiteral(ctx *IntegerLiteralContext) interface{}

	// Visit a parse tree produced by DorisParser#decimalLiteral.
	VisitDecimalLiteral(ctx *DecimalLiteralContext) interface{}

	// Visit a parse tree produced by DorisParser#nonReserved.
	VisitNonReserved(ctx *NonReservedContext) interface{}

}