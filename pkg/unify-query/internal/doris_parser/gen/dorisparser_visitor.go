// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Code generated from DorisParser.g4 by ANTLR 4.13.2. DO NOT EDIT.

package gen // DorisParser
import "github.com/antlr4-go/antlr/v4"

// A complete Visitor for a parse tree produced by DorisParserParser.
type DorisParserVisitor interface {
	antlr.ParseTreeVisitor

	// Visit a parse tree produced by DorisParserParser#multiStatements.
	VisitMultiStatements(ctx *MultiStatementsContext) any

	// Visit a parse tree produced by DorisParserParser#singleStatement.
	VisitSingleStatement(ctx *SingleStatementContext) any

	// Visit a parse tree produced by DorisParserParser#statementBaseAlias.
	VisitStatementBaseAlias(ctx *StatementBaseAliasContext) any

	// Visit a parse tree produced by DorisParserParser#callProcedure.
	VisitCallProcedure(ctx *CallProcedureContext) any

	// Visit a parse tree produced by DorisParserParser#createProcedure.
	VisitCreateProcedure(ctx *CreateProcedureContext) any

	// Visit a parse tree produced by DorisParserParser#dropProcedure.
	VisitDropProcedure(ctx *DropProcedureContext) any

	// Visit a parse tree produced by DorisParserParser#showProcedureStatus.
	VisitShowProcedureStatus(ctx *ShowProcedureStatusContext) any

	// Visit a parse tree produced by DorisParserParser#showCreateProcedure.
	VisitShowCreateProcedure(ctx *ShowCreateProcedureContext) any

	// Visit a parse tree produced by DorisParserParser#showConfig.
	VisitShowConfig(ctx *ShowConfigContext) any

	// Visit a parse tree produced by DorisParserParser#statementDefault.
	VisitStatementDefault(ctx *StatementDefaultContext) any

	// Visit a parse tree produced by DorisParserParser#supportedDmlStatementAlias.
	VisitSupportedDmlStatementAlias(ctx *SupportedDmlStatementAliasContext) any

	// Visit a parse tree produced by DorisParserParser#supportedCreateStatementAlias.
	VisitSupportedCreateStatementAlias(ctx *SupportedCreateStatementAliasContext) any

	// Visit a parse tree produced by DorisParserParser#supportedAlterStatementAlias.
	VisitSupportedAlterStatementAlias(ctx *SupportedAlterStatementAliasContext) any

	// Visit a parse tree produced by DorisParserParser#materializedViewStatementAlias.
	VisitMaterializedViewStatementAlias(ctx *MaterializedViewStatementAliasContext) any

	// Visit a parse tree produced by DorisParserParser#supportedJobStatementAlias.
	VisitSupportedJobStatementAlias(ctx *SupportedJobStatementAliasContext) any

	// Visit a parse tree produced by DorisParserParser#constraintStatementAlias.
	VisitConstraintStatementAlias(ctx *ConstraintStatementAliasContext) any

	// Visit a parse tree produced by DorisParserParser#supportedCleanStatementAlias.
	VisitSupportedCleanStatementAlias(ctx *SupportedCleanStatementAliasContext) any

	// Visit a parse tree produced by DorisParserParser#supportedDescribeStatementAlias.
	VisitSupportedDescribeStatementAlias(ctx *SupportedDescribeStatementAliasContext) any

	// Visit a parse tree produced by DorisParserParser#supportedDropStatementAlias.
	VisitSupportedDropStatementAlias(ctx *SupportedDropStatementAliasContext) any

	// Visit a parse tree produced by DorisParserParser#supportedSetStatementAlias.
	VisitSupportedSetStatementAlias(ctx *SupportedSetStatementAliasContext) any

	// Visit a parse tree produced by DorisParserParser#supportedUnsetStatementAlias.
	VisitSupportedUnsetStatementAlias(ctx *SupportedUnsetStatementAliasContext) any

	// Visit a parse tree produced by DorisParserParser#supportedRefreshStatementAlias.
	VisitSupportedRefreshStatementAlias(ctx *SupportedRefreshStatementAliasContext) any

	// Visit a parse tree produced by DorisParserParser#supportedShowStatementAlias.
	VisitSupportedShowStatementAlias(ctx *SupportedShowStatementAliasContext) any

	// Visit a parse tree produced by DorisParserParser#supportedLoadStatementAlias.
	VisitSupportedLoadStatementAlias(ctx *SupportedLoadStatementAliasContext) any

	// Visit a parse tree produced by DorisParserParser#supportedCancelStatementAlias.
	VisitSupportedCancelStatementAlias(ctx *SupportedCancelStatementAliasContext) any

	// Visit a parse tree produced by DorisParserParser#supportedRecoverStatementAlias.
	VisitSupportedRecoverStatementAlias(ctx *SupportedRecoverStatementAliasContext) any

	// Visit a parse tree produced by DorisParserParser#supportedAdminStatementAlias.
	VisitSupportedAdminStatementAlias(ctx *SupportedAdminStatementAliasContext) any

	// Visit a parse tree produced by DorisParserParser#supportedUseStatementAlias.
	VisitSupportedUseStatementAlias(ctx *SupportedUseStatementAliasContext) any

	// Visit a parse tree produced by DorisParserParser#supportedOtherStatementAlias.
	VisitSupportedOtherStatementAlias(ctx *SupportedOtherStatementAliasContext) any

	// Visit a parse tree produced by DorisParserParser#supportedKillStatementAlias.
	VisitSupportedKillStatementAlias(ctx *SupportedKillStatementAliasContext) any

	// Visit a parse tree produced by DorisParserParser#supportedStatsStatementAlias.
	VisitSupportedStatsStatementAlias(ctx *SupportedStatsStatementAliasContext) any

	// Visit a parse tree produced by DorisParserParser#supportedTransactionStatementAlias.
	VisitSupportedTransactionStatementAlias(ctx *SupportedTransactionStatementAliasContext) any

	// Visit a parse tree produced by DorisParserParser#supportedGrantRevokeStatementAlias.
	VisitSupportedGrantRevokeStatementAlias(ctx *SupportedGrantRevokeStatementAliasContext) any

	// Visit a parse tree produced by DorisParserParser#unsupported.
	VisitUnsupported(ctx *UnsupportedContext) any

	// Visit a parse tree produced by DorisParserParser#unsupportedStatement.
	VisitUnsupportedStatement(ctx *UnsupportedStatementContext) any

	// Visit a parse tree produced by DorisParserParser#createMTMV.
	VisitCreateMTMV(ctx *CreateMTMVContext) any

	// Visit a parse tree produced by DorisParserParser#refreshMTMV.
	VisitRefreshMTMV(ctx *RefreshMTMVContext) any

	// Visit a parse tree produced by DorisParserParser#alterMTMV.
	VisitAlterMTMV(ctx *AlterMTMVContext) any

	// Visit a parse tree produced by DorisParserParser#dropMTMV.
	VisitDropMTMV(ctx *DropMTMVContext) any

	// Visit a parse tree produced by DorisParserParser#pauseMTMV.
	VisitPauseMTMV(ctx *PauseMTMVContext) any

	// Visit a parse tree produced by DorisParserParser#resumeMTMV.
	VisitResumeMTMV(ctx *ResumeMTMVContext) any

	// Visit a parse tree produced by DorisParserParser#cancelMTMVTask.
	VisitCancelMTMVTask(ctx *CancelMTMVTaskContext) any

	// Visit a parse tree produced by DorisParserParser#showCreateMTMV.
	VisitShowCreateMTMV(ctx *ShowCreateMTMVContext) any

	// Visit a parse tree produced by DorisParserParser#createScheduledJob.
	VisitCreateScheduledJob(ctx *CreateScheduledJobContext) any

	// Visit a parse tree produced by DorisParserParser#pauseJob.
	VisitPauseJob(ctx *PauseJobContext) any

	// Visit a parse tree produced by DorisParserParser#dropJob.
	VisitDropJob(ctx *DropJobContext) any

	// Visit a parse tree produced by DorisParserParser#resumeJob.
	VisitResumeJob(ctx *ResumeJobContext) any

	// Visit a parse tree produced by DorisParserParser#cancelJobTask.
	VisitCancelJobTask(ctx *CancelJobTaskContext) any

	// Visit a parse tree produced by DorisParserParser#addConstraint.
	VisitAddConstraint(ctx *AddConstraintContext) any

	// Visit a parse tree produced by DorisParserParser#dropConstraint.
	VisitDropConstraint(ctx *DropConstraintContext) any

	// Visit a parse tree produced by DorisParserParser#showConstraint.
	VisitShowConstraint(ctx *ShowConstraintContext) any

	// Visit a parse tree produced by DorisParserParser#insertTable.
	VisitInsertTable(ctx *InsertTableContext) any

	// Visit a parse tree produced by DorisParserParser#update.
	VisitUpdate(ctx *UpdateContext) any

	// Visit a parse tree produced by DorisParserParser#delete.
	VisitDelete(ctx *DeleteContext) any

	// Visit a parse tree produced by DorisParserParser#load.
	VisitLoad(ctx *LoadContext) any

	// Visit a parse tree produced by DorisParserParser#export.
	VisitExport(ctx *ExportContext) any

	// Visit a parse tree produced by DorisParserParser#replay.
	VisitReplay(ctx *ReplayContext) any

	// Visit a parse tree produced by DorisParserParser#copyInto.
	VisitCopyInto(ctx *CopyIntoContext) any

	// Visit a parse tree produced by DorisParserParser#truncateTable.
	VisitTruncateTable(ctx *TruncateTableContext) any

	// Visit a parse tree produced by DorisParserParser#createTable.
	VisitCreateTable(ctx *CreateTableContext) any

	// Visit a parse tree produced by DorisParserParser#createView.
	VisitCreateView(ctx *CreateViewContext) any

	// Visit a parse tree produced by DorisParserParser#createFile.
	VisitCreateFile(ctx *CreateFileContext) any

	// Visit a parse tree produced by DorisParserParser#createTableLike.
	VisitCreateTableLike(ctx *CreateTableLikeContext) any

	// Visit a parse tree produced by DorisParserParser#createRole.
	VisitCreateRole(ctx *CreateRoleContext) any

	// Visit a parse tree produced by DorisParserParser#createWorkloadGroup.
	VisitCreateWorkloadGroup(ctx *CreateWorkloadGroupContext) any

	// Visit a parse tree produced by DorisParserParser#createCatalog.
	VisitCreateCatalog(ctx *CreateCatalogContext) any

	// Visit a parse tree produced by DorisParserParser#createRowPolicy.
	VisitCreateRowPolicy(ctx *CreateRowPolicyContext) any

	// Visit a parse tree produced by DorisParserParser#createStoragePolicy.
	VisitCreateStoragePolicy(ctx *CreateStoragePolicyContext) any

	// Visit a parse tree produced by DorisParserParser#buildIndex.
	VisitBuildIndex(ctx *BuildIndexContext) any

	// Visit a parse tree produced by DorisParserParser#createIndex.
	VisitCreateIndex(ctx *CreateIndexContext) any

	// Visit a parse tree produced by DorisParserParser#createWorkloadPolicy.
	VisitCreateWorkloadPolicy(ctx *CreateWorkloadPolicyContext) any

	// Visit a parse tree produced by DorisParserParser#createSqlBlockRule.
	VisitCreateSqlBlockRule(ctx *CreateSqlBlockRuleContext) any

	// Visit a parse tree produced by DorisParserParser#createEncryptkey.
	VisitCreateEncryptkey(ctx *CreateEncryptkeyContext) any

	// Visit a parse tree produced by DorisParserParser#createUserDefineFunction.
	VisitCreateUserDefineFunction(ctx *CreateUserDefineFunctionContext) any

	// Visit a parse tree produced by DorisParserParser#createAliasFunction.
	VisitCreateAliasFunction(ctx *CreateAliasFunctionContext) any

	// Visit a parse tree produced by DorisParserParser#createUser.
	VisitCreateUser(ctx *CreateUserContext) any

	// Visit a parse tree produced by DorisParserParser#createDatabase.
	VisitCreateDatabase(ctx *CreateDatabaseContext) any

	// Visit a parse tree produced by DorisParserParser#createRepository.
	VisitCreateRepository(ctx *CreateRepositoryContext) any

	// Visit a parse tree produced by DorisParserParser#createResource.
	VisitCreateResource(ctx *CreateResourceContext) any

	// Visit a parse tree produced by DorisParserParser#createDictionary.
	VisitCreateDictionary(ctx *CreateDictionaryContext) any

	// Visit a parse tree produced by DorisParserParser#createStage.
	VisitCreateStage(ctx *CreateStageContext) any

	// Visit a parse tree produced by DorisParserParser#createStorageVault.
	VisitCreateStorageVault(ctx *CreateStorageVaultContext) any

	// Visit a parse tree produced by DorisParserParser#createIndexAnalyzer.
	VisitCreateIndexAnalyzer(ctx *CreateIndexAnalyzerContext) any

	// Visit a parse tree produced by DorisParserParser#createIndexTokenizer.
	VisitCreateIndexTokenizer(ctx *CreateIndexTokenizerContext) any

	// Visit a parse tree produced by DorisParserParser#createIndexTokenFilter.
	VisitCreateIndexTokenFilter(ctx *CreateIndexTokenFilterContext) any

	// Visit a parse tree produced by DorisParserParser#dictionaryColumnDefs.
	VisitDictionaryColumnDefs(ctx *DictionaryColumnDefsContext) any

	// Visit a parse tree produced by DorisParserParser#dictionaryColumnDef.
	VisitDictionaryColumnDef(ctx *DictionaryColumnDefContext) any

	// Visit a parse tree produced by DorisParserParser#alterSystem.
	VisitAlterSystem(ctx *AlterSystemContext) any

	// Visit a parse tree produced by DorisParserParser#alterView.
	VisitAlterView(ctx *AlterViewContext) any

	// Visit a parse tree produced by DorisParserParser#alterCatalogRename.
	VisitAlterCatalogRename(ctx *AlterCatalogRenameContext) any

	// Visit a parse tree produced by DorisParserParser#alterRole.
	VisitAlterRole(ctx *AlterRoleContext) any

	// Visit a parse tree produced by DorisParserParser#alterStorageVault.
	VisitAlterStorageVault(ctx *AlterStorageVaultContext) any

	// Visit a parse tree produced by DorisParserParser#alterWorkloadGroup.
	VisitAlterWorkloadGroup(ctx *AlterWorkloadGroupContext) any

	// Visit a parse tree produced by DorisParserParser#alterCatalogProperties.
	VisitAlterCatalogProperties(ctx *AlterCatalogPropertiesContext) any

	// Visit a parse tree produced by DorisParserParser#alterWorkloadPolicy.
	VisitAlterWorkloadPolicy(ctx *AlterWorkloadPolicyContext) any

	// Visit a parse tree produced by DorisParserParser#alterSqlBlockRule.
	VisitAlterSqlBlockRule(ctx *AlterSqlBlockRuleContext) any

	// Visit a parse tree produced by DorisParserParser#alterCatalogComment.
	VisitAlterCatalogComment(ctx *AlterCatalogCommentContext) any

	// Visit a parse tree produced by DorisParserParser#alterDatabaseRename.
	VisitAlterDatabaseRename(ctx *AlterDatabaseRenameContext) any

	// Visit a parse tree produced by DorisParserParser#alterStoragePolicy.
	VisitAlterStoragePolicy(ctx *AlterStoragePolicyContext) any

	// Visit a parse tree produced by DorisParserParser#alterTable.
	VisitAlterTable(ctx *AlterTableContext) any

	// Visit a parse tree produced by DorisParserParser#alterTableAddRollup.
	VisitAlterTableAddRollup(ctx *AlterTableAddRollupContext) any

	// Visit a parse tree produced by DorisParserParser#alterTableDropRollup.
	VisitAlterTableDropRollup(ctx *AlterTableDropRollupContext) any

	// Visit a parse tree produced by DorisParserParser#alterTableProperties.
	VisitAlterTableProperties(ctx *AlterTablePropertiesContext) any

	// Visit a parse tree produced by DorisParserParser#alterDatabaseSetQuota.
	VisitAlterDatabaseSetQuota(ctx *AlterDatabaseSetQuotaContext) any

	// Visit a parse tree produced by DorisParserParser#alterDatabaseProperties.
	VisitAlterDatabaseProperties(ctx *AlterDatabasePropertiesContext) any

	// Visit a parse tree produced by DorisParserParser#alterSystemRenameComputeGroup.
	VisitAlterSystemRenameComputeGroup(ctx *AlterSystemRenameComputeGroupContext) any

	// Visit a parse tree produced by DorisParserParser#alterResource.
	VisitAlterResource(ctx *AlterResourceContext) any

	// Visit a parse tree produced by DorisParserParser#alterRepository.
	VisitAlterRepository(ctx *AlterRepositoryContext) any

	// Visit a parse tree produced by DorisParserParser#alterRoutineLoad.
	VisitAlterRoutineLoad(ctx *AlterRoutineLoadContext) any

	// Visit a parse tree produced by DorisParserParser#alterColocateGroup.
	VisitAlterColocateGroup(ctx *AlterColocateGroupContext) any

	// Visit a parse tree produced by DorisParserParser#alterUser.
	VisitAlterUser(ctx *AlterUserContext) any

	// Visit a parse tree produced by DorisParserParser#dropCatalogRecycleBin.
	VisitDropCatalogRecycleBin(ctx *DropCatalogRecycleBinContext) any

	// Visit a parse tree produced by DorisParserParser#dropEncryptkey.
	VisitDropEncryptkey(ctx *DropEncryptkeyContext) any

	// Visit a parse tree produced by DorisParserParser#dropRole.
	VisitDropRole(ctx *DropRoleContext) any

	// Visit a parse tree produced by DorisParserParser#dropSqlBlockRule.
	VisitDropSqlBlockRule(ctx *DropSqlBlockRuleContext) any

	// Visit a parse tree produced by DorisParserParser#dropUser.
	VisitDropUser(ctx *DropUserContext) any

	// Visit a parse tree produced by DorisParserParser#dropStoragePolicy.
	VisitDropStoragePolicy(ctx *DropStoragePolicyContext) any

	// Visit a parse tree produced by DorisParserParser#dropWorkloadGroup.
	VisitDropWorkloadGroup(ctx *DropWorkloadGroupContext) any

	// Visit a parse tree produced by DorisParserParser#dropCatalog.
	VisitDropCatalog(ctx *DropCatalogContext) any

	// Visit a parse tree produced by DorisParserParser#dropFile.
	VisitDropFile(ctx *DropFileContext) any

	// Visit a parse tree produced by DorisParserParser#dropWorkloadPolicy.
	VisitDropWorkloadPolicy(ctx *DropWorkloadPolicyContext) any

	// Visit a parse tree produced by DorisParserParser#dropRepository.
	VisitDropRepository(ctx *DropRepositoryContext) any

	// Visit a parse tree produced by DorisParserParser#dropTable.
	VisitDropTable(ctx *DropTableContext) any

	// Visit a parse tree produced by DorisParserParser#dropDatabase.
	VisitDropDatabase(ctx *DropDatabaseContext) any

	// Visit a parse tree produced by DorisParserParser#dropFunction.
	VisitDropFunction(ctx *DropFunctionContext) any

	// Visit a parse tree produced by DorisParserParser#dropIndex.
	VisitDropIndex(ctx *DropIndexContext) any

	// Visit a parse tree produced by DorisParserParser#dropResource.
	VisitDropResource(ctx *DropResourceContext) any

	// Visit a parse tree produced by DorisParserParser#dropRowPolicy.
	VisitDropRowPolicy(ctx *DropRowPolicyContext) any

	// Visit a parse tree produced by DorisParserParser#dropDictionary.
	VisitDropDictionary(ctx *DropDictionaryContext) any

	// Visit a parse tree produced by DorisParserParser#dropStage.
	VisitDropStage(ctx *DropStageContext) any

	// Visit a parse tree produced by DorisParserParser#dropView.
	VisitDropView(ctx *DropViewContext) any

	// Visit a parse tree produced by DorisParserParser#dropIndexAnalyzer.
	VisitDropIndexAnalyzer(ctx *DropIndexAnalyzerContext) any

	// Visit a parse tree produced by DorisParserParser#dropIndexTokenizer.
	VisitDropIndexTokenizer(ctx *DropIndexTokenizerContext) any

	// Visit a parse tree produced by DorisParserParser#dropIndexTokenFilter.
	VisitDropIndexTokenFilter(ctx *DropIndexTokenFilterContext) any

	// Visit a parse tree produced by DorisParserParser#showVariables.
	VisitShowVariables(ctx *ShowVariablesContext) any

	// Visit a parse tree produced by DorisParserParser#showAuthors.
	VisitShowAuthors(ctx *ShowAuthorsContext) any

	// Visit a parse tree produced by DorisParserParser#showAlterTable.
	VisitShowAlterTable(ctx *ShowAlterTableContext) any

	// Visit a parse tree produced by DorisParserParser#showCreateDatabase.
	VisitShowCreateDatabase(ctx *ShowCreateDatabaseContext) any

	// Visit a parse tree produced by DorisParserParser#showBackup.
	VisitShowBackup(ctx *ShowBackupContext) any

	// Visit a parse tree produced by DorisParserParser#showBroker.
	VisitShowBroker(ctx *ShowBrokerContext) any

	// Visit a parse tree produced by DorisParserParser#showBuildIndex.
	VisitShowBuildIndex(ctx *ShowBuildIndexContext) any

	// Visit a parse tree produced by DorisParserParser#showDynamicPartition.
	VisitShowDynamicPartition(ctx *ShowDynamicPartitionContext) any

	// Visit a parse tree produced by DorisParserParser#showEvents.
	VisitShowEvents(ctx *ShowEventsContext) any

	// Visit a parse tree produced by DorisParserParser#showExport.
	VisitShowExport(ctx *ShowExportContext) any

	// Visit a parse tree produced by DorisParserParser#showLastInsert.
	VisitShowLastInsert(ctx *ShowLastInsertContext) any

	// Visit a parse tree produced by DorisParserParser#showCharset.
	VisitShowCharset(ctx *ShowCharsetContext) any

	// Visit a parse tree produced by DorisParserParser#showDelete.
	VisitShowDelete(ctx *ShowDeleteContext) any

	// Visit a parse tree produced by DorisParserParser#showCreateFunction.
	VisitShowCreateFunction(ctx *ShowCreateFunctionContext) any

	// Visit a parse tree produced by DorisParserParser#showFunctions.
	VisitShowFunctions(ctx *ShowFunctionsContext) any

	// Visit a parse tree produced by DorisParserParser#showGlobalFunctions.
	VisitShowGlobalFunctions(ctx *ShowGlobalFunctionsContext) any

	// Visit a parse tree produced by DorisParserParser#showGrants.
	VisitShowGrants(ctx *ShowGrantsContext) any

	// Visit a parse tree produced by DorisParserParser#showGrantsForUser.
	VisitShowGrantsForUser(ctx *ShowGrantsForUserContext) any

	// Visit a parse tree produced by DorisParserParser#showCreateUser.
	VisitShowCreateUser(ctx *ShowCreateUserContext) any

	// Visit a parse tree produced by DorisParserParser#showSnapshot.
	VisitShowSnapshot(ctx *ShowSnapshotContext) any

	// Visit a parse tree produced by DorisParserParser#showLoadProfile.
	VisitShowLoadProfile(ctx *ShowLoadProfileContext) any

	// Visit a parse tree produced by DorisParserParser#showCreateRepository.
	VisitShowCreateRepository(ctx *ShowCreateRepositoryContext) any

	// Visit a parse tree produced by DorisParserParser#showView.
	VisitShowView(ctx *ShowViewContext) any

	// Visit a parse tree produced by DorisParserParser#showPlugins.
	VisitShowPlugins(ctx *ShowPluginsContext) any

	// Visit a parse tree produced by DorisParserParser#showStorageVault.
	VisitShowStorageVault(ctx *ShowStorageVaultContext) any

	// Visit a parse tree produced by DorisParserParser#showRepositories.
	VisitShowRepositories(ctx *ShowRepositoriesContext) any

	// Visit a parse tree produced by DorisParserParser#showEncryptKeys.
	VisitShowEncryptKeys(ctx *ShowEncryptKeysContext) any

	// Visit a parse tree produced by DorisParserParser#showCreateTable.
	VisitShowCreateTable(ctx *ShowCreateTableContext) any

	// Visit a parse tree produced by DorisParserParser#showProcessList.
	VisitShowProcessList(ctx *ShowProcessListContext) any

	// Visit a parse tree produced by DorisParserParser#showPartitions.
	VisitShowPartitions(ctx *ShowPartitionsContext) any

	// Visit a parse tree produced by DorisParserParser#showRestore.
	VisitShowRestore(ctx *ShowRestoreContext) any

	// Visit a parse tree produced by DorisParserParser#showRoles.
	VisitShowRoles(ctx *ShowRolesContext) any

	// Visit a parse tree produced by DorisParserParser#showPartitionId.
	VisitShowPartitionId(ctx *ShowPartitionIdContext) any

	// Visit a parse tree produced by DorisParserParser#showPrivileges.
	VisitShowPrivileges(ctx *ShowPrivilegesContext) any

	// Visit a parse tree produced by DorisParserParser#showProc.
	VisitShowProc(ctx *ShowProcContext) any

	// Visit a parse tree produced by DorisParserParser#showSmallFiles.
	VisitShowSmallFiles(ctx *ShowSmallFilesContext) any

	// Visit a parse tree produced by DorisParserParser#showStorageEngines.
	VisitShowStorageEngines(ctx *ShowStorageEnginesContext) any

	// Visit a parse tree produced by DorisParserParser#showCreateCatalog.
	VisitShowCreateCatalog(ctx *ShowCreateCatalogContext) any

	// Visit a parse tree produced by DorisParserParser#showCatalog.
	VisitShowCatalog(ctx *ShowCatalogContext) any

	// Visit a parse tree produced by DorisParserParser#showCatalogs.
	VisitShowCatalogs(ctx *ShowCatalogsContext) any

	// Visit a parse tree produced by DorisParserParser#showUserProperties.
	VisitShowUserProperties(ctx *ShowUserPropertiesContext) any

	// Visit a parse tree produced by DorisParserParser#showAllProperties.
	VisitShowAllProperties(ctx *ShowAllPropertiesContext) any

	// Visit a parse tree produced by DorisParserParser#showCollation.
	VisitShowCollation(ctx *ShowCollationContext) any

	// Visit a parse tree produced by DorisParserParser#showRowPolicy.
	VisitShowRowPolicy(ctx *ShowRowPolicyContext) any

	// Visit a parse tree produced by DorisParserParser#showStoragePolicy.
	VisitShowStoragePolicy(ctx *ShowStoragePolicyContext) any

	// Visit a parse tree produced by DorisParserParser#showSqlBlockRule.
	VisitShowSqlBlockRule(ctx *ShowSqlBlockRuleContext) any

	// Visit a parse tree produced by DorisParserParser#showCreateView.
	VisitShowCreateView(ctx *ShowCreateViewContext) any

	// Visit a parse tree produced by DorisParserParser#showDataTypes.
	VisitShowDataTypes(ctx *ShowDataTypesContext) any

	// Visit a parse tree produced by DorisParserParser#showData.
	VisitShowData(ctx *ShowDataContext) any

	// Visit a parse tree produced by DorisParserParser#showCreateMaterializedView.
	VisitShowCreateMaterializedView(ctx *ShowCreateMaterializedViewContext) any

	// Visit a parse tree produced by DorisParserParser#showWarningErrors.
	VisitShowWarningErrors(ctx *ShowWarningErrorsContext) any

	// Visit a parse tree produced by DorisParserParser#showWarningErrorCount.
	VisitShowWarningErrorCount(ctx *ShowWarningErrorCountContext) any

	// Visit a parse tree produced by DorisParserParser#showBackends.
	VisitShowBackends(ctx *ShowBackendsContext) any

	// Visit a parse tree produced by DorisParserParser#showStages.
	VisitShowStages(ctx *ShowStagesContext) any

	// Visit a parse tree produced by DorisParserParser#showReplicaDistribution.
	VisitShowReplicaDistribution(ctx *ShowReplicaDistributionContext) any

	// Visit a parse tree produced by DorisParserParser#showResources.
	VisitShowResources(ctx *ShowResourcesContext) any

	// Visit a parse tree produced by DorisParserParser#showLoad.
	VisitShowLoad(ctx *ShowLoadContext) any

	// Visit a parse tree produced by DorisParserParser#showLoadWarings.
	VisitShowLoadWarings(ctx *ShowLoadWaringsContext) any

	// Visit a parse tree produced by DorisParserParser#showTriggers.
	VisitShowTriggers(ctx *ShowTriggersContext) any

	// Visit a parse tree produced by DorisParserParser#showDiagnoseTablet.
	VisitShowDiagnoseTablet(ctx *ShowDiagnoseTabletContext) any

	// Visit a parse tree produced by DorisParserParser#showOpenTables.
	VisitShowOpenTables(ctx *ShowOpenTablesContext) any

	// Visit a parse tree produced by DorisParserParser#showFrontends.
	VisitShowFrontends(ctx *ShowFrontendsContext) any

	// Visit a parse tree produced by DorisParserParser#showDatabaseId.
	VisitShowDatabaseId(ctx *ShowDatabaseIdContext) any

	// Visit a parse tree produced by DorisParserParser#showColumns.
	VisitShowColumns(ctx *ShowColumnsContext) any

	// Visit a parse tree produced by DorisParserParser#showTableId.
	VisitShowTableId(ctx *ShowTableIdContext) any

	// Visit a parse tree produced by DorisParserParser#showTrash.
	VisitShowTrash(ctx *ShowTrashContext) any

	// Visit a parse tree produced by DorisParserParser#showTypeCast.
	VisitShowTypeCast(ctx *ShowTypeCastContext) any

	// Visit a parse tree produced by DorisParserParser#showClusters.
	VisitShowClusters(ctx *ShowClustersContext) any

	// Visit a parse tree produced by DorisParserParser#showStatus.
	VisitShowStatus(ctx *ShowStatusContext) any

	// Visit a parse tree produced by DorisParserParser#showWhitelist.
	VisitShowWhitelist(ctx *ShowWhitelistContext) any

	// Visit a parse tree produced by DorisParserParser#showTabletsBelong.
	VisitShowTabletsBelong(ctx *ShowTabletsBelongContext) any

	// Visit a parse tree produced by DorisParserParser#showDataSkew.
	VisitShowDataSkew(ctx *ShowDataSkewContext) any

	// Visit a parse tree produced by DorisParserParser#showTableCreation.
	VisitShowTableCreation(ctx *ShowTableCreationContext) any

	// Visit a parse tree produced by DorisParserParser#showTabletStorageFormat.
	VisitShowTabletStorageFormat(ctx *ShowTabletStorageFormatContext) any

	// Visit a parse tree produced by DorisParserParser#showQueryProfile.
	VisitShowQueryProfile(ctx *ShowQueryProfileContext) any

	// Visit a parse tree produced by DorisParserParser#showConvertLsc.
	VisitShowConvertLsc(ctx *ShowConvertLscContext) any

	// Visit a parse tree produced by DorisParserParser#showTables.
	VisitShowTables(ctx *ShowTablesContext) any

	// Visit a parse tree produced by DorisParserParser#showViews.
	VisitShowViews(ctx *ShowViewsContext) any

	// Visit a parse tree produced by DorisParserParser#showTableStatus.
	VisitShowTableStatus(ctx *ShowTableStatusContext) any

	// Visit a parse tree produced by DorisParserParser#showDatabases.
	VisitShowDatabases(ctx *ShowDatabasesContext) any

	// Visit a parse tree produced by DorisParserParser#showTabletsFromTable.
	VisitShowTabletsFromTable(ctx *ShowTabletsFromTableContext) any

	// Visit a parse tree produced by DorisParserParser#showCatalogRecycleBin.
	VisitShowCatalogRecycleBin(ctx *ShowCatalogRecycleBinContext) any

	// Visit a parse tree produced by DorisParserParser#showTabletId.
	VisitShowTabletId(ctx *ShowTabletIdContext) any

	// Visit a parse tree produced by DorisParserParser#showDictionaries.
	VisitShowDictionaries(ctx *ShowDictionariesContext) any

	// Visit a parse tree produced by DorisParserParser#showTransaction.
	VisitShowTransaction(ctx *ShowTransactionContext) any

	// Visit a parse tree produced by DorisParserParser#showReplicaStatus.
	VisitShowReplicaStatus(ctx *ShowReplicaStatusContext) any

	// Visit a parse tree produced by DorisParserParser#showWorkloadGroups.
	VisitShowWorkloadGroups(ctx *ShowWorkloadGroupsContext) any

	// Visit a parse tree produced by DorisParserParser#showCopy.
	VisitShowCopy(ctx *ShowCopyContext) any

	// Visit a parse tree produced by DorisParserParser#showQueryStats.
	VisitShowQueryStats(ctx *ShowQueryStatsContext) any

	// Visit a parse tree produced by DorisParserParser#showIndex.
	VisitShowIndex(ctx *ShowIndexContext) any

	// Visit a parse tree produced by DorisParserParser#showWarmUpJob.
	VisitShowWarmUpJob(ctx *ShowWarmUpJobContext) any

	// Visit a parse tree produced by DorisParserParser#sync.
	VisitSync(ctx *SyncContext) any

	// Visit a parse tree produced by DorisParserParser#createRoutineLoadAlias.
	VisitCreateRoutineLoadAlias(ctx *CreateRoutineLoadAliasContext) any

	// Visit a parse tree produced by DorisParserParser#showCreateRoutineLoad.
	VisitShowCreateRoutineLoad(ctx *ShowCreateRoutineLoadContext) any

	// Visit a parse tree produced by DorisParserParser#pauseRoutineLoad.
	VisitPauseRoutineLoad(ctx *PauseRoutineLoadContext) any

	// Visit a parse tree produced by DorisParserParser#pauseAllRoutineLoad.
	VisitPauseAllRoutineLoad(ctx *PauseAllRoutineLoadContext) any

	// Visit a parse tree produced by DorisParserParser#resumeRoutineLoad.
	VisitResumeRoutineLoad(ctx *ResumeRoutineLoadContext) any

	// Visit a parse tree produced by DorisParserParser#resumeAllRoutineLoad.
	VisitResumeAllRoutineLoad(ctx *ResumeAllRoutineLoadContext) any

	// Visit a parse tree produced by DorisParserParser#stopRoutineLoad.
	VisitStopRoutineLoad(ctx *StopRoutineLoadContext) any

	// Visit a parse tree produced by DorisParserParser#showRoutineLoad.
	VisitShowRoutineLoad(ctx *ShowRoutineLoadContext) any

	// Visit a parse tree produced by DorisParserParser#showRoutineLoadTask.
	VisitShowRoutineLoadTask(ctx *ShowRoutineLoadTaskContext) any

	// Visit a parse tree produced by DorisParserParser#showIndexAnalyzer.
	VisitShowIndexAnalyzer(ctx *ShowIndexAnalyzerContext) any

	// Visit a parse tree produced by DorisParserParser#showIndexTokenizer.
	VisitShowIndexTokenizer(ctx *ShowIndexTokenizerContext) any

	// Visit a parse tree produced by DorisParserParser#showIndexTokenFilter.
	VisitShowIndexTokenFilter(ctx *ShowIndexTokenFilterContext) any

	// Visit a parse tree produced by DorisParserParser#killConnection.
	VisitKillConnection(ctx *KillConnectionContext) any

	// Visit a parse tree produced by DorisParserParser#killQuery.
	VisitKillQuery(ctx *KillQueryContext) any

	// Visit a parse tree produced by DorisParserParser#help.
	VisitHelp(ctx *HelpContext) any

	// Visit a parse tree produced by DorisParserParser#unlockTables.
	VisitUnlockTables(ctx *UnlockTablesContext) any

	// Visit a parse tree produced by DorisParserParser#installPlugin.
	VisitInstallPlugin(ctx *InstallPluginContext) any

	// Visit a parse tree produced by DorisParserParser#uninstallPlugin.
	VisitUninstallPlugin(ctx *UninstallPluginContext) any

	// Visit a parse tree produced by DorisParserParser#lockTables.
	VisitLockTables(ctx *LockTablesContext) any

	// Visit a parse tree produced by DorisParserParser#restore.
	VisitRestore(ctx *RestoreContext) any

	// Visit a parse tree produced by DorisParserParser#warmUpCluster.
	VisitWarmUpCluster(ctx *WarmUpClusterContext) any

	// Visit a parse tree produced by DorisParserParser#backup.
	VisitBackup(ctx *BackupContext) any

	// Visit a parse tree produced by DorisParserParser#unsupportedStartTransaction.
	VisitUnsupportedStartTransaction(ctx *UnsupportedStartTransactionContext) any

	// Visit a parse tree produced by DorisParserParser#warmUpItem.
	VisitWarmUpItem(ctx *WarmUpItemContext) any

	// Visit a parse tree produced by DorisParserParser#lockTable.
	VisitLockTable(ctx *LockTableContext) any

	// Visit a parse tree produced by DorisParserParser#createRoutineLoad.
	VisitCreateRoutineLoad(ctx *CreateRoutineLoadContext) any

	// Visit a parse tree produced by DorisParserParser#mysqlLoad.
	VisitMysqlLoad(ctx *MysqlLoadContext) any

	// Visit a parse tree produced by DorisParserParser#showCreateLoad.
	VisitShowCreateLoad(ctx *ShowCreateLoadContext) any

	// Visit a parse tree produced by DorisParserParser#separator.
	VisitSeparator(ctx *SeparatorContext) any

	// Visit a parse tree produced by DorisParserParser#importColumns.
	VisitImportColumns(ctx *ImportColumnsContext) any

	// Visit a parse tree produced by DorisParserParser#importPrecedingFilter.
	VisitImportPrecedingFilter(ctx *ImportPrecedingFilterContext) any

	// Visit a parse tree produced by DorisParserParser#importWhere.
	VisitImportWhere(ctx *ImportWhereContext) any

	// Visit a parse tree produced by DorisParserParser#importDeleteOn.
	VisitImportDeleteOn(ctx *ImportDeleteOnContext) any

	// Visit a parse tree produced by DorisParserParser#importSequence.
	VisitImportSequence(ctx *ImportSequenceContext) any

	// Visit a parse tree produced by DorisParserParser#importPartitions.
	VisitImportPartitions(ctx *ImportPartitionsContext) any

	// Visit a parse tree produced by DorisParserParser#importSequenceStatement.
	VisitImportSequenceStatement(ctx *ImportSequenceStatementContext) any

	// Visit a parse tree produced by DorisParserParser#importDeleteOnStatement.
	VisitImportDeleteOnStatement(ctx *ImportDeleteOnStatementContext) any

	// Visit a parse tree produced by DorisParserParser#importWhereStatement.
	VisitImportWhereStatement(ctx *ImportWhereStatementContext) any

	// Visit a parse tree produced by DorisParserParser#importPrecedingFilterStatement.
	VisitImportPrecedingFilterStatement(ctx *ImportPrecedingFilterStatementContext) any

	// Visit a parse tree produced by DorisParserParser#importColumnsStatement.
	VisitImportColumnsStatement(ctx *ImportColumnsStatementContext) any

	// Visit a parse tree produced by DorisParserParser#importColumnDesc.
	VisitImportColumnDesc(ctx *ImportColumnDescContext) any

	// Visit a parse tree produced by DorisParserParser#refreshCatalog.
	VisitRefreshCatalog(ctx *RefreshCatalogContext) any

	// Visit a parse tree produced by DorisParserParser#refreshDatabase.
	VisitRefreshDatabase(ctx *RefreshDatabaseContext) any

	// Visit a parse tree produced by DorisParserParser#refreshTable.
	VisitRefreshTable(ctx *RefreshTableContext) any

	// Visit a parse tree produced by DorisParserParser#refreshDictionary.
	VisitRefreshDictionary(ctx *RefreshDictionaryContext) any

	// Visit a parse tree produced by DorisParserParser#refreshLdap.
	VisitRefreshLdap(ctx *RefreshLdapContext) any

	// Visit a parse tree produced by DorisParserParser#cleanAllProfile.
	VisitCleanAllProfile(ctx *CleanAllProfileContext) any

	// Visit a parse tree produced by DorisParserParser#cleanLabel.
	VisitCleanLabel(ctx *CleanLabelContext) any

	// Visit a parse tree produced by DorisParserParser#cleanQueryStats.
	VisitCleanQueryStats(ctx *CleanQueryStatsContext) any

	// Visit a parse tree produced by DorisParserParser#cleanAllQueryStats.
	VisitCleanAllQueryStats(ctx *CleanAllQueryStatsContext) any

	// Visit a parse tree produced by DorisParserParser#cancelLoad.
	VisitCancelLoad(ctx *CancelLoadContext) any

	// Visit a parse tree produced by DorisParserParser#cancelExport.
	VisitCancelExport(ctx *CancelExportContext) any

	// Visit a parse tree produced by DorisParserParser#cancelWarmUpJob.
	VisitCancelWarmUpJob(ctx *CancelWarmUpJobContext) any

	// Visit a parse tree produced by DorisParserParser#cancelDecommisionBackend.
	VisitCancelDecommisionBackend(ctx *CancelDecommisionBackendContext) any

	// Visit a parse tree produced by DorisParserParser#cancelBackup.
	VisitCancelBackup(ctx *CancelBackupContext) any

	// Visit a parse tree produced by DorisParserParser#cancelRestore.
	VisitCancelRestore(ctx *CancelRestoreContext) any

	// Visit a parse tree produced by DorisParserParser#cancelBuildIndex.
	VisitCancelBuildIndex(ctx *CancelBuildIndexContext) any

	// Visit a parse tree produced by DorisParserParser#cancelAlterTable.
	VisitCancelAlterTable(ctx *CancelAlterTableContext) any

	// Visit a parse tree produced by DorisParserParser#adminShowReplicaDistribution.
	VisitAdminShowReplicaDistribution(ctx *AdminShowReplicaDistributionContext) any

	// Visit a parse tree produced by DorisParserParser#adminRebalanceDisk.
	VisitAdminRebalanceDisk(ctx *AdminRebalanceDiskContext) any

	// Visit a parse tree produced by DorisParserParser#adminCancelRebalanceDisk.
	VisitAdminCancelRebalanceDisk(ctx *AdminCancelRebalanceDiskContext) any

	// Visit a parse tree produced by DorisParserParser#adminDiagnoseTablet.
	VisitAdminDiagnoseTablet(ctx *AdminDiagnoseTabletContext) any

	// Visit a parse tree produced by DorisParserParser#adminShowReplicaStatus.
	VisitAdminShowReplicaStatus(ctx *AdminShowReplicaStatusContext) any

	// Visit a parse tree produced by DorisParserParser#adminCompactTable.
	VisitAdminCompactTable(ctx *AdminCompactTableContext) any

	// Visit a parse tree produced by DorisParserParser#adminCheckTablets.
	VisitAdminCheckTablets(ctx *AdminCheckTabletsContext) any

	// Visit a parse tree produced by DorisParserParser#adminShowTabletStorageFormat.
	VisitAdminShowTabletStorageFormat(ctx *AdminShowTabletStorageFormatContext) any

	// Visit a parse tree produced by DorisParserParser#adminSetFrontendConfig.
	VisitAdminSetFrontendConfig(ctx *AdminSetFrontendConfigContext) any

	// Visit a parse tree produced by DorisParserParser#adminCleanTrash.
	VisitAdminCleanTrash(ctx *AdminCleanTrashContext) any

	// Visit a parse tree produced by DorisParserParser#adminSetReplicaVersion.
	VisitAdminSetReplicaVersion(ctx *AdminSetReplicaVersionContext) any

	// Visit a parse tree produced by DorisParserParser#adminSetTableStatus.
	VisitAdminSetTableStatus(ctx *AdminSetTableStatusContext) any

	// Visit a parse tree produced by DorisParserParser#adminSetReplicaStatus.
	VisitAdminSetReplicaStatus(ctx *AdminSetReplicaStatusContext) any

	// Visit a parse tree produced by DorisParserParser#adminRepairTable.
	VisitAdminRepairTable(ctx *AdminRepairTableContext) any

	// Visit a parse tree produced by DorisParserParser#adminCancelRepairTable.
	VisitAdminCancelRepairTable(ctx *AdminCancelRepairTableContext) any

	// Visit a parse tree produced by DorisParserParser#adminCopyTablet.
	VisitAdminCopyTablet(ctx *AdminCopyTabletContext) any

	// Visit a parse tree produced by DorisParserParser#recoverDatabase.
	VisitRecoverDatabase(ctx *RecoverDatabaseContext) any

	// Visit a parse tree produced by DorisParserParser#recoverTable.
	VisitRecoverTable(ctx *RecoverTableContext) any

	// Visit a parse tree produced by DorisParserParser#recoverPartition.
	VisitRecoverPartition(ctx *RecoverPartitionContext) any

	// Visit a parse tree produced by DorisParserParser#adminSetPartitionVersion.
	VisitAdminSetPartitionVersion(ctx *AdminSetPartitionVersionContext) any

	// Visit a parse tree produced by DorisParserParser#baseTableRef.
	VisitBaseTableRef(ctx *BaseTableRefContext) any

	// Visit a parse tree produced by DorisParserParser#wildWhere.
	VisitWildWhere(ctx *WildWhereContext) any

	// Visit a parse tree produced by DorisParserParser#transactionBegin.
	VisitTransactionBegin(ctx *TransactionBeginContext) any

	// Visit a parse tree produced by DorisParserParser#transcationCommit.
	VisitTranscationCommit(ctx *TranscationCommitContext) any

	// Visit a parse tree produced by DorisParserParser#transactionRollback.
	VisitTransactionRollback(ctx *TransactionRollbackContext) any

	// Visit a parse tree produced by DorisParserParser#grantTablePrivilege.
	VisitGrantTablePrivilege(ctx *GrantTablePrivilegeContext) any

	// Visit a parse tree produced by DorisParserParser#grantResourcePrivilege.
	VisitGrantResourcePrivilege(ctx *GrantResourcePrivilegeContext) any

	// Visit a parse tree produced by DorisParserParser#grantRole.
	VisitGrantRole(ctx *GrantRoleContext) any

	// Visit a parse tree produced by DorisParserParser#revokeRole.
	VisitRevokeRole(ctx *RevokeRoleContext) any

	// Visit a parse tree produced by DorisParserParser#revokeResourcePrivilege.
	VisitRevokeResourcePrivilege(ctx *RevokeResourcePrivilegeContext) any

	// Visit a parse tree produced by DorisParserParser#revokeTablePrivilege.
	VisitRevokeTablePrivilege(ctx *RevokeTablePrivilegeContext) any

	// Visit a parse tree produced by DorisParserParser#privilege.
	VisitPrivilege(ctx *PrivilegeContext) any

	// Visit a parse tree produced by DorisParserParser#privilegeList.
	VisitPrivilegeList(ctx *PrivilegeListContext) any

	// Visit a parse tree produced by DorisParserParser#addBackendClause.
	VisitAddBackendClause(ctx *AddBackendClauseContext) any

	// Visit a parse tree produced by DorisParserParser#dropBackendClause.
	VisitDropBackendClause(ctx *DropBackendClauseContext) any

	// Visit a parse tree produced by DorisParserParser#decommissionBackendClause.
	VisitDecommissionBackendClause(ctx *DecommissionBackendClauseContext) any

	// Visit a parse tree produced by DorisParserParser#addObserverClause.
	VisitAddObserverClause(ctx *AddObserverClauseContext) any

	// Visit a parse tree produced by DorisParserParser#dropObserverClause.
	VisitDropObserverClause(ctx *DropObserverClauseContext) any

	// Visit a parse tree produced by DorisParserParser#addFollowerClause.
	VisitAddFollowerClause(ctx *AddFollowerClauseContext) any

	// Visit a parse tree produced by DorisParserParser#dropFollowerClause.
	VisitDropFollowerClause(ctx *DropFollowerClauseContext) any

	// Visit a parse tree produced by DorisParserParser#addBrokerClause.
	VisitAddBrokerClause(ctx *AddBrokerClauseContext) any

	// Visit a parse tree produced by DorisParserParser#dropBrokerClause.
	VisitDropBrokerClause(ctx *DropBrokerClauseContext) any

	// Visit a parse tree produced by DorisParserParser#dropAllBrokerClause.
	VisitDropAllBrokerClause(ctx *DropAllBrokerClauseContext) any

	// Visit a parse tree produced by DorisParserParser#alterLoadErrorUrlClause.
	VisitAlterLoadErrorUrlClause(ctx *AlterLoadErrorUrlClauseContext) any

	// Visit a parse tree produced by DorisParserParser#modifyBackendClause.
	VisitModifyBackendClause(ctx *ModifyBackendClauseContext) any

	// Visit a parse tree produced by DorisParserParser#modifyFrontendOrBackendHostNameClause.
	VisitModifyFrontendOrBackendHostNameClause(ctx *ModifyFrontendOrBackendHostNameClauseContext) any

	// Visit a parse tree produced by DorisParserParser#dropRollupClause.
	VisitDropRollupClause(ctx *DropRollupClauseContext) any

	// Visit a parse tree produced by DorisParserParser#addRollupClause.
	VisitAddRollupClause(ctx *AddRollupClauseContext) any

	// Visit a parse tree produced by DorisParserParser#addColumnClause.
	VisitAddColumnClause(ctx *AddColumnClauseContext) any

	// Visit a parse tree produced by DorisParserParser#addColumnsClause.
	VisitAddColumnsClause(ctx *AddColumnsClauseContext) any

	// Visit a parse tree produced by DorisParserParser#dropColumnClause.
	VisitDropColumnClause(ctx *DropColumnClauseContext) any

	// Visit a parse tree produced by DorisParserParser#modifyColumnClause.
	VisitModifyColumnClause(ctx *ModifyColumnClauseContext) any

	// Visit a parse tree produced by DorisParserParser#reorderColumnsClause.
	VisitReorderColumnsClause(ctx *ReorderColumnsClauseContext) any

	// Visit a parse tree produced by DorisParserParser#addPartitionClause.
	VisitAddPartitionClause(ctx *AddPartitionClauseContext) any

	// Visit a parse tree produced by DorisParserParser#dropPartitionClause.
	VisitDropPartitionClause(ctx *DropPartitionClauseContext) any

	// Visit a parse tree produced by DorisParserParser#modifyPartitionClause.
	VisitModifyPartitionClause(ctx *ModifyPartitionClauseContext) any

	// Visit a parse tree produced by DorisParserParser#replacePartitionClause.
	VisitReplacePartitionClause(ctx *ReplacePartitionClauseContext) any

	// Visit a parse tree produced by DorisParserParser#replaceTableClause.
	VisitReplaceTableClause(ctx *ReplaceTableClauseContext) any

	// Visit a parse tree produced by DorisParserParser#renameClause.
	VisitRenameClause(ctx *RenameClauseContext) any

	// Visit a parse tree produced by DorisParserParser#renameRollupClause.
	VisitRenameRollupClause(ctx *RenameRollupClauseContext) any

	// Visit a parse tree produced by DorisParserParser#renamePartitionClause.
	VisitRenamePartitionClause(ctx *RenamePartitionClauseContext) any

	// Visit a parse tree produced by DorisParserParser#renameColumnClause.
	VisitRenameColumnClause(ctx *RenameColumnClauseContext) any

	// Visit a parse tree produced by DorisParserParser#addIndexClause.
	VisitAddIndexClause(ctx *AddIndexClauseContext) any

	// Visit a parse tree produced by DorisParserParser#dropIndexClause.
	VisitDropIndexClause(ctx *DropIndexClauseContext) any

	// Visit a parse tree produced by DorisParserParser#enableFeatureClause.
	VisitEnableFeatureClause(ctx *EnableFeatureClauseContext) any

	// Visit a parse tree produced by DorisParserParser#modifyDistributionClause.
	VisitModifyDistributionClause(ctx *ModifyDistributionClauseContext) any

	// Visit a parse tree produced by DorisParserParser#modifyTableCommentClause.
	VisitModifyTableCommentClause(ctx *ModifyTableCommentClauseContext) any

	// Visit a parse tree produced by DorisParserParser#modifyColumnCommentClause.
	VisitModifyColumnCommentClause(ctx *ModifyColumnCommentClauseContext) any

	// Visit a parse tree produced by DorisParserParser#modifyEngineClause.
	VisitModifyEngineClause(ctx *ModifyEngineClauseContext) any

	// Visit a parse tree produced by DorisParserParser#alterMultiPartitionClause.
	VisitAlterMultiPartitionClause(ctx *AlterMultiPartitionClauseContext) any

	// Visit a parse tree produced by DorisParserParser#createOrReplaceTagClauses.
	VisitCreateOrReplaceTagClauses(ctx *CreateOrReplaceTagClausesContext) any

	// Visit a parse tree produced by DorisParserParser#createOrReplaceBranchClauses.
	VisitCreateOrReplaceBranchClauses(ctx *CreateOrReplaceBranchClausesContext) any

	// Visit a parse tree produced by DorisParserParser#dropBranchClauses.
	VisitDropBranchClauses(ctx *DropBranchClausesContext) any

	// Visit a parse tree produced by DorisParserParser#dropTagClauses.
	VisitDropTagClauses(ctx *DropTagClausesContext) any

	// Visit a parse tree produced by DorisParserParser#createOrReplaceTagClause.
	VisitCreateOrReplaceTagClause(ctx *CreateOrReplaceTagClauseContext) any

	// Visit a parse tree produced by DorisParserParser#createOrReplaceBranchClause.
	VisitCreateOrReplaceBranchClause(ctx *CreateOrReplaceBranchClauseContext) any

	// Visit a parse tree produced by DorisParserParser#tagOptions.
	VisitTagOptions(ctx *TagOptionsContext) any

	// Visit a parse tree produced by DorisParserParser#branchOptions.
	VisitBranchOptions(ctx *BranchOptionsContext) any

	// Visit a parse tree produced by DorisParserParser#retainTime.
	VisitRetainTime(ctx *RetainTimeContext) any

	// Visit a parse tree produced by DorisParserParser#retentionSnapshot.
	VisitRetentionSnapshot(ctx *RetentionSnapshotContext) any

	// Visit a parse tree produced by DorisParserParser#minSnapshotsToKeep.
	VisitMinSnapshotsToKeep(ctx *MinSnapshotsToKeepContext) any

	// Visit a parse tree produced by DorisParserParser#timeValueWithUnit.
	VisitTimeValueWithUnit(ctx *TimeValueWithUnitContext) any

	// Visit a parse tree produced by DorisParserParser#dropBranchClause.
	VisitDropBranchClause(ctx *DropBranchClauseContext) any

	// Visit a parse tree produced by DorisParserParser#dropTagClause.
	VisitDropTagClause(ctx *DropTagClauseContext) any

	// Visit a parse tree produced by DorisParserParser#columnPosition.
	VisitColumnPosition(ctx *ColumnPositionContext) any

	// Visit a parse tree produced by DorisParserParser#toRollup.
	VisitToRollup(ctx *ToRollupContext) any

	// Visit a parse tree produced by DorisParserParser#fromRollup.
	VisitFromRollup(ctx *FromRollupContext) any

	// Visit a parse tree produced by DorisParserParser#showAnalyze.
	VisitShowAnalyze(ctx *ShowAnalyzeContext) any

	// Visit a parse tree produced by DorisParserParser#showQueuedAnalyzeJobs.
	VisitShowQueuedAnalyzeJobs(ctx *ShowQueuedAnalyzeJobsContext) any

	// Visit a parse tree produced by DorisParserParser#showColumnHistogramStats.
	VisitShowColumnHistogramStats(ctx *ShowColumnHistogramStatsContext) any

	// Visit a parse tree produced by DorisParserParser#analyzeDatabase.
	VisitAnalyzeDatabase(ctx *AnalyzeDatabaseContext) any

	// Visit a parse tree produced by DorisParserParser#analyzeTable.
	VisitAnalyzeTable(ctx *AnalyzeTableContext) any

	// Visit a parse tree produced by DorisParserParser#alterTableStats.
	VisitAlterTableStats(ctx *AlterTableStatsContext) any

	// Visit a parse tree produced by DorisParserParser#alterColumnStats.
	VisitAlterColumnStats(ctx *AlterColumnStatsContext) any

	// Visit a parse tree produced by DorisParserParser#showIndexStats.
	VisitShowIndexStats(ctx *ShowIndexStatsContext) any

	// Visit a parse tree produced by DorisParserParser#dropStats.
	VisitDropStats(ctx *DropStatsContext) any

	// Visit a parse tree produced by DorisParserParser#dropCachedStats.
	VisitDropCachedStats(ctx *DropCachedStatsContext) any

	// Visit a parse tree produced by DorisParserParser#dropExpiredStats.
	VisitDropExpiredStats(ctx *DropExpiredStatsContext) any

	// Visit a parse tree produced by DorisParserParser#killAnalyzeJob.
	VisitKillAnalyzeJob(ctx *KillAnalyzeJobContext) any

	// Visit a parse tree produced by DorisParserParser#dropAnalyzeJob.
	VisitDropAnalyzeJob(ctx *DropAnalyzeJobContext) any

	// Visit a parse tree produced by DorisParserParser#showTableStats.
	VisitShowTableStats(ctx *ShowTableStatsContext) any

	// Visit a parse tree produced by DorisParserParser#showColumnStats.
	VisitShowColumnStats(ctx *ShowColumnStatsContext) any

	// Visit a parse tree produced by DorisParserParser#showAnalyzeTask.
	VisitShowAnalyzeTask(ctx *ShowAnalyzeTaskContext) any

	// Visit a parse tree produced by DorisParserParser#analyzeProperties.
	VisitAnalyzeProperties(ctx *AnalyzePropertiesContext) any

	// Visit a parse tree produced by DorisParserParser#workloadPolicyActions.
	VisitWorkloadPolicyActions(ctx *WorkloadPolicyActionsContext) any

	// Visit a parse tree produced by DorisParserParser#workloadPolicyAction.
	VisitWorkloadPolicyAction(ctx *WorkloadPolicyActionContext) any

	// Visit a parse tree produced by DorisParserParser#workloadPolicyConditions.
	VisitWorkloadPolicyConditions(ctx *WorkloadPolicyConditionsContext) any

	// Visit a parse tree produced by DorisParserParser#workloadPolicyCondition.
	VisitWorkloadPolicyCondition(ctx *WorkloadPolicyConditionContext) any

	// Visit a parse tree produced by DorisParserParser#storageBackend.
	VisitStorageBackend(ctx *StorageBackendContext) any

	// Visit a parse tree produced by DorisParserParser#passwordOption.
	VisitPasswordOption(ctx *PasswordOptionContext) any

	// Visit a parse tree produced by DorisParserParser#functionArguments.
	VisitFunctionArguments(ctx *FunctionArgumentsContext) any

	// Visit a parse tree produced by DorisParserParser#dataTypeList.
	VisitDataTypeList(ctx *DataTypeListContext) any

	// Visit a parse tree produced by DorisParserParser#setOptions.
	VisitSetOptions(ctx *SetOptionsContext) any

	// Visit a parse tree produced by DorisParserParser#setDefaultStorageVault.
	VisitSetDefaultStorageVault(ctx *SetDefaultStorageVaultContext) any

	// Visit a parse tree produced by DorisParserParser#setUserProperties.
	VisitSetUserProperties(ctx *SetUserPropertiesContext) any

	// Visit a parse tree produced by DorisParserParser#setTransaction.
	VisitSetTransaction(ctx *SetTransactionContext) any

	// Visit a parse tree produced by DorisParserParser#setVariableWithType.
	VisitSetVariableWithType(ctx *SetVariableWithTypeContext) any

	// Visit a parse tree produced by DorisParserParser#setNames.
	VisitSetNames(ctx *SetNamesContext) any

	// Visit a parse tree produced by DorisParserParser#setCharset.
	VisitSetCharset(ctx *SetCharsetContext) any

	// Visit a parse tree produced by DorisParserParser#setCollate.
	VisitSetCollate(ctx *SetCollateContext) any

	// Visit a parse tree produced by DorisParserParser#setPassword.
	VisitSetPassword(ctx *SetPasswordContext) any

	// Visit a parse tree produced by DorisParserParser#setLdapAdminPassword.
	VisitSetLdapAdminPassword(ctx *SetLdapAdminPasswordContext) any

	// Visit a parse tree produced by DorisParserParser#setVariableWithoutType.
	VisitSetVariableWithoutType(ctx *SetVariableWithoutTypeContext) any

	// Visit a parse tree produced by DorisParserParser#setSystemVariable.
	VisitSetSystemVariable(ctx *SetSystemVariableContext) any

	// Visit a parse tree produced by DorisParserParser#setUserVariable.
	VisitSetUserVariable(ctx *SetUserVariableContext) any

	// Visit a parse tree produced by DorisParserParser#transactionAccessMode.
	VisitTransactionAccessMode(ctx *TransactionAccessModeContext) any

	// Visit a parse tree produced by DorisParserParser#isolationLevel.
	VisitIsolationLevel(ctx *IsolationLevelContext) any

	// Visit a parse tree produced by DorisParserParser#supportedUnsetStatement.
	VisitSupportedUnsetStatement(ctx *SupportedUnsetStatementContext) any

	// Visit a parse tree produced by DorisParserParser#switchCatalog.
	VisitSwitchCatalog(ctx *SwitchCatalogContext) any

	// Visit a parse tree produced by DorisParserParser#useDatabase.
	VisitUseDatabase(ctx *UseDatabaseContext) any

	// Visit a parse tree produced by DorisParserParser#useCloudCluster.
	VisitUseCloudCluster(ctx *UseCloudClusterContext) any

	// Visit a parse tree produced by DorisParserParser#stageAndPattern.
	VisitStageAndPattern(ctx *StageAndPatternContext) any

	// Visit a parse tree produced by DorisParserParser#describeTableValuedFunction.
	VisitDescribeTableValuedFunction(ctx *DescribeTableValuedFunctionContext) any

	// Visit a parse tree produced by DorisParserParser#describeTableAll.
	VisitDescribeTableAll(ctx *DescribeTableAllContext) any

	// Visit a parse tree produced by DorisParserParser#describeTable.
	VisitDescribeTable(ctx *DescribeTableContext) any

	// Visit a parse tree produced by DorisParserParser#describeDictionary.
	VisitDescribeDictionary(ctx *DescribeDictionaryContext) any

	// Visit a parse tree produced by DorisParserParser#constraint.
	VisitConstraint(ctx *ConstraintContext) any

	// Visit a parse tree produced by DorisParserParser#partitionSpec.
	VisitPartitionSpec(ctx *PartitionSpecContext) any

	// Visit a parse tree produced by DorisParserParser#partitionTable.
	VisitPartitionTable(ctx *PartitionTableContext) any

	// Visit a parse tree produced by DorisParserParser#identityOrFunctionList.
	VisitIdentityOrFunctionList(ctx *IdentityOrFunctionListContext) any

	// Visit a parse tree produced by DorisParserParser#identityOrFunction.
	VisitIdentityOrFunction(ctx *IdentityOrFunctionContext) any

	// Visit a parse tree produced by DorisParserParser#dataDesc.
	VisitDataDesc(ctx *DataDescContext) any

	// Visit a parse tree produced by DorisParserParser#statementScope.
	VisitStatementScope(ctx *StatementScopeContext) any

	// Visit a parse tree produced by DorisParserParser#buildMode.
	VisitBuildMode(ctx *BuildModeContext) any

	// Visit a parse tree produced by DorisParserParser#refreshTrigger.
	VisitRefreshTrigger(ctx *RefreshTriggerContext) any

	// Visit a parse tree produced by DorisParserParser#refreshSchedule.
	VisitRefreshSchedule(ctx *RefreshScheduleContext) any

	// Visit a parse tree produced by DorisParserParser#refreshMethod.
	VisitRefreshMethod(ctx *RefreshMethodContext) any

	// Visit a parse tree produced by DorisParserParser#mvPartition.
	VisitMvPartition(ctx *MvPartitionContext) any

	// Visit a parse tree produced by DorisParserParser#identifierOrText.
	VisitIdentifierOrText(ctx *IdentifierOrTextContext) any

	// Visit a parse tree produced by DorisParserParser#identifierOrTextOrAsterisk.
	VisitIdentifierOrTextOrAsterisk(ctx *IdentifierOrTextOrAsteriskContext) any

	// Visit a parse tree produced by DorisParserParser#multipartIdentifierOrAsterisk.
	VisitMultipartIdentifierOrAsterisk(ctx *MultipartIdentifierOrAsteriskContext) any

	// Visit a parse tree produced by DorisParserParser#identifierOrAsterisk.
	VisitIdentifierOrAsterisk(ctx *IdentifierOrAsteriskContext) any

	// Visit a parse tree produced by DorisParserParser#userIdentify.
	VisitUserIdentify(ctx *UserIdentifyContext) any

	// Visit a parse tree produced by DorisParserParser#grantUserIdentify.
	VisitGrantUserIdentify(ctx *GrantUserIdentifyContext) any

	// Visit a parse tree produced by DorisParserParser#explain.
	VisitExplain(ctx *ExplainContext) any

	// Visit a parse tree produced by DorisParserParser#explainCommand.
	VisitExplainCommand(ctx *ExplainCommandContext) any

	// Visit a parse tree produced by DorisParserParser#planType.
	VisitPlanType(ctx *PlanTypeContext) any

	// Visit a parse tree produced by DorisParserParser#replayCommand.
	VisitReplayCommand(ctx *ReplayCommandContext) any

	// Visit a parse tree produced by DorisParserParser#replayType.
	VisitReplayType(ctx *ReplayTypeContext) any

	// Visit a parse tree produced by DorisParserParser#mergeType.
	VisitMergeType(ctx *MergeTypeContext) any

	// Visit a parse tree produced by DorisParserParser#preFilterClause.
	VisitPreFilterClause(ctx *PreFilterClauseContext) any

	// Visit a parse tree produced by DorisParserParser#deleteOnClause.
	VisitDeleteOnClause(ctx *DeleteOnClauseContext) any

	// Visit a parse tree produced by DorisParserParser#sequenceColClause.
	VisitSequenceColClause(ctx *SequenceColClauseContext) any

	// Visit a parse tree produced by DorisParserParser#colFromPath.
	VisitColFromPath(ctx *ColFromPathContext) any

	// Visit a parse tree produced by DorisParserParser#colMappingList.
	VisitColMappingList(ctx *ColMappingListContext) any

	// Visit a parse tree produced by DorisParserParser#mappingExpr.
	VisitMappingExpr(ctx *MappingExprContext) any

	// Visit a parse tree produced by DorisParserParser#withRemoteStorageSystem.
	VisitWithRemoteStorageSystem(ctx *WithRemoteStorageSystemContext) any

	// Visit a parse tree produced by DorisParserParser#resourceDesc.
	VisitResourceDesc(ctx *ResourceDescContext) any

	// Visit a parse tree produced by DorisParserParser#mysqlDataDesc.
	VisitMysqlDataDesc(ctx *MysqlDataDescContext) any

	// Visit a parse tree produced by DorisParserParser#skipLines.
	VisitSkipLines(ctx *SkipLinesContext) any

	// Visit a parse tree produced by DorisParserParser#outFileClause.
	VisitOutFileClause(ctx *OutFileClauseContext) any

	// Visit a parse tree produced by DorisParserParser#query.
	VisitQuery(ctx *QueryContext) any

	// Visit a parse tree produced by DorisParserParser#queryTermDefault.
	VisitQueryTermDefault(ctx *QueryTermDefaultContext) any

	// Visit a parse tree produced by DorisParserParser#setOperation.
	VisitSetOperation(ctx *SetOperationContext) any

	// Visit a parse tree produced by DorisParserParser#setQuantifier.
	VisitSetQuantifier(ctx *SetQuantifierContext) any

	// Visit a parse tree produced by DorisParserParser#queryPrimaryDefault.
	VisitQueryPrimaryDefault(ctx *QueryPrimaryDefaultContext) any

	// Visit a parse tree produced by DorisParserParser#subquery.
	VisitSubquery(ctx *SubqueryContext) any

	// Visit a parse tree produced by DorisParserParser#valuesTable.
	VisitValuesTable(ctx *ValuesTableContext) any

	// Visit a parse tree produced by DorisParserParser#regularQuerySpecification.
	VisitRegularQuerySpecification(ctx *RegularQuerySpecificationContext) any

	// Visit a parse tree produced by DorisParserParser#cte.
	VisitCte(ctx *CteContext) any

	// Visit a parse tree produced by DorisParserParser#aliasQuery.
	VisitAliasQuery(ctx *AliasQueryContext) any

	// Visit a parse tree produced by DorisParserParser#columnAliases.
	VisitColumnAliases(ctx *ColumnAliasesContext) any

	// Visit a parse tree produced by DorisParserParser#selectClause.
	VisitSelectClause(ctx *SelectClauseContext) any

	// Visit a parse tree produced by DorisParserParser#selectColumnClause.
	VisitSelectColumnClause(ctx *SelectColumnClauseContext) any

	// Visit a parse tree produced by DorisParserParser#whereClause.
	VisitWhereClause(ctx *WhereClauseContext) any

	// Visit a parse tree produced by DorisParserParser#fromClause.
	VisitFromClause(ctx *FromClauseContext) any

	// Visit a parse tree produced by DorisParserParser#intoClause.
	VisitIntoClause(ctx *IntoClauseContext) any

	// Visit a parse tree produced by DorisParserParser#bulkCollectClause.
	VisitBulkCollectClause(ctx *BulkCollectClauseContext) any

	// Visit a parse tree produced by DorisParserParser#tableRow.
	VisitTableRow(ctx *TableRowContext) any

	// Visit a parse tree produced by DorisParserParser#relations.
	VisitRelations(ctx *RelationsContext) any

	// Visit a parse tree produced by DorisParserParser#relation.
	VisitRelation(ctx *RelationContext) any

	// Visit a parse tree produced by DorisParserParser#joinRelation.
	VisitJoinRelation(ctx *JoinRelationContext) any

	// Visit a parse tree produced by DorisParserParser#bracketDistributeType.
	VisitBracketDistributeType(ctx *BracketDistributeTypeContext) any

	// Visit a parse tree produced by DorisParserParser#commentDistributeType.
	VisitCommentDistributeType(ctx *CommentDistributeTypeContext) any

	// Visit a parse tree produced by DorisParserParser#bracketRelationHint.
	VisitBracketRelationHint(ctx *BracketRelationHintContext) any

	// Visit a parse tree produced by DorisParserParser#commentRelationHint.
	VisitCommentRelationHint(ctx *CommentRelationHintContext) any

	// Visit a parse tree produced by DorisParserParser#aggClause.
	VisitAggClause(ctx *AggClauseContext) any

	// Visit a parse tree produced by DorisParserParser#groupingElement.
	VisitGroupingElement(ctx *GroupingElementContext) any

	// Visit a parse tree produced by DorisParserParser#groupingSet.
	VisitGroupingSet(ctx *GroupingSetContext) any

	// Visit a parse tree produced by DorisParserParser#havingClause.
	VisitHavingClause(ctx *HavingClauseContext) any

	// Visit a parse tree produced by DorisParserParser#qualifyClause.
	VisitQualifyClause(ctx *QualifyClauseContext) any

	// Visit a parse tree produced by DorisParserParser#selectHint.
	VisitSelectHint(ctx *SelectHintContext) any

	// Visit a parse tree produced by DorisParserParser#hintStatement.
	VisitHintStatement(ctx *HintStatementContext) any

	// Visit a parse tree produced by DorisParserParser#hintAssignment.
	VisitHintAssignment(ctx *HintAssignmentContext) any

	// Visit a parse tree produced by DorisParserParser#updateAssignment.
	VisitUpdateAssignment(ctx *UpdateAssignmentContext) any

	// Visit a parse tree produced by DorisParserParser#updateAssignmentSeq.
	VisitUpdateAssignmentSeq(ctx *UpdateAssignmentSeqContext) any

	// Visit a parse tree produced by DorisParserParser#lateralView.
	VisitLateralView(ctx *LateralViewContext) any

	// Visit a parse tree produced by DorisParserParser#queryOrganization.
	VisitQueryOrganization(ctx *QueryOrganizationContext) any

	// Visit a parse tree produced by DorisParserParser#sortClause.
	VisitSortClause(ctx *SortClauseContext) any

	// Visit a parse tree produced by DorisParserParser#sortItem.
	VisitSortItem(ctx *SortItemContext) any

	// Visit a parse tree produced by DorisParserParser#limitClause.
	VisitLimitClause(ctx *LimitClauseContext) any

	// Visit a parse tree produced by DorisParserParser#partitionClause.
	VisitPartitionClause(ctx *PartitionClauseContext) any

	// Visit a parse tree produced by DorisParserParser#joinType.
	VisitJoinType(ctx *JoinTypeContext) any

	// Visit a parse tree produced by DorisParserParser#joinCriteria.
	VisitJoinCriteria(ctx *JoinCriteriaContext) any

	// Visit a parse tree produced by DorisParserParser#identifierList.
	VisitIdentifierList(ctx *IdentifierListContext) any

	// Visit a parse tree produced by DorisParserParser#identifierSeq.
	VisitIdentifierSeq(ctx *IdentifierSeqContext) any

	// Visit a parse tree produced by DorisParserParser#optScanParams.
	VisitOptScanParams(ctx *OptScanParamsContext) any

	// Visit a parse tree produced by DorisParserParser#tableName.
	VisitTableName(ctx *TableNameContext) any

	// Visit a parse tree produced by DorisParserParser#aliasedQuery.
	VisitAliasedQuery(ctx *AliasedQueryContext) any

	// Visit a parse tree produced by DorisParserParser#tableValuedFunction.
	VisitTableValuedFunction(ctx *TableValuedFunctionContext) any

	// Visit a parse tree produced by DorisParserParser#relationList.
	VisitRelationList(ctx *RelationListContext) any

	// Visit a parse tree produced by DorisParserParser#materializedViewName.
	VisitMaterializedViewName(ctx *MaterializedViewNameContext) any

	// Visit a parse tree produced by DorisParserParser#propertyClause.
	VisitPropertyClause(ctx *PropertyClauseContext) any

	// Visit a parse tree produced by DorisParserParser#propertyItemList.
	VisitPropertyItemList(ctx *PropertyItemListContext) any

	// Visit a parse tree produced by DorisParserParser#propertyItem.
	VisitPropertyItem(ctx *PropertyItemContext) any

	// Visit a parse tree produced by DorisParserParser#propertyKey.
	VisitPropertyKey(ctx *PropertyKeyContext) any

	// Visit a parse tree produced by DorisParserParser#propertyValue.
	VisitPropertyValue(ctx *PropertyValueContext) any

	// Visit a parse tree produced by DorisParserParser#tableAlias.
	VisitTableAlias(ctx *TableAliasContext) any

	// Visit a parse tree produced by DorisParserParser#multipartIdentifier.
	VisitMultipartIdentifier(ctx *MultipartIdentifierContext) any

	// Visit a parse tree produced by DorisParserParser#simpleColumnDefs.
	VisitSimpleColumnDefs(ctx *SimpleColumnDefsContext) any

	// Visit a parse tree produced by DorisParserParser#simpleColumnDef.
	VisitSimpleColumnDef(ctx *SimpleColumnDefContext) any

	// Visit a parse tree produced by DorisParserParser#columnDefs.
	VisitColumnDefs(ctx *ColumnDefsContext) any

	// Visit a parse tree produced by DorisParserParser#columnDef.
	VisitColumnDef(ctx *ColumnDefContext) any

	// Visit a parse tree produced by DorisParserParser#indexDefs.
	VisitIndexDefs(ctx *IndexDefsContext) any

	// Visit a parse tree produced by DorisParserParser#indexDef.
	VisitIndexDef(ctx *IndexDefContext) any

	// Visit a parse tree produced by DorisParserParser#partitionsDef.
	VisitPartitionsDef(ctx *PartitionsDefContext) any

	// Visit a parse tree produced by DorisParserParser#partitionDef.
	VisitPartitionDef(ctx *PartitionDefContext) any

	// Visit a parse tree produced by DorisParserParser#lessThanPartitionDef.
	VisitLessThanPartitionDef(ctx *LessThanPartitionDefContext) any

	// Visit a parse tree produced by DorisParserParser#fixedPartitionDef.
	VisitFixedPartitionDef(ctx *FixedPartitionDefContext) any

	// Visit a parse tree produced by DorisParserParser#stepPartitionDef.
	VisitStepPartitionDef(ctx *StepPartitionDefContext) any

	// Visit a parse tree produced by DorisParserParser#inPartitionDef.
	VisitInPartitionDef(ctx *InPartitionDefContext) any

	// Visit a parse tree produced by DorisParserParser#partitionValueList.
	VisitPartitionValueList(ctx *PartitionValueListContext) any

	// Visit a parse tree produced by DorisParserParser#partitionValueDef.
	VisitPartitionValueDef(ctx *PartitionValueDefContext) any

	// Visit a parse tree produced by DorisParserParser#rollupDefs.
	VisitRollupDefs(ctx *RollupDefsContext) any

	// Visit a parse tree produced by DorisParserParser#rollupDef.
	VisitRollupDef(ctx *RollupDefContext) any

	// Visit a parse tree produced by DorisParserParser#aggTypeDef.
	VisitAggTypeDef(ctx *AggTypeDefContext) any

	// Visit a parse tree produced by DorisParserParser#tabletList.
	VisitTabletList(ctx *TabletListContext) any

	// Visit a parse tree produced by DorisParserParser#inlineTable.
	VisitInlineTable(ctx *InlineTableContext) any

	// Visit a parse tree produced by DorisParserParser#namedExpression.
	VisitNamedExpression(ctx *NamedExpressionContext) any

	// Visit a parse tree produced by DorisParserParser#namedExpressionSeq.
	VisitNamedExpressionSeq(ctx *NamedExpressionSeqContext) any

	// Visit a parse tree produced by DorisParserParser#expression.
	VisitExpression(ctx *ExpressionContext) any

	// Visit a parse tree produced by DorisParserParser#lambdaExpression.
	VisitLambdaExpression(ctx *LambdaExpressionContext) any

	// Visit a parse tree produced by DorisParserParser#exist.
	VisitExist(ctx *ExistContext) any

	// Visit a parse tree produced by DorisParserParser#logicalNot.
	VisitLogicalNot(ctx *LogicalNotContext) any

	// Visit a parse tree produced by DorisParserParser#predicated.
	VisitPredicated(ctx *PredicatedContext) any

	// Visit a parse tree produced by DorisParserParser#isnull.
	VisitIsnull(ctx *IsnullContext) any

	// Visit a parse tree produced by DorisParserParser#is_not_null_pred.
	VisitIs_not_null_pred(ctx *Is_not_null_predContext) any

	// Visit a parse tree produced by DorisParserParser#logicalBinary.
	VisitLogicalBinary(ctx *LogicalBinaryContext) any

	// Visit a parse tree produced by DorisParserParser#doublePipes.
	VisitDoublePipes(ctx *DoublePipesContext) any

	// Visit a parse tree produced by DorisParserParser#rowConstructor.
	VisitRowConstructor(ctx *RowConstructorContext) any

	// Visit a parse tree produced by DorisParserParser#rowConstructorItem.
	VisitRowConstructorItem(ctx *RowConstructorItemContext) any

	// Visit a parse tree produced by DorisParserParser#predicate.
	VisitPredicate(ctx *PredicateContext) any

	// Visit a parse tree produced by DorisParserParser#valueExpressionDefault.
	VisitValueExpressionDefault(ctx *ValueExpressionDefaultContext) any

	// Visit a parse tree produced by DorisParserParser#comparison.
	VisitComparison(ctx *ComparisonContext) any

	// Visit a parse tree produced by DorisParserParser#arithmeticBinary.
	VisitArithmeticBinary(ctx *ArithmeticBinaryContext) any

	// Visit a parse tree produced by DorisParserParser#arithmeticUnary.
	VisitArithmeticUnary(ctx *ArithmeticUnaryContext) any

	// Visit a parse tree produced by DorisParserParser#dereference.
	VisitDereference(ctx *DereferenceContext) any

	// Visit a parse tree produced by DorisParserParser#currentDate.
	VisitCurrentDate(ctx *CurrentDateContext) any

	// Visit a parse tree produced by DorisParserParser#cast.
	VisitCast(ctx *CastContext) any

	// Visit a parse tree produced by DorisParserParser#parenthesizedExpression.
	VisitParenthesizedExpression(ctx *ParenthesizedExpressionContext) any

	// Visit a parse tree produced by DorisParserParser#userVariable.
	VisitUserVariable(ctx *UserVariableContext) any

	// Visit a parse tree produced by DorisParserParser#elementAt.
	VisitElementAt(ctx *ElementAtContext) any

	// Visit a parse tree produced by DorisParserParser#localTimestamp.
	VisitLocalTimestamp(ctx *LocalTimestampContext) any

	// Visit a parse tree produced by DorisParserParser#charFunction.
	VisitCharFunction(ctx *CharFunctionContext) any

	// Visit a parse tree produced by DorisParserParser#intervalLiteral.
	VisitIntervalLiteral(ctx *IntervalLiteralContext) any

	// Visit a parse tree produced by DorisParserParser#simpleCase.
	VisitSimpleCase(ctx *SimpleCaseContext) any

	// Visit a parse tree produced by DorisParserParser#columnReference.
	VisitColumnReference(ctx *ColumnReferenceContext) any

	// Visit a parse tree produced by DorisParserParser#star.
	VisitStar(ctx *StarContext) any

	// Visit a parse tree produced by DorisParserParser#sessionUser.
	VisitSessionUser(ctx *SessionUserContext) any

	// Visit a parse tree produced by DorisParserParser#convertType.
	VisitConvertType(ctx *ConvertTypeContext) any

	// Visit a parse tree produced by DorisParserParser#convertCharSet.
	VisitConvertCharSet(ctx *ConvertCharSetContext) any

	// Visit a parse tree produced by DorisParserParser#subqueryExpression.
	VisitSubqueryExpression(ctx *SubqueryExpressionContext) any

	// Visit a parse tree produced by DorisParserParser#encryptKey.
	VisitEncryptKey(ctx *EncryptKeyContext) any

	// Visit a parse tree produced by DorisParserParser#currentTime.
	VisitCurrentTime(ctx *CurrentTimeContext) any

	// Visit a parse tree produced by DorisParserParser#localTime.
	VisitLocalTime(ctx *LocalTimeContext) any

	// Visit a parse tree produced by DorisParserParser#systemVariable.
	VisitSystemVariable(ctx *SystemVariableContext) any

	// Visit a parse tree produced by DorisParserParser#collate.
	VisitCollate(ctx *CollateContext) any

	// Visit a parse tree produced by DorisParserParser#currentUser.
	VisitCurrentUser(ctx *CurrentUserContext) any

	// Visit a parse tree produced by DorisParserParser#constantDefault.
	VisitConstantDefault(ctx *ConstantDefaultContext) any

	// Visit a parse tree produced by DorisParserParser#extract.
	VisitExtract(ctx *ExtractContext) any

	// Visit a parse tree produced by DorisParserParser#currentTimestamp.
	VisitCurrentTimestamp(ctx *CurrentTimestampContext) any

	// Visit a parse tree produced by DorisParserParser#functionCall.
	VisitFunctionCall(ctx *FunctionCallContext) any

	// Visit a parse tree produced by DorisParserParser#arraySlice.
	VisitArraySlice(ctx *ArraySliceContext) any

	// Visit a parse tree produced by DorisParserParser#searchedCase.
	VisitSearchedCase(ctx *SearchedCaseContext) any

	// Visit a parse tree produced by DorisParserParser#except.
	VisitExcept(ctx *ExceptContext) any

	// Visit a parse tree produced by DorisParserParser#replace.
	VisitReplace(ctx *ReplaceContext) any

	// Visit a parse tree produced by DorisParserParser#castDataType.
	VisitCastDataType(ctx *CastDataTypeContext) any

	// Visit a parse tree produced by DorisParserParser#functionCallExpression.
	VisitFunctionCallExpression(ctx *FunctionCallExpressionContext) any

	// Visit a parse tree produced by DorisParserParser#functionIdentifier.
	VisitFunctionIdentifier(ctx *FunctionIdentifierContext) any

	// Visit a parse tree produced by DorisParserParser#functionNameIdentifier.
	VisitFunctionNameIdentifier(ctx *FunctionNameIdentifierContext) any

	// Visit a parse tree produced by DorisParserParser#windowSpec.
	VisitWindowSpec(ctx *WindowSpecContext) any

	// Visit a parse tree produced by DorisParserParser#windowFrame.
	VisitWindowFrame(ctx *WindowFrameContext) any

	// Visit a parse tree produced by DorisParserParser#frameUnits.
	VisitFrameUnits(ctx *FrameUnitsContext) any

	// Visit a parse tree produced by DorisParserParser#frameBoundary.
	VisitFrameBoundary(ctx *FrameBoundaryContext) any

	// Visit a parse tree produced by DorisParserParser#qualifiedName.
	VisitQualifiedName(ctx *QualifiedNameContext) any

	// Visit a parse tree produced by DorisParserParser#specifiedPartition.
	VisitSpecifiedPartition(ctx *SpecifiedPartitionContext) any

	// Visit a parse tree produced by DorisParserParser#nullLiteral.
	VisitNullLiteral(ctx *NullLiteralContext) any

	// Visit a parse tree produced by DorisParserParser#typeConstructor.
	VisitTypeConstructor(ctx *TypeConstructorContext) any

	// Visit a parse tree produced by DorisParserParser#numericLiteral.
	VisitNumericLiteral(ctx *NumericLiteralContext) any

	// Visit a parse tree produced by DorisParserParser#booleanLiteral.
	VisitBooleanLiteral(ctx *BooleanLiteralContext) any

	// Visit a parse tree produced by DorisParserParser#stringLiteral.
	VisitStringLiteral(ctx *StringLiteralContext) any

	// Visit a parse tree produced by DorisParserParser#arrayLiteral.
	VisitArrayLiteral(ctx *ArrayLiteralContext) any

	// Visit a parse tree produced by DorisParserParser#mapLiteral.
	VisitMapLiteral(ctx *MapLiteralContext) any

	// Visit a parse tree produced by DorisParserParser#structLiteral.
	VisitStructLiteral(ctx *StructLiteralContext) any

	// Visit a parse tree produced by DorisParserParser#placeholder.
	VisitPlaceholder(ctx *PlaceholderContext) any

	// Visit a parse tree produced by DorisParserParser#comparisonOperator.
	VisitComparisonOperator(ctx *ComparisonOperatorContext) any

	// Visit a parse tree produced by DorisParserParser#booleanValue.
	VisitBooleanValue(ctx *BooleanValueContext) any

	// Visit a parse tree produced by DorisParserParser#whenClause.
	VisitWhenClause(ctx *WhenClauseContext) any

	// Visit a parse tree produced by DorisParserParser#interval.
	VisitInterval(ctx *IntervalContext) any

	// Visit a parse tree produced by DorisParserParser#unitIdentifier.
	VisitUnitIdentifier(ctx *UnitIdentifierContext) any

	// Visit a parse tree produced by DorisParserParser#dataTypeWithNullable.
	VisitDataTypeWithNullable(ctx *DataTypeWithNullableContext) any

	// Visit a parse tree produced by DorisParserParser#complexDataType.
	VisitComplexDataType(ctx *ComplexDataTypeContext) any

	// Visit a parse tree produced by DorisParserParser#aggStateDataType.
	VisitAggStateDataType(ctx *AggStateDataTypeContext) any

	// Visit a parse tree produced by DorisParserParser#primitiveDataType.
	VisitPrimitiveDataType(ctx *PrimitiveDataTypeContext) any

	// Visit a parse tree produced by DorisParserParser#primitiveColType.
	VisitPrimitiveColType(ctx *PrimitiveColTypeContext) any

	// Visit a parse tree produced by DorisParserParser#complexColTypeList.
	VisitComplexColTypeList(ctx *ComplexColTypeListContext) any

	// Visit a parse tree produced by DorisParserParser#complexColType.
	VisitComplexColType(ctx *ComplexColTypeContext) any

	// Visit a parse tree produced by DorisParserParser#commentSpec.
	VisitCommentSpec(ctx *CommentSpecContext) any

	// Visit a parse tree produced by DorisParserParser#sample.
	VisitSample(ctx *SampleContext) any

	// Visit a parse tree produced by DorisParserParser#sampleByPercentile.
	VisitSampleByPercentile(ctx *SampleByPercentileContext) any

	// Visit a parse tree produced by DorisParserParser#sampleByRows.
	VisitSampleByRows(ctx *SampleByRowsContext) any

	// Visit a parse tree produced by DorisParserParser#tableSnapshot.
	VisitTableSnapshot(ctx *TableSnapshotContext) any

	// Visit a parse tree produced by DorisParserParser#errorCapturingIdentifier.
	VisitErrorCapturingIdentifier(ctx *ErrorCapturingIdentifierContext) any

	// Visit a parse tree produced by DorisParserParser#errorIdent.
	VisitErrorIdent(ctx *ErrorIdentContext) any

	// Visit a parse tree produced by DorisParserParser#realIdent.
	VisitRealIdent(ctx *RealIdentContext) any

	// Visit a parse tree produced by DorisParserParser#identifier.
	VisitIdentifier(ctx *IdentifierContext) any

	// Visit a parse tree produced by DorisParserParser#unquotedIdentifier.
	VisitUnquotedIdentifier(ctx *UnquotedIdentifierContext) any

	// Visit a parse tree produced by DorisParserParser#quotedIdentifierAlternative.
	VisitQuotedIdentifierAlternative(ctx *QuotedIdentifierAlternativeContext) any

	// Visit a parse tree produced by DorisParserParser#quotedIdentifier.
	VisitQuotedIdentifier(ctx *QuotedIdentifierContext) any

	// Visit a parse tree produced by DorisParserParser#integerLiteral.
	VisitIntegerLiteral(ctx *IntegerLiteralContext) any

	// Visit a parse tree produced by DorisParserParser#decimalLiteral.
	VisitDecimalLiteral(ctx *DecimalLiteralContext) any

	// Visit a parse tree produced by DorisParserParser#nonReserved.
	VisitNonReserved(ctx *NonReservedContext) any
}
