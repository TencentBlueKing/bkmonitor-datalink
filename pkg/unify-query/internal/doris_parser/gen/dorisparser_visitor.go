// Code generated from DorisParser.g4 by ANTLR 4.13.2. DO NOT EDIT.

package gen // DorisParser
import "github.com/antlr4-go/antlr/v4"

// A complete Visitor for a parse tree produced by DorisParserParser.
type DorisParserVisitor interface {
	antlr.ParseTreeVisitor

	// Visit a parse tree produced by DorisParserParser#multiStatements.
	VisitMultiStatements(ctx *MultiStatementsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#singleStatement.
	VisitSingleStatement(ctx *SingleStatementContext) interface{}

	// Visit a parse tree produced by DorisParserParser#statementBaseAlias.
	VisitStatementBaseAlias(ctx *StatementBaseAliasContext) interface{}

	// Visit a parse tree produced by DorisParserParser#callProcedure.
	VisitCallProcedure(ctx *CallProcedureContext) interface{}

	// Visit a parse tree produced by DorisParserParser#createProcedure.
	VisitCreateProcedure(ctx *CreateProcedureContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dropProcedure.
	VisitDropProcedure(ctx *DropProcedureContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showProcedureStatus.
	VisitShowProcedureStatus(ctx *ShowProcedureStatusContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showCreateProcedure.
	VisitShowCreateProcedure(ctx *ShowCreateProcedureContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showConfig.
	VisitShowConfig(ctx *ShowConfigContext) interface{}

	// Visit a parse tree produced by DorisParserParser#statementDefault.
	VisitStatementDefault(ctx *StatementDefaultContext) interface{}

	// Visit a parse tree produced by DorisParserParser#supportedDmlStatementAlias.
	VisitSupportedDmlStatementAlias(ctx *SupportedDmlStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParserParser#supportedCreateStatementAlias.
	VisitSupportedCreateStatementAlias(ctx *SupportedCreateStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParserParser#supportedAlterStatementAlias.
	VisitSupportedAlterStatementAlias(ctx *SupportedAlterStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParserParser#materializedViewStatementAlias.
	VisitMaterializedViewStatementAlias(ctx *MaterializedViewStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParserParser#supportedJobStatementAlias.
	VisitSupportedJobStatementAlias(ctx *SupportedJobStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParserParser#constraintStatementAlias.
	VisitConstraintStatementAlias(ctx *ConstraintStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParserParser#supportedCleanStatementAlias.
	VisitSupportedCleanStatementAlias(ctx *SupportedCleanStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParserParser#supportedDescribeStatementAlias.
	VisitSupportedDescribeStatementAlias(ctx *SupportedDescribeStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParserParser#supportedDropStatementAlias.
	VisitSupportedDropStatementAlias(ctx *SupportedDropStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParserParser#supportedSetStatementAlias.
	VisitSupportedSetStatementAlias(ctx *SupportedSetStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParserParser#supportedUnsetStatementAlias.
	VisitSupportedUnsetStatementAlias(ctx *SupportedUnsetStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParserParser#supportedRefreshStatementAlias.
	VisitSupportedRefreshStatementAlias(ctx *SupportedRefreshStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParserParser#supportedShowStatementAlias.
	VisitSupportedShowStatementAlias(ctx *SupportedShowStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParserParser#supportedLoadStatementAlias.
	VisitSupportedLoadStatementAlias(ctx *SupportedLoadStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParserParser#supportedCancelStatementAlias.
	VisitSupportedCancelStatementAlias(ctx *SupportedCancelStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParserParser#supportedRecoverStatementAlias.
	VisitSupportedRecoverStatementAlias(ctx *SupportedRecoverStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParserParser#supportedAdminStatementAlias.
	VisitSupportedAdminStatementAlias(ctx *SupportedAdminStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParserParser#supportedUseStatementAlias.
	VisitSupportedUseStatementAlias(ctx *SupportedUseStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParserParser#supportedOtherStatementAlias.
	VisitSupportedOtherStatementAlias(ctx *SupportedOtherStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParserParser#supportedKillStatementAlias.
	VisitSupportedKillStatementAlias(ctx *SupportedKillStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParserParser#supportedStatsStatementAlias.
	VisitSupportedStatsStatementAlias(ctx *SupportedStatsStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParserParser#supportedTransactionStatementAlias.
	VisitSupportedTransactionStatementAlias(ctx *SupportedTransactionStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParserParser#supportedGrantRevokeStatementAlias.
	VisitSupportedGrantRevokeStatementAlias(ctx *SupportedGrantRevokeStatementAliasContext) interface{}

	// Visit a parse tree produced by DorisParserParser#unsupported.
	VisitUnsupported(ctx *UnsupportedContext) interface{}

	// Visit a parse tree produced by DorisParserParser#unsupportedStatement.
	VisitUnsupportedStatement(ctx *UnsupportedStatementContext) interface{}

	// Visit a parse tree produced by DorisParserParser#createMTMV.
	VisitCreateMTMV(ctx *CreateMTMVContext) interface{}

	// Visit a parse tree produced by DorisParserParser#refreshMTMV.
	VisitRefreshMTMV(ctx *RefreshMTMVContext) interface{}

	// Visit a parse tree produced by DorisParserParser#alterMTMV.
	VisitAlterMTMV(ctx *AlterMTMVContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dropMTMV.
	VisitDropMTMV(ctx *DropMTMVContext) interface{}

	// Visit a parse tree produced by DorisParserParser#pauseMTMV.
	VisitPauseMTMV(ctx *PauseMTMVContext) interface{}

	// Visit a parse tree produced by DorisParserParser#resumeMTMV.
	VisitResumeMTMV(ctx *ResumeMTMVContext) interface{}

	// Visit a parse tree produced by DorisParserParser#cancelMTMVTask.
	VisitCancelMTMVTask(ctx *CancelMTMVTaskContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showCreateMTMV.
	VisitShowCreateMTMV(ctx *ShowCreateMTMVContext) interface{}

	// Visit a parse tree produced by DorisParserParser#createScheduledJob.
	VisitCreateScheduledJob(ctx *CreateScheduledJobContext) interface{}

	// Visit a parse tree produced by DorisParserParser#pauseJob.
	VisitPauseJob(ctx *PauseJobContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dropJob.
	VisitDropJob(ctx *DropJobContext) interface{}

	// Visit a parse tree produced by DorisParserParser#resumeJob.
	VisitResumeJob(ctx *ResumeJobContext) interface{}

	// Visit a parse tree produced by DorisParserParser#cancelJobTask.
	VisitCancelJobTask(ctx *CancelJobTaskContext) interface{}

	// Visit a parse tree produced by DorisParserParser#addConstraint.
	VisitAddConstraint(ctx *AddConstraintContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dropConstraint.
	VisitDropConstraint(ctx *DropConstraintContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showConstraint.
	VisitShowConstraint(ctx *ShowConstraintContext) interface{}

	// Visit a parse tree produced by DorisParserParser#insertTable.
	VisitInsertTable(ctx *InsertTableContext) interface{}

	// Visit a parse tree produced by DorisParserParser#update.
	VisitUpdate(ctx *UpdateContext) interface{}

	// Visit a parse tree produced by DorisParserParser#delete.
	VisitDelete(ctx *DeleteContext) interface{}

	// Visit a parse tree produced by DorisParserParser#load.
	VisitLoad(ctx *LoadContext) interface{}

	// Visit a parse tree produced by DorisParserParser#export.
	VisitExport(ctx *ExportContext) interface{}

	// Visit a parse tree produced by DorisParserParser#replay.
	VisitReplay(ctx *ReplayContext) interface{}

	// Visit a parse tree produced by DorisParserParser#copyInto.
	VisitCopyInto(ctx *CopyIntoContext) interface{}

	// Visit a parse tree produced by DorisParserParser#truncateTable.
	VisitTruncateTable(ctx *TruncateTableContext) interface{}

	// Visit a parse tree produced by DorisParserParser#createTable.
	VisitCreateTable(ctx *CreateTableContext) interface{}

	// Visit a parse tree produced by DorisParserParser#createView.
	VisitCreateView(ctx *CreateViewContext) interface{}

	// Visit a parse tree produced by DorisParserParser#createFile.
	VisitCreateFile(ctx *CreateFileContext) interface{}

	// Visit a parse tree produced by DorisParserParser#createTableLike.
	VisitCreateTableLike(ctx *CreateTableLikeContext) interface{}

	// Visit a parse tree produced by DorisParserParser#createRole.
	VisitCreateRole(ctx *CreateRoleContext) interface{}

	// Visit a parse tree produced by DorisParserParser#createWorkloadGroup.
	VisitCreateWorkloadGroup(ctx *CreateWorkloadGroupContext) interface{}

	// Visit a parse tree produced by DorisParserParser#createCatalog.
	VisitCreateCatalog(ctx *CreateCatalogContext) interface{}

	// Visit a parse tree produced by DorisParserParser#createRowPolicy.
	VisitCreateRowPolicy(ctx *CreateRowPolicyContext) interface{}

	// Visit a parse tree produced by DorisParserParser#createStoragePolicy.
	VisitCreateStoragePolicy(ctx *CreateStoragePolicyContext) interface{}

	// Visit a parse tree produced by DorisParserParser#buildIndex.
	VisitBuildIndex(ctx *BuildIndexContext) interface{}

	// Visit a parse tree produced by DorisParserParser#createIndex.
	VisitCreateIndex(ctx *CreateIndexContext) interface{}

	// Visit a parse tree produced by DorisParserParser#createWorkloadPolicy.
	VisitCreateWorkloadPolicy(ctx *CreateWorkloadPolicyContext) interface{}

	// Visit a parse tree produced by DorisParserParser#createSqlBlockRule.
	VisitCreateSqlBlockRule(ctx *CreateSqlBlockRuleContext) interface{}

	// Visit a parse tree produced by DorisParserParser#createEncryptkey.
	VisitCreateEncryptkey(ctx *CreateEncryptkeyContext) interface{}

	// Visit a parse tree produced by DorisParserParser#createUserDefineFunction.
	VisitCreateUserDefineFunction(ctx *CreateUserDefineFunctionContext) interface{}

	// Visit a parse tree produced by DorisParserParser#createAliasFunction.
	VisitCreateAliasFunction(ctx *CreateAliasFunctionContext) interface{}

	// Visit a parse tree produced by DorisParserParser#createUser.
	VisitCreateUser(ctx *CreateUserContext) interface{}

	// Visit a parse tree produced by DorisParserParser#createDatabase.
	VisitCreateDatabase(ctx *CreateDatabaseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#createRepository.
	VisitCreateRepository(ctx *CreateRepositoryContext) interface{}

	// Visit a parse tree produced by DorisParserParser#createResource.
	VisitCreateResource(ctx *CreateResourceContext) interface{}

	// Visit a parse tree produced by DorisParserParser#createDictionary.
	VisitCreateDictionary(ctx *CreateDictionaryContext) interface{}

	// Visit a parse tree produced by DorisParserParser#createStage.
	VisitCreateStage(ctx *CreateStageContext) interface{}

	// Visit a parse tree produced by DorisParserParser#createStorageVault.
	VisitCreateStorageVault(ctx *CreateStorageVaultContext) interface{}

	// Visit a parse tree produced by DorisParserParser#createIndexAnalyzer.
	VisitCreateIndexAnalyzer(ctx *CreateIndexAnalyzerContext) interface{}

	// Visit a parse tree produced by DorisParserParser#createIndexTokenizer.
	VisitCreateIndexTokenizer(ctx *CreateIndexTokenizerContext) interface{}

	// Visit a parse tree produced by DorisParserParser#createIndexTokenFilter.
	VisitCreateIndexTokenFilter(ctx *CreateIndexTokenFilterContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dictionaryColumnDefs.
	VisitDictionaryColumnDefs(ctx *DictionaryColumnDefsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dictionaryColumnDef.
	VisitDictionaryColumnDef(ctx *DictionaryColumnDefContext) interface{}

	// Visit a parse tree produced by DorisParserParser#alterSystem.
	VisitAlterSystem(ctx *AlterSystemContext) interface{}

	// Visit a parse tree produced by DorisParserParser#alterView.
	VisitAlterView(ctx *AlterViewContext) interface{}

	// Visit a parse tree produced by DorisParserParser#alterCatalogRename.
	VisitAlterCatalogRename(ctx *AlterCatalogRenameContext) interface{}

	// Visit a parse tree produced by DorisParserParser#alterRole.
	VisitAlterRole(ctx *AlterRoleContext) interface{}

	// Visit a parse tree produced by DorisParserParser#alterStorageVault.
	VisitAlterStorageVault(ctx *AlterStorageVaultContext) interface{}

	// Visit a parse tree produced by DorisParserParser#alterWorkloadGroup.
	VisitAlterWorkloadGroup(ctx *AlterWorkloadGroupContext) interface{}

	// Visit a parse tree produced by DorisParserParser#alterCatalogProperties.
	VisitAlterCatalogProperties(ctx *AlterCatalogPropertiesContext) interface{}

	// Visit a parse tree produced by DorisParserParser#alterWorkloadPolicy.
	VisitAlterWorkloadPolicy(ctx *AlterWorkloadPolicyContext) interface{}

	// Visit a parse tree produced by DorisParserParser#alterSqlBlockRule.
	VisitAlterSqlBlockRule(ctx *AlterSqlBlockRuleContext) interface{}

	// Visit a parse tree produced by DorisParserParser#alterCatalogComment.
	VisitAlterCatalogComment(ctx *AlterCatalogCommentContext) interface{}

	// Visit a parse tree produced by DorisParserParser#alterDatabaseRename.
	VisitAlterDatabaseRename(ctx *AlterDatabaseRenameContext) interface{}

	// Visit a parse tree produced by DorisParserParser#alterStoragePolicy.
	VisitAlterStoragePolicy(ctx *AlterStoragePolicyContext) interface{}

	// Visit a parse tree produced by DorisParserParser#alterTable.
	VisitAlterTable(ctx *AlterTableContext) interface{}

	// Visit a parse tree produced by DorisParserParser#alterTableAddRollup.
	VisitAlterTableAddRollup(ctx *AlterTableAddRollupContext) interface{}

	// Visit a parse tree produced by DorisParserParser#alterTableDropRollup.
	VisitAlterTableDropRollup(ctx *AlterTableDropRollupContext) interface{}

	// Visit a parse tree produced by DorisParserParser#alterTableProperties.
	VisitAlterTableProperties(ctx *AlterTablePropertiesContext) interface{}

	// Visit a parse tree produced by DorisParserParser#alterDatabaseSetQuota.
	VisitAlterDatabaseSetQuota(ctx *AlterDatabaseSetQuotaContext) interface{}

	// Visit a parse tree produced by DorisParserParser#alterDatabaseProperties.
	VisitAlterDatabaseProperties(ctx *AlterDatabasePropertiesContext) interface{}

	// Visit a parse tree produced by DorisParserParser#alterSystemRenameComputeGroup.
	VisitAlterSystemRenameComputeGroup(ctx *AlterSystemRenameComputeGroupContext) interface{}

	// Visit a parse tree produced by DorisParserParser#alterResource.
	VisitAlterResource(ctx *AlterResourceContext) interface{}

	// Visit a parse tree produced by DorisParserParser#alterRepository.
	VisitAlterRepository(ctx *AlterRepositoryContext) interface{}

	// Visit a parse tree produced by DorisParserParser#alterRoutineLoad.
	VisitAlterRoutineLoad(ctx *AlterRoutineLoadContext) interface{}

	// Visit a parse tree produced by DorisParserParser#alterColocateGroup.
	VisitAlterColocateGroup(ctx *AlterColocateGroupContext) interface{}

	// Visit a parse tree produced by DorisParserParser#alterUser.
	VisitAlterUser(ctx *AlterUserContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dropCatalogRecycleBin.
	VisitDropCatalogRecycleBin(ctx *DropCatalogRecycleBinContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dropEncryptkey.
	VisitDropEncryptkey(ctx *DropEncryptkeyContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dropRole.
	VisitDropRole(ctx *DropRoleContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dropSqlBlockRule.
	VisitDropSqlBlockRule(ctx *DropSqlBlockRuleContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dropUser.
	VisitDropUser(ctx *DropUserContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dropStoragePolicy.
	VisitDropStoragePolicy(ctx *DropStoragePolicyContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dropWorkloadGroup.
	VisitDropWorkloadGroup(ctx *DropWorkloadGroupContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dropCatalog.
	VisitDropCatalog(ctx *DropCatalogContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dropFile.
	VisitDropFile(ctx *DropFileContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dropWorkloadPolicy.
	VisitDropWorkloadPolicy(ctx *DropWorkloadPolicyContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dropRepository.
	VisitDropRepository(ctx *DropRepositoryContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dropTable.
	VisitDropTable(ctx *DropTableContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dropDatabase.
	VisitDropDatabase(ctx *DropDatabaseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dropFunction.
	VisitDropFunction(ctx *DropFunctionContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dropIndex.
	VisitDropIndex(ctx *DropIndexContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dropResource.
	VisitDropResource(ctx *DropResourceContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dropRowPolicy.
	VisitDropRowPolicy(ctx *DropRowPolicyContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dropDictionary.
	VisitDropDictionary(ctx *DropDictionaryContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dropStage.
	VisitDropStage(ctx *DropStageContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dropView.
	VisitDropView(ctx *DropViewContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dropIndexAnalyzer.
	VisitDropIndexAnalyzer(ctx *DropIndexAnalyzerContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dropIndexTokenizer.
	VisitDropIndexTokenizer(ctx *DropIndexTokenizerContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dropIndexTokenFilter.
	VisitDropIndexTokenFilter(ctx *DropIndexTokenFilterContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showVariables.
	VisitShowVariables(ctx *ShowVariablesContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showAuthors.
	VisitShowAuthors(ctx *ShowAuthorsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showAlterTable.
	VisitShowAlterTable(ctx *ShowAlterTableContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showCreateDatabase.
	VisitShowCreateDatabase(ctx *ShowCreateDatabaseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showBackup.
	VisitShowBackup(ctx *ShowBackupContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showBroker.
	VisitShowBroker(ctx *ShowBrokerContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showBuildIndex.
	VisitShowBuildIndex(ctx *ShowBuildIndexContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showDynamicPartition.
	VisitShowDynamicPartition(ctx *ShowDynamicPartitionContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showEvents.
	VisitShowEvents(ctx *ShowEventsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showExport.
	VisitShowExport(ctx *ShowExportContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showLastInsert.
	VisitShowLastInsert(ctx *ShowLastInsertContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showCharset.
	VisitShowCharset(ctx *ShowCharsetContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showDelete.
	VisitShowDelete(ctx *ShowDeleteContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showCreateFunction.
	VisitShowCreateFunction(ctx *ShowCreateFunctionContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showFunctions.
	VisitShowFunctions(ctx *ShowFunctionsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showGlobalFunctions.
	VisitShowGlobalFunctions(ctx *ShowGlobalFunctionsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showGrants.
	VisitShowGrants(ctx *ShowGrantsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showGrantsForUser.
	VisitShowGrantsForUser(ctx *ShowGrantsForUserContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showCreateUser.
	VisitShowCreateUser(ctx *ShowCreateUserContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showSnapshot.
	VisitShowSnapshot(ctx *ShowSnapshotContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showLoadProfile.
	VisitShowLoadProfile(ctx *ShowLoadProfileContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showCreateRepository.
	VisitShowCreateRepository(ctx *ShowCreateRepositoryContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showView.
	VisitShowView(ctx *ShowViewContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showPlugins.
	VisitShowPlugins(ctx *ShowPluginsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showStorageVault.
	VisitShowStorageVault(ctx *ShowStorageVaultContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showRepositories.
	VisitShowRepositories(ctx *ShowRepositoriesContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showEncryptKeys.
	VisitShowEncryptKeys(ctx *ShowEncryptKeysContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showCreateTable.
	VisitShowCreateTable(ctx *ShowCreateTableContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showProcessList.
	VisitShowProcessList(ctx *ShowProcessListContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showPartitions.
	VisitShowPartitions(ctx *ShowPartitionsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showRestore.
	VisitShowRestore(ctx *ShowRestoreContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showRoles.
	VisitShowRoles(ctx *ShowRolesContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showPartitionId.
	VisitShowPartitionId(ctx *ShowPartitionIdContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showPrivileges.
	VisitShowPrivileges(ctx *ShowPrivilegesContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showProc.
	VisitShowProc(ctx *ShowProcContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showSmallFiles.
	VisitShowSmallFiles(ctx *ShowSmallFilesContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showStorageEngines.
	VisitShowStorageEngines(ctx *ShowStorageEnginesContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showCreateCatalog.
	VisitShowCreateCatalog(ctx *ShowCreateCatalogContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showCatalog.
	VisitShowCatalog(ctx *ShowCatalogContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showCatalogs.
	VisitShowCatalogs(ctx *ShowCatalogsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showUserProperties.
	VisitShowUserProperties(ctx *ShowUserPropertiesContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showAllProperties.
	VisitShowAllProperties(ctx *ShowAllPropertiesContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showCollation.
	VisitShowCollation(ctx *ShowCollationContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showRowPolicy.
	VisitShowRowPolicy(ctx *ShowRowPolicyContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showStoragePolicy.
	VisitShowStoragePolicy(ctx *ShowStoragePolicyContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showSqlBlockRule.
	VisitShowSqlBlockRule(ctx *ShowSqlBlockRuleContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showCreateView.
	VisitShowCreateView(ctx *ShowCreateViewContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showDataTypes.
	VisitShowDataTypes(ctx *ShowDataTypesContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showData.
	VisitShowData(ctx *ShowDataContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showCreateMaterializedView.
	VisitShowCreateMaterializedView(ctx *ShowCreateMaterializedViewContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showWarningErrors.
	VisitShowWarningErrors(ctx *ShowWarningErrorsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showWarningErrorCount.
	VisitShowWarningErrorCount(ctx *ShowWarningErrorCountContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showBackends.
	VisitShowBackends(ctx *ShowBackendsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showStages.
	VisitShowStages(ctx *ShowStagesContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showReplicaDistribution.
	VisitShowReplicaDistribution(ctx *ShowReplicaDistributionContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showResources.
	VisitShowResources(ctx *ShowResourcesContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showLoad.
	VisitShowLoad(ctx *ShowLoadContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showLoadWarings.
	VisitShowLoadWarings(ctx *ShowLoadWaringsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showTriggers.
	VisitShowTriggers(ctx *ShowTriggersContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showDiagnoseTablet.
	VisitShowDiagnoseTablet(ctx *ShowDiagnoseTabletContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showOpenTables.
	VisitShowOpenTables(ctx *ShowOpenTablesContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showFrontends.
	VisitShowFrontends(ctx *ShowFrontendsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showDatabaseId.
	VisitShowDatabaseId(ctx *ShowDatabaseIdContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showColumns.
	VisitShowColumns(ctx *ShowColumnsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showTableId.
	VisitShowTableId(ctx *ShowTableIdContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showTrash.
	VisitShowTrash(ctx *ShowTrashContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showTypeCast.
	VisitShowTypeCast(ctx *ShowTypeCastContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showClusters.
	VisitShowClusters(ctx *ShowClustersContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showStatus.
	VisitShowStatus(ctx *ShowStatusContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showWhitelist.
	VisitShowWhitelist(ctx *ShowWhitelistContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showTabletsBelong.
	VisitShowTabletsBelong(ctx *ShowTabletsBelongContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showDataSkew.
	VisitShowDataSkew(ctx *ShowDataSkewContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showTableCreation.
	VisitShowTableCreation(ctx *ShowTableCreationContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showTabletStorageFormat.
	VisitShowTabletStorageFormat(ctx *ShowTabletStorageFormatContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showQueryProfile.
	VisitShowQueryProfile(ctx *ShowQueryProfileContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showConvertLsc.
	VisitShowConvertLsc(ctx *ShowConvertLscContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showTables.
	VisitShowTables(ctx *ShowTablesContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showViews.
	VisitShowViews(ctx *ShowViewsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showTableStatus.
	VisitShowTableStatus(ctx *ShowTableStatusContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showDatabases.
	VisitShowDatabases(ctx *ShowDatabasesContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showTabletsFromTable.
	VisitShowTabletsFromTable(ctx *ShowTabletsFromTableContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showCatalogRecycleBin.
	VisitShowCatalogRecycleBin(ctx *ShowCatalogRecycleBinContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showTabletId.
	VisitShowTabletId(ctx *ShowTabletIdContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showDictionaries.
	VisitShowDictionaries(ctx *ShowDictionariesContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showTransaction.
	VisitShowTransaction(ctx *ShowTransactionContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showReplicaStatus.
	VisitShowReplicaStatus(ctx *ShowReplicaStatusContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showWorkloadGroups.
	VisitShowWorkloadGroups(ctx *ShowWorkloadGroupsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showCopy.
	VisitShowCopy(ctx *ShowCopyContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showQueryStats.
	VisitShowQueryStats(ctx *ShowQueryStatsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showIndex.
	VisitShowIndex(ctx *ShowIndexContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showWarmUpJob.
	VisitShowWarmUpJob(ctx *ShowWarmUpJobContext) interface{}

	// Visit a parse tree produced by DorisParserParser#sync.
	VisitSync(ctx *SyncContext) interface{}

	// Visit a parse tree produced by DorisParserParser#createRoutineLoadAlias.
	VisitCreateRoutineLoadAlias(ctx *CreateRoutineLoadAliasContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showCreateRoutineLoad.
	VisitShowCreateRoutineLoad(ctx *ShowCreateRoutineLoadContext) interface{}

	// Visit a parse tree produced by DorisParserParser#pauseRoutineLoad.
	VisitPauseRoutineLoad(ctx *PauseRoutineLoadContext) interface{}

	// Visit a parse tree produced by DorisParserParser#pauseAllRoutineLoad.
	VisitPauseAllRoutineLoad(ctx *PauseAllRoutineLoadContext) interface{}

	// Visit a parse tree produced by DorisParserParser#resumeRoutineLoad.
	VisitResumeRoutineLoad(ctx *ResumeRoutineLoadContext) interface{}

	// Visit a parse tree produced by DorisParserParser#resumeAllRoutineLoad.
	VisitResumeAllRoutineLoad(ctx *ResumeAllRoutineLoadContext) interface{}

	// Visit a parse tree produced by DorisParserParser#stopRoutineLoad.
	VisitStopRoutineLoad(ctx *StopRoutineLoadContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showRoutineLoad.
	VisitShowRoutineLoad(ctx *ShowRoutineLoadContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showRoutineLoadTask.
	VisitShowRoutineLoadTask(ctx *ShowRoutineLoadTaskContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showIndexAnalyzer.
	VisitShowIndexAnalyzer(ctx *ShowIndexAnalyzerContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showIndexTokenizer.
	VisitShowIndexTokenizer(ctx *ShowIndexTokenizerContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showIndexTokenFilter.
	VisitShowIndexTokenFilter(ctx *ShowIndexTokenFilterContext) interface{}

	// Visit a parse tree produced by DorisParserParser#killConnection.
	VisitKillConnection(ctx *KillConnectionContext) interface{}

	// Visit a parse tree produced by DorisParserParser#killQuery.
	VisitKillQuery(ctx *KillQueryContext) interface{}

	// Visit a parse tree produced by DorisParserParser#help.
	VisitHelp(ctx *HelpContext) interface{}

	// Visit a parse tree produced by DorisParserParser#unlockTables.
	VisitUnlockTables(ctx *UnlockTablesContext) interface{}

	// Visit a parse tree produced by DorisParserParser#installPlugin.
	VisitInstallPlugin(ctx *InstallPluginContext) interface{}

	// Visit a parse tree produced by DorisParserParser#uninstallPlugin.
	VisitUninstallPlugin(ctx *UninstallPluginContext) interface{}

	// Visit a parse tree produced by DorisParserParser#lockTables.
	VisitLockTables(ctx *LockTablesContext) interface{}

	// Visit a parse tree produced by DorisParserParser#restore.
	VisitRestore(ctx *RestoreContext) interface{}

	// Visit a parse tree produced by DorisParserParser#warmUpCluster.
	VisitWarmUpCluster(ctx *WarmUpClusterContext) interface{}

	// Visit a parse tree produced by DorisParserParser#backup.
	VisitBackup(ctx *BackupContext) interface{}

	// Visit a parse tree produced by DorisParserParser#unsupportedStartTransaction.
	VisitUnsupportedStartTransaction(ctx *UnsupportedStartTransactionContext) interface{}

	// Visit a parse tree produced by DorisParserParser#warmUpItem.
	VisitWarmUpItem(ctx *WarmUpItemContext) interface{}

	// Visit a parse tree produced by DorisParserParser#lockTable.
	VisitLockTable(ctx *LockTableContext) interface{}

	// Visit a parse tree produced by DorisParserParser#createRoutineLoad.
	VisitCreateRoutineLoad(ctx *CreateRoutineLoadContext) interface{}

	// Visit a parse tree produced by DorisParserParser#mysqlLoad.
	VisitMysqlLoad(ctx *MysqlLoadContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showCreateLoad.
	VisitShowCreateLoad(ctx *ShowCreateLoadContext) interface{}

	// Visit a parse tree produced by DorisParserParser#separator.
	VisitSeparator(ctx *SeparatorContext) interface{}

	// Visit a parse tree produced by DorisParserParser#importColumns.
	VisitImportColumns(ctx *ImportColumnsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#importPrecedingFilter.
	VisitImportPrecedingFilter(ctx *ImportPrecedingFilterContext) interface{}

	// Visit a parse tree produced by DorisParserParser#importWhere.
	VisitImportWhere(ctx *ImportWhereContext) interface{}

	// Visit a parse tree produced by DorisParserParser#importDeleteOn.
	VisitImportDeleteOn(ctx *ImportDeleteOnContext) interface{}

	// Visit a parse tree produced by DorisParserParser#importSequence.
	VisitImportSequence(ctx *ImportSequenceContext) interface{}

	// Visit a parse tree produced by DorisParserParser#importPartitions.
	VisitImportPartitions(ctx *ImportPartitionsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#importSequenceStatement.
	VisitImportSequenceStatement(ctx *ImportSequenceStatementContext) interface{}

	// Visit a parse tree produced by DorisParserParser#importDeleteOnStatement.
	VisitImportDeleteOnStatement(ctx *ImportDeleteOnStatementContext) interface{}

	// Visit a parse tree produced by DorisParserParser#importWhereStatement.
	VisitImportWhereStatement(ctx *ImportWhereStatementContext) interface{}

	// Visit a parse tree produced by DorisParserParser#importPrecedingFilterStatement.
	VisitImportPrecedingFilterStatement(ctx *ImportPrecedingFilterStatementContext) interface{}

	// Visit a parse tree produced by DorisParserParser#importColumnsStatement.
	VisitImportColumnsStatement(ctx *ImportColumnsStatementContext) interface{}

	// Visit a parse tree produced by DorisParserParser#importColumnDesc.
	VisitImportColumnDesc(ctx *ImportColumnDescContext) interface{}

	// Visit a parse tree produced by DorisParserParser#refreshCatalog.
	VisitRefreshCatalog(ctx *RefreshCatalogContext) interface{}

	// Visit a parse tree produced by DorisParserParser#refreshDatabase.
	VisitRefreshDatabase(ctx *RefreshDatabaseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#refreshTable.
	VisitRefreshTable(ctx *RefreshTableContext) interface{}

	// Visit a parse tree produced by DorisParserParser#refreshDictionary.
	VisitRefreshDictionary(ctx *RefreshDictionaryContext) interface{}

	// Visit a parse tree produced by DorisParserParser#refreshLdap.
	VisitRefreshLdap(ctx *RefreshLdapContext) interface{}

	// Visit a parse tree produced by DorisParserParser#cleanAllProfile.
	VisitCleanAllProfile(ctx *CleanAllProfileContext) interface{}

	// Visit a parse tree produced by DorisParserParser#cleanLabel.
	VisitCleanLabel(ctx *CleanLabelContext) interface{}

	// Visit a parse tree produced by DorisParserParser#cleanQueryStats.
	VisitCleanQueryStats(ctx *CleanQueryStatsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#cleanAllQueryStats.
	VisitCleanAllQueryStats(ctx *CleanAllQueryStatsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#cancelLoad.
	VisitCancelLoad(ctx *CancelLoadContext) interface{}

	// Visit a parse tree produced by DorisParserParser#cancelExport.
	VisitCancelExport(ctx *CancelExportContext) interface{}

	// Visit a parse tree produced by DorisParserParser#cancelWarmUpJob.
	VisitCancelWarmUpJob(ctx *CancelWarmUpJobContext) interface{}

	// Visit a parse tree produced by DorisParserParser#cancelDecommisionBackend.
	VisitCancelDecommisionBackend(ctx *CancelDecommisionBackendContext) interface{}

	// Visit a parse tree produced by DorisParserParser#cancelBackup.
	VisitCancelBackup(ctx *CancelBackupContext) interface{}

	// Visit a parse tree produced by DorisParserParser#cancelRestore.
	VisitCancelRestore(ctx *CancelRestoreContext) interface{}

	// Visit a parse tree produced by DorisParserParser#cancelBuildIndex.
	VisitCancelBuildIndex(ctx *CancelBuildIndexContext) interface{}

	// Visit a parse tree produced by DorisParserParser#cancelAlterTable.
	VisitCancelAlterTable(ctx *CancelAlterTableContext) interface{}

	// Visit a parse tree produced by DorisParserParser#adminShowReplicaDistribution.
	VisitAdminShowReplicaDistribution(ctx *AdminShowReplicaDistributionContext) interface{}

	// Visit a parse tree produced by DorisParserParser#adminRebalanceDisk.
	VisitAdminRebalanceDisk(ctx *AdminRebalanceDiskContext) interface{}

	// Visit a parse tree produced by DorisParserParser#adminCancelRebalanceDisk.
	VisitAdminCancelRebalanceDisk(ctx *AdminCancelRebalanceDiskContext) interface{}

	// Visit a parse tree produced by DorisParserParser#adminDiagnoseTablet.
	VisitAdminDiagnoseTablet(ctx *AdminDiagnoseTabletContext) interface{}

	// Visit a parse tree produced by DorisParserParser#adminShowReplicaStatus.
	VisitAdminShowReplicaStatus(ctx *AdminShowReplicaStatusContext) interface{}

	// Visit a parse tree produced by DorisParserParser#adminCompactTable.
	VisitAdminCompactTable(ctx *AdminCompactTableContext) interface{}

	// Visit a parse tree produced by DorisParserParser#adminCheckTablets.
	VisitAdminCheckTablets(ctx *AdminCheckTabletsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#adminShowTabletStorageFormat.
	VisitAdminShowTabletStorageFormat(ctx *AdminShowTabletStorageFormatContext) interface{}

	// Visit a parse tree produced by DorisParserParser#adminSetFrontendConfig.
	VisitAdminSetFrontendConfig(ctx *AdminSetFrontendConfigContext) interface{}

	// Visit a parse tree produced by DorisParserParser#adminCleanTrash.
	VisitAdminCleanTrash(ctx *AdminCleanTrashContext) interface{}

	// Visit a parse tree produced by DorisParserParser#adminSetReplicaVersion.
	VisitAdminSetReplicaVersion(ctx *AdminSetReplicaVersionContext) interface{}

	// Visit a parse tree produced by DorisParserParser#adminSetTableStatus.
	VisitAdminSetTableStatus(ctx *AdminSetTableStatusContext) interface{}

	// Visit a parse tree produced by DorisParserParser#adminSetReplicaStatus.
	VisitAdminSetReplicaStatus(ctx *AdminSetReplicaStatusContext) interface{}

	// Visit a parse tree produced by DorisParserParser#adminRepairTable.
	VisitAdminRepairTable(ctx *AdminRepairTableContext) interface{}

	// Visit a parse tree produced by DorisParserParser#adminCancelRepairTable.
	VisitAdminCancelRepairTable(ctx *AdminCancelRepairTableContext) interface{}

	// Visit a parse tree produced by DorisParserParser#adminCopyTablet.
	VisitAdminCopyTablet(ctx *AdminCopyTabletContext) interface{}

	// Visit a parse tree produced by DorisParserParser#recoverDatabase.
	VisitRecoverDatabase(ctx *RecoverDatabaseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#recoverTable.
	VisitRecoverTable(ctx *RecoverTableContext) interface{}

	// Visit a parse tree produced by DorisParserParser#recoverPartition.
	VisitRecoverPartition(ctx *RecoverPartitionContext) interface{}

	// Visit a parse tree produced by DorisParserParser#adminSetPartitionVersion.
	VisitAdminSetPartitionVersion(ctx *AdminSetPartitionVersionContext) interface{}

	// Visit a parse tree produced by DorisParserParser#baseTableRef.
	VisitBaseTableRef(ctx *BaseTableRefContext) interface{}

	// Visit a parse tree produced by DorisParserParser#wildWhere.
	VisitWildWhere(ctx *WildWhereContext) interface{}

	// Visit a parse tree produced by DorisParserParser#transactionBegin.
	VisitTransactionBegin(ctx *TransactionBeginContext) interface{}

	// Visit a parse tree produced by DorisParserParser#transcationCommit.
	VisitTranscationCommit(ctx *TranscationCommitContext) interface{}

	// Visit a parse tree produced by DorisParserParser#transactionRollback.
	VisitTransactionRollback(ctx *TransactionRollbackContext) interface{}

	// Visit a parse tree produced by DorisParserParser#grantTablePrivilege.
	VisitGrantTablePrivilege(ctx *GrantTablePrivilegeContext) interface{}

	// Visit a parse tree produced by DorisParserParser#grantResourcePrivilege.
	VisitGrantResourcePrivilege(ctx *GrantResourcePrivilegeContext) interface{}

	// Visit a parse tree produced by DorisParserParser#grantRole.
	VisitGrantRole(ctx *GrantRoleContext) interface{}

	// Visit a parse tree produced by DorisParserParser#revokeRole.
	VisitRevokeRole(ctx *RevokeRoleContext) interface{}

	// Visit a parse tree produced by DorisParserParser#revokeResourcePrivilege.
	VisitRevokeResourcePrivilege(ctx *RevokeResourcePrivilegeContext) interface{}

	// Visit a parse tree produced by DorisParserParser#revokeTablePrivilege.
	VisitRevokeTablePrivilege(ctx *RevokeTablePrivilegeContext) interface{}

	// Visit a parse tree produced by DorisParserParser#privilege.
	VisitPrivilege(ctx *PrivilegeContext) interface{}

	// Visit a parse tree produced by DorisParserParser#privilegeList.
	VisitPrivilegeList(ctx *PrivilegeListContext) interface{}

	// Visit a parse tree produced by DorisParserParser#addBackendClause.
	VisitAddBackendClause(ctx *AddBackendClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dropBackendClause.
	VisitDropBackendClause(ctx *DropBackendClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#decommissionBackendClause.
	VisitDecommissionBackendClause(ctx *DecommissionBackendClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#addObserverClause.
	VisitAddObserverClause(ctx *AddObserverClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dropObserverClause.
	VisitDropObserverClause(ctx *DropObserverClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#addFollowerClause.
	VisitAddFollowerClause(ctx *AddFollowerClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dropFollowerClause.
	VisitDropFollowerClause(ctx *DropFollowerClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#addBrokerClause.
	VisitAddBrokerClause(ctx *AddBrokerClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dropBrokerClause.
	VisitDropBrokerClause(ctx *DropBrokerClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dropAllBrokerClause.
	VisitDropAllBrokerClause(ctx *DropAllBrokerClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#alterLoadErrorUrlClause.
	VisitAlterLoadErrorUrlClause(ctx *AlterLoadErrorUrlClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#modifyBackendClause.
	VisitModifyBackendClause(ctx *ModifyBackendClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#modifyFrontendOrBackendHostNameClause.
	VisitModifyFrontendOrBackendHostNameClause(ctx *ModifyFrontendOrBackendHostNameClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dropRollupClause.
	VisitDropRollupClause(ctx *DropRollupClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#addRollupClause.
	VisitAddRollupClause(ctx *AddRollupClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#addColumnClause.
	VisitAddColumnClause(ctx *AddColumnClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#addColumnsClause.
	VisitAddColumnsClause(ctx *AddColumnsClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dropColumnClause.
	VisitDropColumnClause(ctx *DropColumnClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#modifyColumnClause.
	VisitModifyColumnClause(ctx *ModifyColumnClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#reorderColumnsClause.
	VisitReorderColumnsClause(ctx *ReorderColumnsClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#addPartitionClause.
	VisitAddPartitionClause(ctx *AddPartitionClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dropPartitionClause.
	VisitDropPartitionClause(ctx *DropPartitionClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#modifyPartitionClause.
	VisitModifyPartitionClause(ctx *ModifyPartitionClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#replacePartitionClause.
	VisitReplacePartitionClause(ctx *ReplacePartitionClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#replaceTableClause.
	VisitReplaceTableClause(ctx *ReplaceTableClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#renameClause.
	VisitRenameClause(ctx *RenameClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#renameRollupClause.
	VisitRenameRollupClause(ctx *RenameRollupClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#renamePartitionClause.
	VisitRenamePartitionClause(ctx *RenamePartitionClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#renameColumnClause.
	VisitRenameColumnClause(ctx *RenameColumnClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#addIndexClause.
	VisitAddIndexClause(ctx *AddIndexClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dropIndexClause.
	VisitDropIndexClause(ctx *DropIndexClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#enableFeatureClause.
	VisitEnableFeatureClause(ctx *EnableFeatureClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#modifyDistributionClause.
	VisitModifyDistributionClause(ctx *ModifyDistributionClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#modifyTableCommentClause.
	VisitModifyTableCommentClause(ctx *ModifyTableCommentClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#modifyColumnCommentClause.
	VisitModifyColumnCommentClause(ctx *ModifyColumnCommentClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#modifyEngineClause.
	VisitModifyEngineClause(ctx *ModifyEngineClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#alterMultiPartitionClause.
	VisitAlterMultiPartitionClause(ctx *AlterMultiPartitionClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#createOrReplaceTagClauses.
	VisitCreateOrReplaceTagClauses(ctx *CreateOrReplaceTagClausesContext) interface{}

	// Visit a parse tree produced by DorisParserParser#createOrReplaceBranchClauses.
	VisitCreateOrReplaceBranchClauses(ctx *CreateOrReplaceBranchClausesContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dropBranchClauses.
	VisitDropBranchClauses(ctx *DropBranchClausesContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dropTagClauses.
	VisitDropTagClauses(ctx *DropTagClausesContext) interface{}

	// Visit a parse tree produced by DorisParserParser#createOrReplaceTagClause.
	VisitCreateOrReplaceTagClause(ctx *CreateOrReplaceTagClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#createOrReplaceBranchClause.
	VisitCreateOrReplaceBranchClause(ctx *CreateOrReplaceBranchClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#tagOptions.
	VisitTagOptions(ctx *TagOptionsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#branchOptions.
	VisitBranchOptions(ctx *BranchOptionsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#retainTime.
	VisitRetainTime(ctx *RetainTimeContext) interface{}

	// Visit a parse tree produced by DorisParserParser#retentionSnapshot.
	VisitRetentionSnapshot(ctx *RetentionSnapshotContext) interface{}

	// Visit a parse tree produced by DorisParserParser#minSnapshotsToKeep.
	VisitMinSnapshotsToKeep(ctx *MinSnapshotsToKeepContext) interface{}

	// Visit a parse tree produced by DorisParserParser#timeValueWithUnit.
	VisitTimeValueWithUnit(ctx *TimeValueWithUnitContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dropBranchClause.
	VisitDropBranchClause(ctx *DropBranchClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dropTagClause.
	VisitDropTagClause(ctx *DropTagClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#columnPosition.
	VisitColumnPosition(ctx *ColumnPositionContext) interface{}

	// Visit a parse tree produced by DorisParserParser#toRollup.
	VisitToRollup(ctx *ToRollupContext) interface{}

	// Visit a parse tree produced by DorisParserParser#fromRollup.
	VisitFromRollup(ctx *FromRollupContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showAnalyze.
	VisitShowAnalyze(ctx *ShowAnalyzeContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showQueuedAnalyzeJobs.
	VisitShowQueuedAnalyzeJobs(ctx *ShowQueuedAnalyzeJobsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showColumnHistogramStats.
	VisitShowColumnHistogramStats(ctx *ShowColumnHistogramStatsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#analyzeDatabase.
	VisitAnalyzeDatabase(ctx *AnalyzeDatabaseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#analyzeTable.
	VisitAnalyzeTable(ctx *AnalyzeTableContext) interface{}

	// Visit a parse tree produced by DorisParserParser#alterTableStats.
	VisitAlterTableStats(ctx *AlterTableStatsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#alterColumnStats.
	VisitAlterColumnStats(ctx *AlterColumnStatsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showIndexStats.
	VisitShowIndexStats(ctx *ShowIndexStatsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dropStats.
	VisitDropStats(ctx *DropStatsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dropCachedStats.
	VisitDropCachedStats(ctx *DropCachedStatsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dropExpiredStats.
	VisitDropExpiredStats(ctx *DropExpiredStatsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#killAnalyzeJob.
	VisitKillAnalyzeJob(ctx *KillAnalyzeJobContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dropAnalyzeJob.
	VisitDropAnalyzeJob(ctx *DropAnalyzeJobContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showTableStats.
	VisitShowTableStats(ctx *ShowTableStatsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showColumnStats.
	VisitShowColumnStats(ctx *ShowColumnStatsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#showAnalyzeTask.
	VisitShowAnalyzeTask(ctx *ShowAnalyzeTaskContext) interface{}

	// Visit a parse tree produced by DorisParserParser#analyzeProperties.
	VisitAnalyzeProperties(ctx *AnalyzePropertiesContext) interface{}

	// Visit a parse tree produced by DorisParserParser#workloadPolicyActions.
	VisitWorkloadPolicyActions(ctx *WorkloadPolicyActionsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#workloadPolicyAction.
	VisitWorkloadPolicyAction(ctx *WorkloadPolicyActionContext) interface{}

	// Visit a parse tree produced by DorisParserParser#workloadPolicyConditions.
	VisitWorkloadPolicyConditions(ctx *WorkloadPolicyConditionsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#workloadPolicyCondition.
	VisitWorkloadPolicyCondition(ctx *WorkloadPolicyConditionContext) interface{}

	// Visit a parse tree produced by DorisParserParser#storageBackend.
	VisitStorageBackend(ctx *StorageBackendContext) interface{}

	// Visit a parse tree produced by DorisParserParser#passwordOption.
	VisitPasswordOption(ctx *PasswordOptionContext) interface{}

	// Visit a parse tree produced by DorisParserParser#functionArguments.
	VisitFunctionArguments(ctx *FunctionArgumentsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dataTypeList.
	VisitDataTypeList(ctx *DataTypeListContext) interface{}

	// Visit a parse tree produced by DorisParserParser#setOptions.
	VisitSetOptions(ctx *SetOptionsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#setDefaultStorageVault.
	VisitSetDefaultStorageVault(ctx *SetDefaultStorageVaultContext) interface{}

	// Visit a parse tree produced by DorisParserParser#setUserProperties.
	VisitSetUserProperties(ctx *SetUserPropertiesContext) interface{}

	// Visit a parse tree produced by DorisParserParser#setTransaction.
	VisitSetTransaction(ctx *SetTransactionContext) interface{}

	// Visit a parse tree produced by DorisParserParser#setVariableWithType.
	VisitSetVariableWithType(ctx *SetVariableWithTypeContext) interface{}

	// Visit a parse tree produced by DorisParserParser#setNames.
	VisitSetNames(ctx *SetNamesContext) interface{}

	// Visit a parse tree produced by DorisParserParser#setCharset.
	VisitSetCharset(ctx *SetCharsetContext) interface{}

	// Visit a parse tree produced by DorisParserParser#setCollate.
	VisitSetCollate(ctx *SetCollateContext) interface{}

	// Visit a parse tree produced by DorisParserParser#setPassword.
	VisitSetPassword(ctx *SetPasswordContext) interface{}

	// Visit a parse tree produced by DorisParserParser#setLdapAdminPassword.
	VisitSetLdapAdminPassword(ctx *SetLdapAdminPasswordContext) interface{}

	// Visit a parse tree produced by DorisParserParser#setVariableWithoutType.
	VisitSetVariableWithoutType(ctx *SetVariableWithoutTypeContext) interface{}

	// Visit a parse tree produced by DorisParserParser#setSystemVariable.
	VisitSetSystemVariable(ctx *SetSystemVariableContext) interface{}

	// Visit a parse tree produced by DorisParserParser#setUserVariable.
	VisitSetUserVariable(ctx *SetUserVariableContext) interface{}

	// Visit a parse tree produced by DorisParserParser#transactionAccessMode.
	VisitTransactionAccessMode(ctx *TransactionAccessModeContext) interface{}

	// Visit a parse tree produced by DorisParserParser#isolationLevel.
	VisitIsolationLevel(ctx *IsolationLevelContext) interface{}

	// Visit a parse tree produced by DorisParserParser#supportedUnsetStatement.
	VisitSupportedUnsetStatement(ctx *SupportedUnsetStatementContext) interface{}

	// Visit a parse tree produced by DorisParserParser#switchCatalog.
	VisitSwitchCatalog(ctx *SwitchCatalogContext) interface{}

	// Visit a parse tree produced by DorisParserParser#useDatabase.
	VisitUseDatabase(ctx *UseDatabaseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#useCloudCluster.
	VisitUseCloudCluster(ctx *UseCloudClusterContext) interface{}

	// Visit a parse tree produced by DorisParserParser#stageAndPattern.
	VisitStageAndPattern(ctx *StageAndPatternContext) interface{}

	// Visit a parse tree produced by DorisParserParser#describeTableValuedFunction.
	VisitDescribeTableValuedFunction(ctx *DescribeTableValuedFunctionContext) interface{}

	// Visit a parse tree produced by DorisParserParser#describeTableAll.
	VisitDescribeTableAll(ctx *DescribeTableAllContext) interface{}

	// Visit a parse tree produced by DorisParserParser#describeTable.
	VisitDescribeTable(ctx *DescribeTableContext) interface{}

	// Visit a parse tree produced by DorisParserParser#describeDictionary.
	VisitDescribeDictionary(ctx *DescribeDictionaryContext) interface{}

	// Visit a parse tree produced by DorisParserParser#constraint.
	VisitConstraint(ctx *ConstraintContext) interface{}

	// Visit a parse tree produced by DorisParserParser#partitionSpec.
	VisitPartitionSpec(ctx *PartitionSpecContext) interface{}

	// Visit a parse tree produced by DorisParserParser#partitionTable.
	VisitPartitionTable(ctx *PartitionTableContext) interface{}

	// Visit a parse tree produced by DorisParserParser#identityOrFunctionList.
	VisitIdentityOrFunctionList(ctx *IdentityOrFunctionListContext) interface{}

	// Visit a parse tree produced by DorisParserParser#identityOrFunction.
	VisitIdentityOrFunction(ctx *IdentityOrFunctionContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dataDesc.
	VisitDataDesc(ctx *DataDescContext) interface{}

	// Visit a parse tree produced by DorisParserParser#statementScope.
	VisitStatementScope(ctx *StatementScopeContext) interface{}

	// Visit a parse tree produced by DorisParserParser#buildMode.
	VisitBuildMode(ctx *BuildModeContext) interface{}

	// Visit a parse tree produced by DorisParserParser#refreshTrigger.
	VisitRefreshTrigger(ctx *RefreshTriggerContext) interface{}

	// Visit a parse tree produced by DorisParserParser#refreshSchedule.
	VisitRefreshSchedule(ctx *RefreshScheduleContext) interface{}

	// Visit a parse tree produced by DorisParserParser#refreshMethod.
	VisitRefreshMethod(ctx *RefreshMethodContext) interface{}

	// Visit a parse tree produced by DorisParserParser#mvPartition.
	VisitMvPartition(ctx *MvPartitionContext) interface{}

	// Visit a parse tree produced by DorisParserParser#identifierOrText.
	VisitIdentifierOrText(ctx *IdentifierOrTextContext) interface{}

	// Visit a parse tree produced by DorisParserParser#identifierOrTextOrAsterisk.
	VisitIdentifierOrTextOrAsterisk(ctx *IdentifierOrTextOrAsteriskContext) interface{}

	// Visit a parse tree produced by DorisParserParser#multipartIdentifierOrAsterisk.
	VisitMultipartIdentifierOrAsterisk(ctx *MultipartIdentifierOrAsteriskContext) interface{}

	// Visit a parse tree produced by DorisParserParser#identifierOrAsterisk.
	VisitIdentifierOrAsterisk(ctx *IdentifierOrAsteriskContext) interface{}

	// Visit a parse tree produced by DorisParserParser#userIdentify.
	VisitUserIdentify(ctx *UserIdentifyContext) interface{}

	// Visit a parse tree produced by DorisParserParser#grantUserIdentify.
	VisitGrantUserIdentify(ctx *GrantUserIdentifyContext) interface{}

	// Visit a parse tree produced by DorisParserParser#explain.
	VisitExplain(ctx *ExplainContext) interface{}

	// Visit a parse tree produced by DorisParserParser#explainCommand.
	VisitExplainCommand(ctx *ExplainCommandContext) interface{}

	// Visit a parse tree produced by DorisParserParser#planType.
	VisitPlanType(ctx *PlanTypeContext) interface{}

	// Visit a parse tree produced by DorisParserParser#replayCommand.
	VisitReplayCommand(ctx *ReplayCommandContext) interface{}

	// Visit a parse tree produced by DorisParserParser#replayType.
	VisitReplayType(ctx *ReplayTypeContext) interface{}

	// Visit a parse tree produced by DorisParserParser#mergeType.
	VisitMergeType(ctx *MergeTypeContext) interface{}

	// Visit a parse tree produced by DorisParserParser#preFilterClause.
	VisitPreFilterClause(ctx *PreFilterClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#deleteOnClause.
	VisitDeleteOnClause(ctx *DeleteOnClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#sequenceColClause.
	VisitSequenceColClause(ctx *SequenceColClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#colFromPath.
	VisitColFromPath(ctx *ColFromPathContext) interface{}

	// Visit a parse tree produced by DorisParserParser#colMappingList.
	VisitColMappingList(ctx *ColMappingListContext) interface{}

	// Visit a parse tree produced by DorisParserParser#mappingExpr.
	VisitMappingExpr(ctx *MappingExprContext) interface{}

	// Visit a parse tree produced by DorisParserParser#withRemoteStorageSystem.
	VisitWithRemoteStorageSystem(ctx *WithRemoteStorageSystemContext) interface{}

	// Visit a parse tree produced by DorisParserParser#resourceDesc.
	VisitResourceDesc(ctx *ResourceDescContext) interface{}

	// Visit a parse tree produced by DorisParserParser#mysqlDataDesc.
	VisitMysqlDataDesc(ctx *MysqlDataDescContext) interface{}

	// Visit a parse tree produced by DorisParserParser#skipLines.
	VisitSkipLines(ctx *SkipLinesContext) interface{}

	// Visit a parse tree produced by DorisParserParser#outFileClause.
	VisitOutFileClause(ctx *OutFileClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#query.
	VisitQuery(ctx *QueryContext) interface{}

	// Visit a parse tree produced by DorisParserParser#queryTermDefault.
	VisitQueryTermDefault(ctx *QueryTermDefaultContext) interface{}

	// Visit a parse tree produced by DorisParserParser#setOperation.
	VisitSetOperation(ctx *SetOperationContext) interface{}

	// Visit a parse tree produced by DorisParserParser#setQuantifier.
	VisitSetQuantifier(ctx *SetQuantifierContext) interface{}

	// Visit a parse tree produced by DorisParserParser#queryPrimaryDefault.
	VisitQueryPrimaryDefault(ctx *QueryPrimaryDefaultContext) interface{}

	// Visit a parse tree produced by DorisParserParser#subquery.
	VisitSubquery(ctx *SubqueryContext) interface{}

	// Visit a parse tree produced by DorisParserParser#valuesTable.
	VisitValuesTable(ctx *ValuesTableContext) interface{}

	// Visit a parse tree produced by DorisParserParser#regularQuerySpecification.
	VisitRegularQuerySpecification(ctx *RegularQuerySpecificationContext) interface{}

	// Visit a parse tree produced by DorisParserParser#cte.
	VisitCte(ctx *CteContext) interface{}

	// Visit a parse tree produced by DorisParserParser#aliasQuery.
	VisitAliasQuery(ctx *AliasQueryContext) interface{}

	// Visit a parse tree produced by DorisParserParser#columnAliases.
	VisitColumnAliases(ctx *ColumnAliasesContext) interface{}

	// Visit a parse tree produced by DorisParserParser#selectClause.
	VisitSelectClause(ctx *SelectClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#selectColumnClause.
	VisitSelectColumnClause(ctx *SelectColumnClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#whereClause.
	VisitWhereClause(ctx *WhereClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#fromClause.
	VisitFromClause(ctx *FromClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#intoClause.
	VisitIntoClause(ctx *IntoClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#bulkCollectClause.
	VisitBulkCollectClause(ctx *BulkCollectClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#tableRow.
	VisitTableRow(ctx *TableRowContext) interface{}

	// Visit a parse tree produced by DorisParserParser#relations.
	VisitRelations(ctx *RelationsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#relation.
	VisitRelation(ctx *RelationContext) interface{}

	// Visit a parse tree produced by DorisParserParser#joinRelation.
	VisitJoinRelation(ctx *JoinRelationContext) interface{}

	// Visit a parse tree produced by DorisParserParser#bracketDistributeType.
	VisitBracketDistributeType(ctx *BracketDistributeTypeContext) interface{}

	// Visit a parse tree produced by DorisParserParser#commentDistributeType.
	VisitCommentDistributeType(ctx *CommentDistributeTypeContext) interface{}

	// Visit a parse tree produced by DorisParserParser#bracketRelationHint.
	VisitBracketRelationHint(ctx *BracketRelationHintContext) interface{}

	// Visit a parse tree produced by DorisParserParser#commentRelationHint.
	VisitCommentRelationHint(ctx *CommentRelationHintContext) interface{}

	// Visit a parse tree produced by DorisParserParser#aggClause.
	VisitAggClause(ctx *AggClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#groupingElement.
	VisitGroupingElement(ctx *GroupingElementContext) interface{}

	// Visit a parse tree produced by DorisParserParser#groupingSet.
	VisitGroupingSet(ctx *GroupingSetContext) interface{}

	// Visit a parse tree produced by DorisParserParser#havingClause.
	VisitHavingClause(ctx *HavingClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#qualifyClause.
	VisitQualifyClause(ctx *QualifyClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#selectHint.
	VisitSelectHint(ctx *SelectHintContext) interface{}

	// Visit a parse tree produced by DorisParserParser#hintStatement.
	VisitHintStatement(ctx *HintStatementContext) interface{}

	// Visit a parse tree produced by DorisParserParser#hintAssignment.
	VisitHintAssignment(ctx *HintAssignmentContext) interface{}

	// Visit a parse tree produced by DorisParserParser#updateAssignment.
	VisitUpdateAssignment(ctx *UpdateAssignmentContext) interface{}

	// Visit a parse tree produced by DorisParserParser#updateAssignmentSeq.
	VisitUpdateAssignmentSeq(ctx *UpdateAssignmentSeqContext) interface{}

	// Visit a parse tree produced by DorisParserParser#lateralView.
	VisitLateralView(ctx *LateralViewContext) interface{}

	// Visit a parse tree produced by DorisParserParser#queryOrganization.
	VisitQueryOrganization(ctx *QueryOrganizationContext) interface{}

	// Visit a parse tree produced by DorisParserParser#sortClause.
	VisitSortClause(ctx *SortClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#sortItem.
	VisitSortItem(ctx *SortItemContext) interface{}

	// Visit a parse tree produced by DorisParserParser#limitClause.
	VisitLimitClause(ctx *LimitClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#partitionClause.
	VisitPartitionClause(ctx *PartitionClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#joinType.
	VisitJoinType(ctx *JoinTypeContext) interface{}

	// Visit a parse tree produced by DorisParserParser#joinCriteria.
	VisitJoinCriteria(ctx *JoinCriteriaContext) interface{}

	// Visit a parse tree produced by DorisParserParser#identifierList.
	VisitIdentifierList(ctx *IdentifierListContext) interface{}

	// Visit a parse tree produced by DorisParserParser#identifierSeq.
	VisitIdentifierSeq(ctx *IdentifierSeqContext) interface{}

	// Visit a parse tree produced by DorisParserParser#optScanParams.
	VisitOptScanParams(ctx *OptScanParamsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#tableName.
	VisitTableName(ctx *TableNameContext) interface{}

	// Visit a parse tree produced by DorisParserParser#aliasedQuery.
	VisitAliasedQuery(ctx *AliasedQueryContext) interface{}

	// Visit a parse tree produced by DorisParserParser#tableValuedFunction.
	VisitTableValuedFunction(ctx *TableValuedFunctionContext) interface{}

	// Visit a parse tree produced by DorisParserParser#relationList.
	VisitRelationList(ctx *RelationListContext) interface{}

	// Visit a parse tree produced by DorisParserParser#materializedViewName.
	VisitMaterializedViewName(ctx *MaterializedViewNameContext) interface{}

	// Visit a parse tree produced by DorisParserParser#propertyClause.
	VisitPropertyClause(ctx *PropertyClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#propertyItemList.
	VisitPropertyItemList(ctx *PropertyItemListContext) interface{}

	// Visit a parse tree produced by DorisParserParser#propertyItem.
	VisitPropertyItem(ctx *PropertyItemContext) interface{}

	// Visit a parse tree produced by DorisParserParser#propertyKey.
	VisitPropertyKey(ctx *PropertyKeyContext) interface{}

	// Visit a parse tree produced by DorisParserParser#propertyValue.
	VisitPropertyValue(ctx *PropertyValueContext) interface{}

	// Visit a parse tree produced by DorisParserParser#tableAlias.
	VisitTableAlias(ctx *TableAliasContext) interface{}

	// Visit a parse tree produced by DorisParserParser#multipartIdentifier.
	VisitMultipartIdentifier(ctx *MultipartIdentifierContext) interface{}

	// Visit a parse tree produced by DorisParserParser#simpleColumnDefs.
	VisitSimpleColumnDefs(ctx *SimpleColumnDefsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#simpleColumnDef.
	VisitSimpleColumnDef(ctx *SimpleColumnDefContext) interface{}

	// Visit a parse tree produced by DorisParserParser#columnDefs.
	VisitColumnDefs(ctx *ColumnDefsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#columnDef.
	VisitColumnDef(ctx *ColumnDefContext) interface{}

	// Visit a parse tree produced by DorisParserParser#indexDefs.
	VisitIndexDefs(ctx *IndexDefsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#indexDef.
	VisitIndexDef(ctx *IndexDefContext) interface{}

	// Visit a parse tree produced by DorisParserParser#partitionsDef.
	VisitPartitionsDef(ctx *PartitionsDefContext) interface{}

	// Visit a parse tree produced by DorisParserParser#partitionDef.
	VisitPartitionDef(ctx *PartitionDefContext) interface{}

	// Visit a parse tree produced by DorisParserParser#lessThanPartitionDef.
	VisitLessThanPartitionDef(ctx *LessThanPartitionDefContext) interface{}

	// Visit a parse tree produced by DorisParserParser#fixedPartitionDef.
	VisitFixedPartitionDef(ctx *FixedPartitionDefContext) interface{}

	// Visit a parse tree produced by DorisParserParser#stepPartitionDef.
	VisitStepPartitionDef(ctx *StepPartitionDefContext) interface{}

	// Visit a parse tree produced by DorisParserParser#inPartitionDef.
	VisitInPartitionDef(ctx *InPartitionDefContext) interface{}

	// Visit a parse tree produced by DorisParserParser#partitionValueList.
	VisitPartitionValueList(ctx *PartitionValueListContext) interface{}

	// Visit a parse tree produced by DorisParserParser#partitionValueDef.
	VisitPartitionValueDef(ctx *PartitionValueDefContext) interface{}

	// Visit a parse tree produced by DorisParserParser#rollupDefs.
	VisitRollupDefs(ctx *RollupDefsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#rollupDef.
	VisitRollupDef(ctx *RollupDefContext) interface{}

	// Visit a parse tree produced by DorisParserParser#aggTypeDef.
	VisitAggTypeDef(ctx *AggTypeDefContext) interface{}

	// Visit a parse tree produced by DorisParserParser#tabletList.
	VisitTabletList(ctx *TabletListContext) interface{}

	// Visit a parse tree produced by DorisParserParser#inlineTable.
	VisitInlineTable(ctx *InlineTableContext) interface{}

	// Visit a parse tree produced by DorisParserParser#namedExpression.
	VisitNamedExpression(ctx *NamedExpressionContext) interface{}

	// Visit a parse tree produced by DorisParserParser#namedExpressionSeq.
	VisitNamedExpressionSeq(ctx *NamedExpressionSeqContext) interface{}

	// Visit a parse tree produced by DorisParserParser#expression.
	VisitExpression(ctx *ExpressionContext) interface{}

	// Visit a parse tree produced by DorisParserParser#lambdaExpression.
	VisitLambdaExpression(ctx *LambdaExpressionContext) interface{}

	// Visit a parse tree produced by DorisParserParser#exist.
	VisitExist(ctx *ExistContext) interface{}

	// Visit a parse tree produced by DorisParserParser#logicalNot.
	VisitLogicalNot(ctx *LogicalNotContext) interface{}

	// Visit a parse tree produced by DorisParserParser#predicated.
	VisitPredicated(ctx *PredicatedContext) interface{}

	// Visit a parse tree produced by DorisParserParser#isnull.
	VisitIsnull(ctx *IsnullContext) interface{}

	// Visit a parse tree produced by DorisParserParser#is_not_null_pred.
	VisitIs_not_null_pred(ctx *Is_not_null_predContext) interface{}

	// Visit a parse tree produced by DorisParserParser#logicalBinary.
	VisitLogicalBinary(ctx *LogicalBinaryContext) interface{}

	// Visit a parse tree produced by DorisParserParser#doublePipes.
	VisitDoublePipes(ctx *DoublePipesContext) interface{}

	// Visit a parse tree produced by DorisParserParser#rowConstructor.
	VisitRowConstructor(ctx *RowConstructorContext) interface{}

	// Visit a parse tree produced by DorisParserParser#rowConstructorItem.
	VisitRowConstructorItem(ctx *RowConstructorItemContext) interface{}

	// Visit a parse tree produced by DorisParserParser#predicate.
	VisitPredicate(ctx *PredicateContext) interface{}

	// Visit a parse tree produced by DorisParserParser#valueExpressionDefault.
	VisitValueExpressionDefault(ctx *ValueExpressionDefaultContext) interface{}

	// Visit a parse tree produced by DorisParserParser#comparison.
	VisitComparison(ctx *ComparisonContext) interface{}

	// Visit a parse tree produced by DorisParserParser#arithmeticBinary.
	VisitArithmeticBinary(ctx *ArithmeticBinaryContext) interface{}

	// Visit a parse tree produced by DorisParserParser#arithmeticUnary.
	VisitArithmeticUnary(ctx *ArithmeticUnaryContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dereference.
	VisitDereference(ctx *DereferenceContext) interface{}

	// Visit a parse tree produced by DorisParserParser#currentDate.
	VisitCurrentDate(ctx *CurrentDateContext) interface{}

	// Visit a parse tree produced by DorisParserParser#cast.
	VisitCast(ctx *CastContext) interface{}

	// Visit a parse tree produced by DorisParserParser#parenthesizedExpression.
	VisitParenthesizedExpression(ctx *ParenthesizedExpressionContext) interface{}

	// Visit a parse tree produced by DorisParserParser#userVariable.
	VisitUserVariable(ctx *UserVariableContext) interface{}

	// Visit a parse tree produced by DorisParserParser#elementAt.
	VisitElementAt(ctx *ElementAtContext) interface{}

	// Visit a parse tree produced by DorisParserParser#localTimestamp.
	VisitLocalTimestamp(ctx *LocalTimestampContext) interface{}

	// Visit a parse tree produced by DorisParserParser#charFunction.
	VisitCharFunction(ctx *CharFunctionContext) interface{}

	// Visit a parse tree produced by DorisParserParser#intervalLiteral.
	VisitIntervalLiteral(ctx *IntervalLiteralContext) interface{}

	// Visit a parse tree produced by DorisParserParser#simpleCase.
	VisitSimpleCase(ctx *SimpleCaseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#columnReference.
	VisitColumnReference(ctx *ColumnReferenceContext) interface{}

	// Visit a parse tree produced by DorisParserParser#star.
	VisitStar(ctx *StarContext) interface{}

	// Visit a parse tree produced by DorisParserParser#sessionUser.
	VisitSessionUser(ctx *SessionUserContext) interface{}

	// Visit a parse tree produced by DorisParserParser#convertType.
	VisitConvertType(ctx *ConvertTypeContext) interface{}

	// Visit a parse tree produced by DorisParserParser#convertCharSet.
	VisitConvertCharSet(ctx *ConvertCharSetContext) interface{}

	// Visit a parse tree produced by DorisParserParser#subqueryExpression.
	VisitSubqueryExpression(ctx *SubqueryExpressionContext) interface{}

	// Visit a parse tree produced by DorisParserParser#encryptKey.
	VisitEncryptKey(ctx *EncryptKeyContext) interface{}

	// Visit a parse tree produced by DorisParserParser#currentTime.
	VisitCurrentTime(ctx *CurrentTimeContext) interface{}

	// Visit a parse tree produced by DorisParserParser#localTime.
	VisitLocalTime(ctx *LocalTimeContext) interface{}

	// Visit a parse tree produced by DorisParserParser#systemVariable.
	VisitSystemVariable(ctx *SystemVariableContext) interface{}

	// Visit a parse tree produced by DorisParserParser#collate.
	VisitCollate(ctx *CollateContext) interface{}

	// Visit a parse tree produced by DorisParserParser#currentUser.
	VisitCurrentUser(ctx *CurrentUserContext) interface{}

	// Visit a parse tree produced by DorisParserParser#constantDefault.
	VisitConstantDefault(ctx *ConstantDefaultContext) interface{}

	// Visit a parse tree produced by DorisParserParser#extract.
	VisitExtract(ctx *ExtractContext) interface{}

	// Visit a parse tree produced by DorisParserParser#currentTimestamp.
	VisitCurrentTimestamp(ctx *CurrentTimestampContext) interface{}

	// Visit a parse tree produced by DorisParserParser#functionCall.
	VisitFunctionCall(ctx *FunctionCallContext) interface{}

	// Visit a parse tree produced by DorisParserParser#arraySlice.
	VisitArraySlice(ctx *ArraySliceContext) interface{}

	// Visit a parse tree produced by DorisParserParser#searchedCase.
	VisitSearchedCase(ctx *SearchedCaseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#except.
	VisitExcept(ctx *ExceptContext) interface{}

	// Visit a parse tree produced by DorisParserParser#replace.
	VisitReplace(ctx *ReplaceContext) interface{}

	// Visit a parse tree produced by DorisParserParser#castDataType.
	VisitCastDataType(ctx *CastDataTypeContext) interface{}

	// Visit a parse tree produced by DorisParserParser#functionCallExpression.
	VisitFunctionCallExpression(ctx *FunctionCallExpressionContext) interface{}

	// Visit a parse tree produced by DorisParserParser#functionIdentifier.
	VisitFunctionIdentifier(ctx *FunctionIdentifierContext) interface{}

	// Visit a parse tree produced by DorisParserParser#functionNameIdentifier.
	VisitFunctionNameIdentifier(ctx *FunctionNameIdentifierContext) interface{}

	// Visit a parse tree produced by DorisParserParser#windowSpec.
	VisitWindowSpec(ctx *WindowSpecContext) interface{}

	// Visit a parse tree produced by DorisParserParser#windowFrame.
	VisitWindowFrame(ctx *WindowFrameContext) interface{}

	// Visit a parse tree produced by DorisParserParser#frameUnits.
	VisitFrameUnits(ctx *FrameUnitsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#frameBoundary.
	VisitFrameBoundary(ctx *FrameBoundaryContext) interface{}

	// Visit a parse tree produced by DorisParserParser#qualifiedName.
	VisitQualifiedName(ctx *QualifiedNameContext) interface{}

	// Visit a parse tree produced by DorisParserParser#specifiedPartition.
	VisitSpecifiedPartition(ctx *SpecifiedPartitionContext) interface{}

	// Visit a parse tree produced by DorisParserParser#nullLiteral.
	VisitNullLiteral(ctx *NullLiteralContext) interface{}

	// Visit a parse tree produced by DorisParserParser#typeConstructor.
	VisitTypeConstructor(ctx *TypeConstructorContext) interface{}

	// Visit a parse tree produced by DorisParserParser#numericLiteral.
	VisitNumericLiteral(ctx *NumericLiteralContext) interface{}

	// Visit a parse tree produced by DorisParserParser#booleanLiteral.
	VisitBooleanLiteral(ctx *BooleanLiteralContext) interface{}

	// Visit a parse tree produced by DorisParserParser#stringLiteral.
	VisitStringLiteral(ctx *StringLiteralContext) interface{}

	// Visit a parse tree produced by DorisParserParser#arrayLiteral.
	VisitArrayLiteral(ctx *ArrayLiteralContext) interface{}

	// Visit a parse tree produced by DorisParserParser#mapLiteral.
	VisitMapLiteral(ctx *MapLiteralContext) interface{}

	// Visit a parse tree produced by DorisParserParser#structLiteral.
	VisitStructLiteral(ctx *StructLiteralContext) interface{}

	// Visit a parse tree produced by DorisParserParser#placeholder.
	VisitPlaceholder(ctx *PlaceholderContext) interface{}

	// Visit a parse tree produced by DorisParserParser#comparisonOperator.
	VisitComparisonOperator(ctx *ComparisonOperatorContext) interface{}

	// Visit a parse tree produced by DorisParserParser#booleanValue.
	VisitBooleanValue(ctx *BooleanValueContext) interface{}

	// Visit a parse tree produced by DorisParserParser#whenClause.
	VisitWhenClause(ctx *WhenClauseContext) interface{}

	// Visit a parse tree produced by DorisParserParser#interval.
	VisitInterval(ctx *IntervalContext) interface{}

	// Visit a parse tree produced by DorisParserParser#unitIdentifier.
	VisitUnitIdentifier(ctx *UnitIdentifierContext) interface{}

	// Visit a parse tree produced by DorisParserParser#dataTypeWithNullable.
	VisitDataTypeWithNullable(ctx *DataTypeWithNullableContext) interface{}

	// Visit a parse tree produced by DorisParserParser#complexDataType.
	VisitComplexDataType(ctx *ComplexDataTypeContext) interface{}

	// Visit a parse tree produced by DorisParserParser#aggStateDataType.
	VisitAggStateDataType(ctx *AggStateDataTypeContext) interface{}

	// Visit a parse tree produced by DorisParserParser#primitiveDataType.
	VisitPrimitiveDataType(ctx *PrimitiveDataTypeContext) interface{}

	// Visit a parse tree produced by DorisParserParser#primitiveColType.
	VisitPrimitiveColType(ctx *PrimitiveColTypeContext) interface{}

	// Visit a parse tree produced by DorisParserParser#complexColTypeList.
	VisitComplexColTypeList(ctx *ComplexColTypeListContext) interface{}

	// Visit a parse tree produced by DorisParserParser#complexColType.
	VisitComplexColType(ctx *ComplexColTypeContext) interface{}

	// Visit a parse tree produced by DorisParserParser#commentSpec.
	VisitCommentSpec(ctx *CommentSpecContext) interface{}

	// Visit a parse tree produced by DorisParserParser#sample.
	VisitSample(ctx *SampleContext) interface{}

	// Visit a parse tree produced by DorisParserParser#sampleByPercentile.
	VisitSampleByPercentile(ctx *SampleByPercentileContext) interface{}

	// Visit a parse tree produced by DorisParserParser#sampleByRows.
	VisitSampleByRows(ctx *SampleByRowsContext) interface{}

	// Visit a parse tree produced by DorisParserParser#tableSnapshot.
	VisitTableSnapshot(ctx *TableSnapshotContext) interface{}

	// Visit a parse tree produced by DorisParserParser#errorCapturingIdentifier.
	VisitErrorCapturingIdentifier(ctx *ErrorCapturingIdentifierContext) interface{}

	// Visit a parse tree produced by DorisParserParser#errorIdent.
	VisitErrorIdent(ctx *ErrorIdentContext) interface{}

	// Visit a parse tree produced by DorisParserParser#realIdent.
	VisitRealIdent(ctx *RealIdentContext) interface{}

	// Visit a parse tree produced by DorisParserParser#identifier.
	VisitIdentifier(ctx *IdentifierContext) interface{}

	// Visit a parse tree produced by DorisParserParser#unquotedIdentifier.
	VisitUnquotedIdentifier(ctx *UnquotedIdentifierContext) interface{}

	// Visit a parse tree produced by DorisParserParser#quotedIdentifierAlternative.
	VisitQuotedIdentifierAlternative(ctx *QuotedIdentifierAlternativeContext) interface{}

	// Visit a parse tree produced by DorisParserParser#quotedIdentifier.
	VisitQuotedIdentifier(ctx *QuotedIdentifierContext) interface{}

	// Visit a parse tree produced by DorisParserParser#integerLiteral.
	VisitIntegerLiteral(ctx *IntegerLiteralContext) interface{}

	// Visit a parse tree produced by DorisParserParser#decimalLiteral.
	VisitDecimalLiteral(ctx *DecimalLiteralContext) interface{}

	// Visit a parse tree produced by DorisParserParser#nonReserved.
	VisitNonReserved(ctx *NonReservedContext) interface{}
}
