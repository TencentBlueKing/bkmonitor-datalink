// Code generated from DorisParser.g4 by ANTLR 4.13.2. DO NOT EDIT.

package gen // DorisParser
import "github.com/antlr4-go/antlr/v4"

// DorisParserListener is a complete listener for a parse tree produced by DorisParserParser.
type DorisParserListener interface {
	antlr.ParseTreeListener

	// EnterMultiStatements is called when entering the multiStatements production.
	EnterMultiStatements(c *MultiStatementsContext)

	// EnterSingleStatement is called when entering the singleStatement production.
	EnterSingleStatement(c *SingleStatementContext)

	// EnterStatementBaseAlias is called when entering the statementBaseAlias production.
	EnterStatementBaseAlias(c *StatementBaseAliasContext)

	// EnterCallProcedure is called when entering the callProcedure production.
	EnterCallProcedure(c *CallProcedureContext)

	// EnterCreateProcedure is called when entering the createProcedure production.
	EnterCreateProcedure(c *CreateProcedureContext)

	// EnterDropProcedure is called when entering the dropProcedure production.
	EnterDropProcedure(c *DropProcedureContext)

	// EnterShowProcedureStatus is called when entering the showProcedureStatus production.
	EnterShowProcedureStatus(c *ShowProcedureStatusContext)

	// EnterShowCreateProcedure is called when entering the showCreateProcedure production.
	EnterShowCreateProcedure(c *ShowCreateProcedureContext)

	// EnterShowConfig is called when entering the showConfig production.
	EnterShowConfig(c *ShowConfigContext)

	// EnterStatementDefault is called when entering the statementDefault production.
	EnterStatementDefault(c *StatementDefaultContext)

	// EnterSupportedDmlStatementAlias is called when entering the supportedDmlStatementAlias production.
	EnterSupportedDmlStatementAlias(c *SupportedDmlStatementAliasContext)

	// EnterSupportedCreateStatementAlias is called when entering the supportedCreateStatementAlias production.
	EnterSupportedCreateStatementAlias(c *SupportedCreateStatementAliasContext)

	// EnterSupportedAlterStatementAlias is called when entering the supportedAlterStatementAlias production.
	EnterSupportedAlterStatementAlias(c *SupportedAlterStatementAliasContext)

	// EnterMaterializedViewStatementAlias is called when entering the materializedViewStatementAlias production.
	EnterMaterializedViewStatementAlias(c *MaterializedViewStatementAliasContext)

	// EnterSupportedJobStatementAlias is called when entering the supportedJobStatementAlias production.
	EnterSupportedJobStatementAlias(c *SupportedJobStatementAliasContext)

	// EnterConstraintStatementAlias is called when entering the constraintStatementAlias production.
	EnterConstraintStatementAlias(c *ConstraintStatementAliasContext)

	// EnterSupportedCleanStatementAlias is called when entering the supportedCleanStatementAlias production.
	EnterSupportedCleanStatementAlias(c *SupportedCleanStatementAliasContext)

	// EnterSupportedDescribeStatementAlias is called when entering the supportedDescribeStatementAlias production.
	EnterSupportedDescribeStatementAlias(c *SupportedDescribeStatementAliasContext)

	// EnterSupportedDropStatementAlias is called when entering the supportedDropStatementAlias production.
	EnterSupportedDropStatementAlias(c *SupportedDropStatementAliasContext)

	// EnterSupportedSetStatementAlias is called when entering the supportedSetStatementAlias production.
	EnterSupportedSetStatementAlias(c *SupportedSetStatementAliasContext)

	// EnterSupportedUnsetStatementAlias is called when entering the supportedUnsetStatementAlias production.
	EnterSupportedUnsetStatementAlias(c *SupportedUnsetStatementAliasContext)

	// EnterSupportedRefreshStatementAlias is called when entering the supportedRefreshStatementAlias production.
	EnterSupportedRefreshStatementAlias(c *SupportedRefreshStatementAliasContext)

	// EnterSupportedShowStatementAlias is called when entering the supportedShowStatementAlias production.
	EnterSupportedShowStatementAlias(c *SupportedShowStatementAliasContext)

	// EnterSupportedLoadStatementAlias is called when entering the supportedLoadStatementAlias production.
	EnterSupportedLoadStatementAlias(c *SupportedLoadStatementAliasContext)

	// EnterSupportedCancelStatementAlias is called when entering the supportedCancelStatementAlias production.
	EnterSupportedCancelStatementAlias(c *SupportedCancelStatementAliasContext)

	// EnterSupportedRecoverStatementAlias is called when entering the supportedRecoverStatementAlias production.
	EnterSupportedRecoverStatementAlias(c *SupportedRecoverStatementAliasContext)

	// EnterSupportedAdminStatementAlias is called when entering the supportedAdminStatementAlias production.
	EnterSupportedAdminStatementAlias(c *SupportedAdminStatementAliasContext)

	// EnterSupportedUseStatementAlias is called when entering the supportedUseStatementAlias production.
	EnterSupportedUseStatementAlias(c *SupportedUseStatementAliasContext)

	// EnterSupportedOtherStatementAlias is called when entering the supportedOtherStatementAlias production.
	EnterSupportedOtherStatementAlias(c *SupportedOtherStatementAliasContext)

	// EnterSupportedKillStatementAlias is called when entering the supportedKillStatementAlias production.
	EnterSupportedKillStatementAlias(c *SupportedKillStatementAliasContext)

	// EnterSupportedStatsStatementAlias is called when entering the supportedStatsStatementAlias production.
	EnterSupportedStatsStatementAlias(c *SupportedStatsStatementAliasContext)

	// EnterSupportedTransactionStatementAlias is called when entering the supportedTransactionStatementAlias production.
	EnterSupportedTransactionStatementAlias(c *SupportedTransactionStatementAliasContext)

	// EnterSupportedGrantRevokeStatementAlias is called when entering the supportedGrantRevokeStatementAlias production.
	EnterSupportedGrantRevokeStatementAlias(c *SupportedGrantRevokeStatementAliasContext)

	// EnterUnsupported is called when entering the unsupported production.
	EnterUnsupported(c *UnsupportedContext)

	// EnterUnsupportedStatement is called when entering the unsupportedStatement production.
	EnterUnsupportedStatement(c *UnsupportedStatementContext)

	// EnterCreateMTMV is called when entering the createMTMV production.
	EnterCreateMTMV(c *CreateMTMVContext)

	// EnterRefreshMTMV is called when entering the refreshMTMV production.
	EnterRefreshMTMV(c *RefreshMTMVContext)

	// EnterAlterMTMV is called when entering the alterMTMV production.
	EnterAlterMTMV(c *AlterMTMVContext)

	// EnterDropMTMV is called when entering the dropMTMV production.
	EnterDropMTMV(c *DropMTMVContext)

	// EnterPauseMTMV is called when entering the pauseMTMV production.
	EnterPauseMTMV(c *PauseMTMVContext)

	// EnterResumeMTMV is called when entering the resumeMTMV production.
	EnterResumeMTMV(c *ResumeMTMVContext)

	// EnterCancelMTMVTask is called when entering the cancelMTMVTask production.
	EnterCancelMTMVTask(c *CancelMTMVTaskContext)

	// EnterShowCreateMTMV is called when entering the showCreateMTMV production.
	EnterShowCreateMTMV(c *ShowCreateMTMVContext)

	// EnterCreateScheduledJob is called when entering the createScheduledJob production.
	EnterCreateScheduledJob(c *CreateScheduledJobContext)

	// EnterPauseJob is called when entering the pauseJob production.
	EnterPauseJob(c *PauseJobContext)

	// EnterDropJob is called when entering the dropJob production.
	EnterDropJob(c *DropJobContext)

	// EnterResumeJob is called when entering the resumeJob production.
	EnterResumeJob(c *ResumeJobContext)

	// EnterCancelJobTask is called when entering the cancelJobTask production.
	EnterCancelJobTask(c *CancelJobTaskContext)

	// EnterAddConstraint is called when entering the addConstraint production.
	EnterAddConstraint(c *AddConstraintContext)

	// EnterDropConstraint is called when entering the dropConstraint production.
	EnterDropConstraint(c *DropConstraintContext)

	// EnterShowConstraint is called when entering the showConstraint production.
	EnterShowConstraint(c *ShowConstraintContext)

	// EnterInsertTable is called when entering the insertTable production.
	EnterInsertTable(c *InsertTableContext)

	// EnterUpdate is called when entering the update production.
	EnterUpdate(c *UpdateContext)

	// EnterDelete is called when entering the delete production.
	EnterDelete(c *DeleteContext)

	// EnterLoad is called when entering the load production.
	EnterLoad(c *LoadContext)

	// EnterExport is called when entering the export production.
	EnterExport(c *ExportContext)

	// EnterReplay is called when entering the replay production.
	EnterReplay(c *ReplayContext)

	// EnterCopyInto is called when entering the copyInto production.
	EnterCopyInto(c *CopyIntoContext)

	// EnterTruncateTable is called when entering the truncateTable production.
	EnterTruncateTable(c *TruncateTableContext)

	// EnterCreateTable is called when entering the createTable production.
	EnterCreateTable(c *CreateTableContext)

	// EnterCreateView is called when entering the createView production.
	EnterCreateView(c *CreateViewContext)

	// EnterCreateFile is called when entering the createFile production.
	EnterCreateFile(c *CreateFileContext)

	// EnterCreateTableLike is called when entering the createTableLike production.
	EnterCreateTableLike(c *CreateTableLikeContext)

	// EnterCreateRole is called when entering the createRole production.
	EnterCreateRole(c *CreateRoleContext)

	// EnterCreateWorkloadGroup is called when entering the createWorkloadGroup production.
	EnterCreateWorkloadGroup(c *CreateWorkloadGroupContext)

	// EnterCreateCatalog is called when entering the createCatalog production.
	EnterCreateCatalog(c *CreateCatalogContext)

	// EnterCreateRowPolicy is called when entering the createRowPolicy production.
	EnterCreateRowPolicy(c *CreateRowPolicyContext)

	// EnterCreateStoragePolicy is called when entering the createStoragePolicy production.
	EnterCreateStoragePolicy(c *CreateStoragePolicyContext)

	// EnterBuildIndex is called when entering the buildIndex production.
	EnterBuildIndex(c *BuildIndexContext)

	// EnterCreateIndex is called when entering the createIndex production.
	EnterCreateIndex(c *CreateIndexContext)

	// EnterCreateWorkloadPolicy is called when entering the createWorkloadPolicy production.
	EnterCreateWorkloadPolicy(c *CreateWorkloadPolicyContext)

	// EnterCreateSqlBlockRule is called when entering the createSqlBlockRule production.
	EnterCreateSqlBlockRule(c *CreateSqlBlockRuleContext)

	// EnterCreateEncryptkey is called when entering the createEncryptkey production.
	EnterCreateEncryptkey(c *CreateEncryptkeyContext)

	// EnterCreateUserDefineFunction is called when entering the createUserDefineFunction production.
	EnterCreateUserDefineFunction(c *CreateUserDefineFunctionContext)

	// EnterCreateAliasFunction is called when entering the createAliasFunction production.
	EnterCreateAliasFunction(c *CreateAliasFunctionContext)

	// EnterCreateUser is called when entering the createUser production.
	EnterCreateUser(c *CreateUserContext)

	// EnterCreateDatabase is called when entering the createDatabase production.
	EnterCreateDatabase(c *CreateDatabaseContext)

	// EnterCreateRepository is called when entering the createRepository production.
	EnterCreateRepository(c *CreateRepositoryContext)

	// EnterCreateResource is called when entering the createResource production.
	EnterCreateResource(c *CreateResourceContext)

	// EnterCreateDictionary is called when entering the createDictionary production.
	EnterCreateDictionary(c *CreateDictionaryContext)

	// EnterCreateStage is called when entering the createStage production.
	EnterCreateStage(c *CreateStageContext)

	// EnterCreateStorageVault is called when entering the createStorageVault production.
	EnterCreateStorageVault(c *CreateStorageVaultContext)

	// EnterCreateIndexAnalyzer is called when entering the createIndexAnalyzer production.
	EnterCreateIndexAnalyzer(c *CreateIndexAnalyzerContext)

	// EnterCreateIndexTokenizer is called when entering the createIndexTokenizer production.
	EnterCreateIndexTokenizer(c *CreateIndexTokenizerContext)

	// EnterCreateIndexTokenFilter is called when entering the createIndexTokenFilter production.
	EnterCreateIndexTokenFilter(c *CreateIndexTokenFilterContext)

	// EnterDictionaryColumnDefs is called when entering the dictionaryColumnDefs production.
	EnterDictionaryColumnDefs(c *DictionaryColumnDefsContext)

	// EnterDictionaryColumnDef is called when entering the dictionaryColumnDef production.
	EnterDictionaryColumnDef(c *DictionaryColumnDefContext)

	// EnterAlterSystem is called when entering the alterSystem production.
	EnterAlterSystem(c *AlterSystemContext)

	// EnterAlterView is called when entering the alterView production.
	EnterAlterView(c *AlterViewContext)

	// EnterAlterCatalogRename is called when entering the alterCatalogRename production.
	EnterAlterCatalogRename(c *AlterCatalogRenameContext)

	// EnterAlterRole is called when entering the alterRole production.
	EnterAlterRole(c *AlterRoleContext)

	// EnterAlterStorageVault is called when entering the alterStorageVault production.
	EnterAlterStorageVault(c *AlterStorageVaultContext)

	// EnterAlterWorkloadGroup is called when entering the alterWorkloadGroup production.
	EnterAlterWorkloadGroup(c *AlterWorkloadGroupContext)

	// EnterAlterCatalogProperties is called when entering the alterCatalogProperties production.
	EnterAlterCatalogProperties(c *AlterCatalogPropertiesContext)

	// EnterAlterWorkloadPolicy is called when entering the alterWorkloadPolicy production.
	EnterAlterWorkloadPolicy(c *AlterWorkloadPolicyContext)

	// EnterAlterSqlBlockRule is called when entering the alterSqlBlockRule production.
	EnterAlterSqlBlockRule(c *AlterSqlBlockRuleContext)

	// EnterAlterCatalogComment is called when entering the alterCatalogComment production.
	EnterAlterCatalogComment(c *AlterCatalogCommentContext)

	// EnterAlterDatabaseRename is called when entering the alterDatabaseRename production.
	EnterAlterDatabaseRename(c *AlterDatabaseRenameContext)

	// EnterAlterStoragePolicy is called when entering the alterStoragePolicy production.
	EnterAlterStoragePolicy(c *AlterStoragePolicyContext)

	// EnterAlterTable is called when entering the alterTable production.
	EnterAlterTable(c *AlterTableContext)

	// EnterAlterTableAddRollup is called when entering the alterTableAddRollup production.
	EnterAlterTableAddRollup(c *AlterTableAddRollupContext)

	// EnterAlterTableDropRollup is called when entering the alterTableDropRollup production.
	EnterAlterTableDropRollup(c *AlterTableDropRollupContext)

	// EnterAlterTableProperties is called when entering the alterTableProperties production.
	EnterAlterTableProperties(c *AlterTablePropertiesContext)

	// EnterAlterDatabaseSetQuota is called when entering the alterDatabaseSetQuota production.
	EnterAlterDatabaseSetQuota(c *AlterDatabaseSetQuotaContext)

	// EnterAlterDatabaseProperties is called when entering the alterDatabaseProperties production.
	EnterAlterDatabaseProperties(c *AlterDatabasePropertiesContext)

	// EnterAlterSystemRenameComputeGroup is called when entering the alterSystemRenameComputeGroup production.
	EnterAlterSystemRenameComputeGroup(c *AlterSystemRenameComputeGroupContext)

	// EnterAlterResource is called when entering the alterResource production.
	EnterAlterResource(c *AlterResourceContext)

	// EnterAlterRepository is called when entering the alterRepository production.
	EnterAlterRepository(c *AlterRepositoryContext)

	// EnterAlterRoutineLoad is called when entering the alterRoutineLoad production.
	EnterAlterRoutineLoad(c *AlterRoutineLoadContext)

	// EnterAlterColocateGroup is called when entering the alterColocateGroup production.
	EnterAlterColocateGroup(c *AlterColocateGroupContext)

	// EnterAlterUser is called when entering the alterUser production.
	EnterAlterUser(c *AlterUserContext)

	// EnterDropCatalogRecycleBin is called when entering the dropCatalogRecycleBin production.
	EnterDropCatalogRecycleBin(c *DropCatalogRecycleBinContext)

	// EnterDropEncryptkey is called when entering the dropEncryptkey production.
	EnterDropEncryptkey(c *DropEncryptkeyContext)

	// EnterDropRole is called when entering the dropRole production.
	EnterDropRole(c *DropRoleContext)

	// EnterDropSqlBlockRule is called when entering the dropSqlBlockRule production.
	EnterDropSqlBlockRule(c *DropSqlBlockRuleContext)

	// EnterDropUser is called when entering the dropUser production.
	EnterDropUser(c *DropUserContext)

	// EnterDropStoragePolicy is called when entering the dropStoragePolicy production.
	EnterDropStoragePolicy(c *DropStoragePolicyContext)

	// EnterDropWorkloadGroup is called when entering the dropWorkloadGroup production.
	EnterDropWorkloadGroup(c *DropWorkloadGroupContext)

	// EnterDropCatalog is called when entering the dropCatalog production.
	EnterDropCatalog(c *DropCatalogContext)

	// EnterDropFile is called when entering the dropFile production.
	EnterDropFile(c *DropFileContext)

	// EnterDropWorkloadPolicy is called when entering the dropWorkloadPolicy production.
	EnterDropWorkloadPolicy(c *DropWorkloadPolicyContext)

	// EnterDropRepository is called when entering the dropRepository production.
	EnterDropRepository(c *DropRepositoryContext)

	// EnterDropTable is called when entering the dropTable production.
	EnterDropTable(c *DropTableContext)

	// EnterDropDatabase is called when entering the dropDatabase production.
	EnterDropDatabase(c *DropDatabaseContext)

	// EnterDropFunction is called when entering the dropFunction production.
	EnterDropFunction(c *DropFunctionContext)

	// EnterDropIndex is called when entering the dropIndex production.
	EnterDropIndex(c *DropIndexContext)

	// EnterDropResource is called when entering the dropResource production.
	EnterDropResource(c *DropResourceContext)

	// EnterDropRowPolicy is called when entering the dropRowPolicy production.
	EnterDropRowPolicy(c *DropRowPolicyContext)

	// EnterDropDictionary is called when entering the dropDictionary production.
	EnterDropDictionary(c *DropDictionaryContext)

	// EnterDropStage is called when entering the dropStage production.
	EnterDropStage(c *DropStageContext)

	// EnterDropView is called when entering the dropView production.
	EnterDropView(c *DropViewContext)

	// EnterDropIndexAnalyzer is called when entering the dropIndexAnalyzer production.
	EnterDropIndexAnalyzer(c *DropIndexAnalyzerContext)

	// EnterDropIndexTokenizer is called when entering the dropIndexTokenizer production.
	EnterDropIndexTokenizer(c *DropIndexTokenizerContext)

	// EnterDropIndexTokenFilter is called when entering the dropIndexTokenFilter production.
	EnterDropIndexTokenFilter(c *DropIndexTokenFilterContext)

	// EnterShowVariables is called when entering the showVariables production.
	EnterShowVariables(c *ShowVariablesContext)

	// EnterShowAuthors is called when entering the showAuthors production.
	EnterShowAuthors(c *ShowAuthorsContext)

	// EnterShowAlterTable is called when entering the showAlterTable production.
	EnterShowAlterTable(c *ShowAlterTableContext)

	// EnterShowCreateDatabase is called when entering the showCreateDatabase production.
	EnterShowCreateDatabase(c *ShowCreateDatabaseContext)

	// EnterShowBackup is called when entering the showBackup production.
	EnterShowBackup(c *ShowBackupContext)

	// EnterShowBroker is called when entering the showBroker production.
	EnterShowBroker(c *ShowBrokerContext)

	// EnterShowBuildIndex is called when entering the showBuildIndex production.
	EnterShowBuildIndex(c *ShowBuildIndexContext)

	// EnterShowDynamicPartition is called when entering the showDynamicPartition production.
	EnterShowDynamicPartition(c *ShowDynamicPartitionContext)

	// EnterShowEvents is called when entering the showEvents production.
	EnterShowEvents(c *ShowEventsContext)

	// EnterShowExport is called when entering the showExport production.
	EnterShowExport(c *ShowExportContext)

	// EnterShowLastInsert is called when entering the showLastInsert production.
	EnterShowLastInsert(c *ShowLastInsertContext)

	// EnterShowCharset is called when entering the showCharset production.
	EnterShowCharset(c *ShowCharsetContext)

	// EnterShowDelete is called when entering the showDelete production.
	EnterShowDelete(c *ShowDeleteContext)

	// EnterShowCreateFunction is called when entering the showCreateFunction production.
	EnterShowCreateFunction(c *ShowCreateFunctionContext)

	// EnterShowFunctions is called when entering the showFunctions production.
	EnterShowFunctions(c *ShowFunctionsContext)

	// EnterShowGlobalFunctions is called when entering the showGlobalFunctions production.
	EnterShowGlobalFunctions(c *ShowGlobalFunctionsContext)

	// EnterShowGrants is called when entering the showGrants production.
	EnterShowGrants(c *ShowGrantsContext)

	// EnterShowGrantsForUser is called when entering the showGrantsForUser production.
	EnterShowGrantsForUser(c *ShowGrantsForUserContext)

	// EnterShowCreateUser is called when entering the showCreateUser production.
	EnterShowCreateUser(c *ShowCreateUserContext)

	// EnterShowSnapshot is called when entering the showSnapshot production.
	EnterShowSnapshot(c *ShowSnapshotContext)

	// EnterShowLoadProfile is called when entering the showLoadProfile production.
	EnterShowLoadProfile(c *ShowLoadProfileContext)

	// EnterShowCreateRepository is called when entering the showCreateRepository production.
	EnterShowCreateRepository(c *ShowCreateRepositoryContext)

	// EnterShowView is called when entering the showView production.
	EnterShowView(c *ShowViewContext)

	// EnterShowPlugins is called when entering the showPlugins production.
	EnterShowPlugins(c *ShowPluginsContext)

	// EnterShowStorageVault is called when entering the showStorageVault production.
	EnterShowStorageVault(c *ShowStorageVaultContext)

	// EnterShowRepositories is called when entering the showRepositories production.
	EnterShowRepositories(c *ShowRepositoriesContext)

	// EnterShowEncryptKeys is called when entering the showEncryptKeys production.
	EnterShowEncryptKeys(c *ShowEncryptKeysContext)

	// EnterShowCreateTable is called when entering the showCreateTable production.
	EnterShowCreateTable(c *ShowCreateTableContext)

	// EnterShowProcessList is called when entering the showProcessList production.
	EnterShowProcessList(c *ShowProcessListContext)

	// EnterShowPartitions is called when entering the showPartitions production.
	EnterShowPartitions(c *ShowPartitionsContext)

	// EnterShowRestore is called when entering the showRestore production.
	EnterShowRestore(c *ShowRestoreContext)

	// EnterShowRoles is called when entering the showRoles production.
	EnterShowRoles(c *ShowRolesContext)

	// EnterShowPartitionId is called when entering the showPartitionId production.
	EnterShowPartitionId(c *ShowPartitionIdContext)

	// EnterShowPrivileges is called when entering the showPrivileges production.
	EnterShowPrivileges(c *ShowPrivilegesContext)

	// EnterShowProc is called when entering the showProc production.
	EnterShowProc(c *ShowProcContext)

	// EnterShowSmallFiles is called when entering the showSmallFiles production.
	EnterShowSmallFiles(c *ShowSmallFilesContext)

	// EnterShowStorageEngines is called when entering the showStorageEngines production.
	EnterShowStorageEngines(c *ShowStorageEnginesContext)

	// EnterShowCreateCatalog is called when entering the showCreateCatalog production.
	EnterShowCreateCatalog(c *ShowCreateCatalogContext)

	// EnterShowCatalog is called when entering the showCatalog production.
	EnterShowCatalog(c *ShowCatalogContext)

	// EnterShowCatalogs is called when entering the showCatalogs production.
	EnterShowCatalogs(c *ShowCatalogsContext)

	// EnterShowUserProperties is called when entering the showUserProperties production.
	EnterShowUserProperties(c *ShowUserPropertiesContext)

	// EnterShowAllProperties is called when entering the showAllProperties production.
	EnterShowAllProperties(c *ShowAllPropertiesContext)

	// EnterShowCollation is called when entering the showCollation production.
	EnterShowCollation(c *ShowCollationContext)

	// EnterShowRowPolicy is called when entering the showRowPolicy production.
	EnterShowRowPolicy(c *ShowRowPolicyContext)

	// EnterShowStoragePolicy is called when entering the showStoragePolicy production.
	EnterShowStoragePolicy(c *ShowStoragePolicyContext)

	// EnterShowSqlBlockRule is called when entering the showSqlBlockRule production.
	EnterShowSqlBlockRule(c *ShowSqlBlockRuleContext)

	// EnterShowCreateView is called when entering the showCreateView production.
	EnterShowCreateView(c *ShowCreateViewContext)

	// EnterShowDataTypes is called when entering the showDataTypes production.
	EnterShowDataTypes(c *ShowDataTypesContext)

	// EnterShowData is called when entering the showData production.
	EnterShowData(c *ShowDataContext)

	// EnterShowCreateMaterializedView is called when entering the showCreateMaterializedView production.
	EnterShowCreateMaterializedView(c *ShowCreateMaterializedViewContext)

	// EnterShowWarningErrors is called when entering the showWarningErrors production.
	EnterShowWarningErrors(c *ShowWarningErrorsContext)

	// EnterShowWarningErrorCount is called when entering the showWarningErrorCount production.
	EnterShowWarningErrorCount(c *ShowWarningErrorCountContext)

	// EnterShowBackends is called when entering the showBackends production.
	EnterShowBackends(c *ShowBackendsContext)

	// EnterShowStages is called when entering the showStages production.
	EnterShowStages(c *ShowStagesContext)

	// EnterShowReplicaDistribution is called when entering the showReplicaDistribution production.
	EnterShowReplicaDistribution(c *ShowReplicaDistributionContext)

	// EnterShowResources is called when entering the showResources production.
	EnterShowResources(c *ShowResourcesContext)

	// EnterShowLoad is called when entering the showLoad production.
	EnterShowLoad(c *ShowLoadContext)

	// EnterShowLoadWarings is called when entering the showLoadWarings production.
	EnterShowLoadWarings(c *ShowLoadWaringsContext)

	// EnterShowTriggers is called when entering the showTriggers production.
	EnterShowTriggers(c *ShowTriggersContext)

	// EnterShowDiagnoseTablet is called when entering the showDiagnoseTablet production.
	EnterShowDiagnoseTablet(c *ShowDiagnoseTabletContext)

	// EnterShowOpenTables is called when entering the showOpenTables production.
	EnterShowOpenTables(c *ShowOpenTablesContext)

	// EnterShowFrontends is called when entering the showFrontends production.
	EnterShowFrontends(c *ShowFrontendsContext)

	// EnterShowDatabaseId is called when entering the showDatabaseId production.
	EnterShowDatabaseId(c *ShowDatabaseIdContext)

	// EnterShowColumns is called when entering the showColumns production.
	EnterShowColumns(c *ShowColumnsContext)

	// EnterShowTableId is called when entering the showTableId production.
	EnterShowTableId(c *ShowTableIdContext)

	// EnterShowTrash is called when entering the showTrash production.
	EnterShowTrash(c *ShowTrashContext)

	// EnterShowTypeCast is called when entering the showTypeCast production.
	EnterShowTypeCast(c *ShowTypeCastContext)

	// EnterShowClusters is called when entering the showClusters production.
	EnterShowClusters(c *ShowClustersContext)

	// EnterShowStatus is called when entering the showStatus production.
	EnterShowStatus(c *ShowStatusContext)

	// EnterShowWhitelist is called when entering the showWhitelist production.
	EnterShowWhitelist(c *ShowWhitelistContext)

	// EnterShowTabletsBelong is called when entering the showTabletsBelong production.
	EnterShowTabletsBelong(c *ShowTabletsBelongContext)

	// EnterShowDataSkew is called when entering the showDataSkew production.
	EnterShowDataSkew(c *ShowDataSkewContext)

	// EnterShowTableCreation is called when entering the showTableCreation production.
	EnterShowTableCreation(c *ShowTableCreationContext)

	// EnterShowTabletStorageFormat is called when entering the showTabletStorageFormat production.
	EnterShowTabletStorageFormat(c *ShowTabletStorageFormatContext)

	// EnterShowQueryProfile is called when entering the showQueryProfile production.
	EnterShowQueryProfile(c *ShowQueryProfileContext)

	// EnterShowConvertLsc is called when entering the showConvertLsc production.
	EnterShowConvertLsc(c *ShowConvertLscContext)

	// EnterShowTables is called when entering the showTables production.
	EnterShowTables(c *ShowTablesContext)

	// EnterShowViews is called when entering the showViews production.
	EnterShowViews(c *ShowViewsContext)

	// EnterShowTableStatus is called when entering the showTableStatus production.
	EnterShowTableStatus(c *ShowTableStatusContext)

	// EnterShowDatabases is called when entering the showDatabases production.
	EnterShowDatabases(c *ShowDatabasesContext)

	// EnterShowTabletsFromTable is called when entering the showTabletsFromTable production.
	EnterShowTabletsFromTable(c *ShowTabletsFromTableContext)

	// EnterShowCatalogRecycleBin is called when entering the showCatalogRecycleBin production.
	EnterShowCatalogRecycleBin(c *ShowCatalogRecycleBinContext)

	// EnterShowTabletId is called when entering the showTabletId production.
	EnterShowTabletId(c *ShowTabletIdContext)

	// EnterShowDictionaries is called when entering the showDictionaries production.
	EnterShowDictionaries(c *ShowDictionariesContext)

	// EnterShowTransaction is called when entering the showTransaction production.
	EnterShowTransaction(c *ShowTransactionContext)

	// EnterShowReplicaStatus is called when entering the showReplicaStatus production.
	EnterShowReplicaStatus(c *ShowReplicaStatusContext)

	// EnterShowWorkloadGroups is called when entering the showWorkloadGroups production.
	EnterShowWorkloadGroups(c *ShowWorkloadGroupsContext)

	// EnterShowCopy is called when entering the showCopy production.
	EnterShowCopy(c *ShowCopyContext)

	// EnterShowQueryStats is called when entering the showQueryStats production.
	EnterShowQueryStats(c *ShowQueryStatsContext)

	// EnterShowIndex is called when entering the showIndex production.
	EnterShowIndex(c *ShowIndexContext)

	// EnterShowWarmUpJob is called when entering the showWarmUpJob production.
	EnterShowWarmUpJob(c *ShowWarmUpJobContext)

	// EnterSync is called when entering the sync production.
	EnterSync(c *SyncContext)

	// EnterCreateRoutineLoadAlias is called when entering the createRoutineLoadAlias production.
	EnterCreateRoutineLoadAlias(c *CreateRoutineLoadAliasContext)

	// EnterShowCreateRoutineLoad is called when entering the showCreateRoutineLoad production.
	EnterShowCreateRoutineLoad(c *ShowCreateRoutineLoadContext)

	// EnterPauseRoutineLoad is called when entering the pauseRoutineLoad production.
	EnterPauseRoutineLoad(c *PauseRoutineLoadContext)

	// EnterPauseAllRoutineLoad is called when entering the pauseAllRoutineLoad production.
	EnterPauseAllRoutineLoad(c *PauseAllRoutineLoadContext)

	// EnterResumeRoutineLoad is called when entering the resumeRoutineLoad production.
	EnterResumeRoutineLoad(c *ResumeRoutineLoadContext)

	// EnterResumeAllRoutineLoad is called when entering the resumeAllRoutineLoad production.
	EnterResumeAllRoutineLoad(c *ResumeAllRoutineLoadContext)

	// EnterStopRoutineLoad is called when entering the stopRoutineLoad production.
	EnterStopRoutineLoad(c *StopRoutineLoadContext)

	// EnterShowRoutineLoad is called when entering the showRoutineLoad production.
	EnterShowRoutineLoad(c *ShowRoutineLoadContext)

	// EnterShowRoutineLoadTask is called when entering the showRoutineLoadTask production.
	EnterShowRoutineLoadTask(c *ShowRoutineLoadTaskContext)

	// EnterShowIndexAnalyzer is called when entering the showIndexAnalyzer production.
	EnterShowIndexAnalyzer(c *ShowIndexAnalyzerContext)

	// EnterShowIndexTokenizer is called when entering the showIndexTokenizer production.
	EnterShowIndexTokenizer(c *ShowIndexTokenizerContext)

	// EnterShowIndexTokenFilter is called when entering the showIndexTokenFilter production.
	EnterShowIndexTokenFilter(c *ShowIndexTokenFilterContext)

	// EnterKillConnection is called when entering the killConnection production.
	EnterKillConnection(c *KillConnectionContext)

	// EnterKillQuery is called when entering the killQuery production.
	EnterKillQuery(c *KillQueryContext)

	// EnterHelp is called when entering the help production.
	EnterHelp(c *HelpContext)

	// EnterUnlockTables is called when entering the unlockTables production.
	EnterUnlockTables(c *UnlockTablesContext)

	// EnterInstallPlugin is called when entering the installPlugin production.
	EnterInstallPlugin(c *InstallPluginContext)

	// EnterUninstallPlugin is called when entering the uninstallPlugin production.
	EnterUninstallPlugin(c *UninstallPluginContext)

	// EnterLockTables is called when entering the lockTables production.
	EnterLockTables(c *LockTablesContext)

	// EnterRestore is called when entering the restore production.
	EnterRestore(c *RestoreContext)

	// EnterWarmUpCluster is called when entering the warmUpCluster production.
	EnterWarmUpCluster(c *WarmUpClusterContext)

	// EnterBackup is called when entering the backup production.
	EnterBackup(c *BackupContext)

	// EnterUnsupportedStartTransaction is called when entering the unsupportedStartTransaction production.
	EnterUnsupportedStartTransaction(c *UnsupportedStartTransactionContext)

	// EnterWarmUpItem is called when entering the warmUpItem production.
	EnterWarmUpItem(c *WarmUpItemContext)

	// EnterLockTable is called when entering the lockTable production.
	EnterLockTable(c *LockTableContext)

	// EnterCreateRoutineLoad is called when entering the createRoutineLoad production.
	EnterCreateRoutineLoad(c *CreateRoutineLoadContext)

	// EnterMysqlLoad is called when entering the mysqlLoad production.
	EnterMysqlLoad(c *MysqlLoadContext)

	// EnterShowCreateLoad is called when entering the showCreateLoad production.
	EnterShowCreateLoad(c *ShowCreateLoadContext)

	// EnterSeparator is called when entering the separator production.
	EnterSeparator(c *SeparatorContext)

	// EnterImportColumns is called when entering the importColumns production.
	EnterImportColumns(c *ImportColumnsContext)

	// EnterImportPrecedingFilter is called when entering the importPrecedingFilter production.
	EnterImportPrecedingFilter(c *ImportPrecedingFilterContext)

	// EnterImportWhere is called when entering the importWhere production.
	EnterImportWhere(c *ImportWhereContext)

	// EnterImportDeleteOn is called when entering the importDeleteOn production.
	EnterImportDeleteOn(c *ImportDeleteOnContext)

	// EnterImportSequence is called when entering the importSequence production.
	EnterImportSequence(c *ImportSequenceContext)

	// EnterImportPartitions is called when entering the importPartitions production.
	EnterImportPartitions(c *ImportPartitionsContext)

	// EnterImportSequenceStatement is called when entering the importSequenceStatement production.
	EnterImportSequenceStatement(c *ImportSequenceStatementContext)

	// EnterImportDeleteOnStatement is called when entering the importDeleteOnStatement production.
	EnterImportDeleteOnStatement(c *ImportDeleteOnStatementContext)

	// EnterImportWhereStatement is called when entering the importWhereStatement production.
	EnterImportWhereStatement(c *ImportWhereStatementContext)

	// EnterImportPrecedingFilterStatement is called when entering the importPrecedingFilterStatement production.
	EnterImportPrecedingFilterStatement(c *ImportPrecedingFilterStatementContext)

	// EnterImportColumnsStatement is called when entering the importColumnsStatement production.
	EnterImportColumnsStatement(c *ImportColumnsStatementContext)

	// EnterImportColumnDesc is called when entering the importColumnDesc production.
	EnterImportColumnDesc(c *ImportColumnDescContext)

	// EnterRefreshCatalog is called when entering the refreshCatalog production.
	EnterRefreshCatalog(c *RefreshCatalogContext)

	// EnterRefreshDatabase is called when entering the refreshDatabase production.
	EnterRefreshDatabase(c *RefreshDatabaseContext)

	// EnterRefreshTable is called when entering the refreshTable production.
	EnterRefreshTable(c *RefreshTableContext)

	// EnterRefreshDictionary is called when entering the refreshDictionary production.
	EnterRefreshDictionary(c *RefreshDictionaryContext)

	// EnterRefreshLdap is called when entering the refreshLdap production.
	EnterRefreshLdap(c *RefreshLdapContext)

	// EnterCleanAllProfile is called when entering the cleanAllProfile production.
	EnterCleanAllProfile(c *CleanAllProfileContext)

	// EnterCleanLabel is called when entering the cleanLabel production.
	EnterCleanLabel(c *CleanLabelContext)

	// EnterCleanQueryStats is called when entering the cleanQueryStats production.
	EnterCleanQueryStats(c *CleanQueryStatsContext)

	// EnterCleanAllQueryStats is called when entering the cleanAllQueryStats production.
	EnterCleanAllQueryStats(c *CleanAllQueryStatsContext)

	// EnterCancelLoad is called when entering the cancelLoad production.
	EnterCancelLoad(c *CancelLoadContext)

	// EnterCancelExport is called when entering the cancelExport production.
	EnterCancelExport(c *CancelExportContext)

	// EnterCancelWarmUpJob is called when entering the cancelWarmUpJob production.
	EnterCancelWarmUpJob(c *CancelWarmUpJobContext)

	// EnterCancelDecommisionBackend is called when entering the cancelDecommisionBackend production.
	EnterCancelDecommisionBackend(c *CancelDecommisionBackendContext)

	// EnterCancelBackup is called when entering the cancelBackup production.
	EnterCancelBackup(c *CancelBackupContext)

	// EnterCancelRestore is called when entering the cancelRestore production.
	EnterCancelRestore(c *CancelRestoreContext)

	// EnterCancelBuildIndex is called when entering the cancelBuildIndex production.
	EnterCancelBuildIndex(c *CancelBuildIndexContext)

	// EnterCancelAlterTable is called when entering the cancelAlterTable production.
	EnterCancelAlterTable(c *CancelAlterTableContext)

	// EnterAdminShowReplicaDistribution is called when entering the adminShowReplicaDistribution production.
	EnterAdminShowReplicaDistribution(c *AdminShowReplicaDistributionContext)

	// EnterAdminRebalanceDisk is called when entering the adminRebalanceDisk production.
	EnterAdminRebalanceDisk(c *AdminRebalanceDiskContext)

	// EnterAdminCancelRebalanceDisk is called when entering the adminCancelRebalanceDisk production.
	EnterAdminCancelRebalanceDisk(c *AdminCancelRebalanceDiskContext)

	// EnterAdminDiagnoseTablet is called when entering the adminDiagnoseTablet production.
	EnterAdminDiagnoseTablet(c *AdminDiagnoseTabletContext)

	// EnterAdminShowReplicaStatus is called when entering the adminShowReplicaStatus production.
	EnterAdminShowReplicaStatus(c *AdminShowReplicaStatusContext)

	// EnterAdminCompactTable is called when entering the adminCompactTable production.
	EnterAdminCompactTable(c *AdminCompactTableContext)

	// EnterAdminCheckTablets is called when entering the adminCheckTablets production.
	EnterAdminCheckTablets(c *AdminCheckTabletsContext)

	// EnterAdminShowTabletStorageFormat is called when entering the adminShowTabletStorageFormat production.
	EnterAdminShowTabletStorageFormat(c *AdminShowTabletStorageFormatContext)

	// EnterAdminSetFrontendConfig is called when entering the adminSetFrontendConfig production.
	EnterAdminSetFrontendConfig(c *AdminSetFrontendConfigContext)

	// EnterAdminCleanTrash is called when entering the adminCleanTrash production.
	EnterAdminCleanTrash(c *AdminCleanTrashContext)

	// EnterAdminSetReplicaVersion is called when entering the adminSetReplicaVersion production.
	EnterAdminSetReplicaVersion(c *AdminSetReplicaVersionContext)

	// EnterAdminSetTableStatus is called when entering the adminSetTableStatus production.
	EnterAdminSetTableStatus(c *AdminSetTableStatusContext)

	// EnterAdminSetReplicaStatus is called when entering the adminSetReplicaStatus production.
	EnterAdminSetReplicaStatus(c *AdminSetReplicaStatusContext)

	// EnterAdminRepairTable is called when entering the adminRepairTable production.
	EnterAdminRepairTable(c *AdminRepairTableContext)

	// EnterAdminCancelRepairTable is called when entering the adminCancelRepairTable production.
	EnterAdminCancelRepairTable(c *AdminCancelRepairTableContext)

	// EnterAdminCopyTablet is called when entering the adminCopyTablet production.
	EnterAdminCopyTablet(c *AdminCopyTabletContext)

	// EnterRecoverDatabase is called when entering the recoverDatabase production.
	EnterRecoverDatabase(c *RecoverDatabaseContext)

	// EnterRecoverTable is called when entering the recoverTable production.
	EnterRecoverTable(c *RecoverTableContext)

	// EnterRecoverPartition is called when entering the recoverPartition production.
	EnterRecoverPartition(c *RecoverPartitionContext)

	// EnterAdminSetPartitionVersion is called when entering the adminSetPartitionVersion production.
	EnterAdminSetPartitionVersion(c *AdminSetPartitionVersionContext)

	// EnterBaseTableRef is called when entering the baseTableRef production.
	EnterBaseTableRef(c *BaseTableRefContext)

	// EnterWildWhere is called when entering the wildWhere production.
	EnterWildWhere(c *WildWhereContext)

	// EnterTransactionBegin is called when entering the transactionBegin production.
	EnterTransactionBegin(c *TransactionBeginContext)

	// EnterTranscationCommit is called when entering the transcationCommit production.
	EnterTranscationCommit(c *TranscationCommitContext)

	// EnterTransactionRollback is called when entering the transactionRollback production.
	EnterTransactionRollback(c *TransactionRollbackContext)

	// EnterGrantTablePrivilege is called when entering the grantTablePrivilege production.
	EnterGrantTablePrivilege(c *GrantTablePrivilegeContext)

	// EnterGrantResourcePrivilege is called when entering the grantResourcePrivilege production.
	EnterGrantResourcePrivilege(c *GrantResourcePrivilegeContext)

	// EnterGrantRole is called when entering the grantRole production.
	EnterGrantRole(c *GrantRoleContext)

	// EnterRevokeRole is called when entering the revokeRole production.
	EnterRevokeRole(c *RevokeRoleContext)

	// EnterRevokeResourcePrivilege is called when entering the revokeResourcePrivilege production.
	EnterRevokeResourcePrivilege(c *RevokeResourcePrivilegeContext)

	// EnterRevokeTablePrivilege is called when entering the revokeTablePrivilege production.
	EnterRevokeTablePrivilege(c *RevokeTablePrivilegeContext)

	// EnterPrivilege is called when entering the privilege production.
	EnterPrivilege(c *PrivilegeContext)

	// EnterPrivilegeList is called when entering the privilegeList production.
	EnterPrivilegeList(c *PrivilegeListContext)

	// EnterAddBackendClause is called when entering the addBackendClause production.
	EnterAddBackendClause(c *AddBackendClauseContext)

	// EnterDropBackendClause is called when entering the dropBackendClause production.
	EnterDropBackendClause(c *DropBackendClauseContext)

	// EnterDecommissionBackendClause is called when entering the decommissionBackendClause production.
	EnterDecommissionBackendClause(c *DecommissionBackendClauseContext)

	// EnterAddObserverClause is called when entering the addObserverClause production.
	EnterAddObserverClause(c *AddObserverClauseContext)

	// EnterDropObserverClause is called when entering the dropObserverClause production.
	EnterDropObserverClause(c *DropObserverClauseContext)

	// EnterAddFollowerClause is called when entering the addFollowerClause production.
	EnterAddFollowerClause(c *AddFollowerClauseContext)

	// EnterDropFollowerClause is called when entering the dropFollowerClause production.
	EnterDropFollowerClause(c *DropFollowerClauseContext)

	// EnterAddBrokerClause is called when entering the addBrokerClause production.
	EnterAddBrokerClause(c *AddBrokerClauseContext)

	// EnterDropBrokerClause is called when entering the dropBrokerClause production.
	EnterDropBrokerClause(c *DropBrokerClauseContext)

	// EnterDropAllBrokerClause is called when entering the dropAllBrokerClause production.
	EnterDropAllBrokerClause(c *DropAllBrokerClauseContext)

	// EnterAlterLoadErrorUrlClause is called when entering the alterLoadErrorUrlClause production.
	EnterAlterLoadErrorUrlClause(c *AlterLoadErrorUrlClauseContext)

	// EnterModifyBackendClause is called when entering the modifyBackendClause production.
	EnterModifyBackendClause(c *ModifyBackendClauseContext)

	// EnterModifyFrontendOrBackendHostNameClause is called when entering the modifyFrontendOrBackendHostNameClause production.
	EnterModifyFrontendOrBackendHostNameClause(c *ModifyFrontendOrBackendHostNameClauseContext)

	// EnterDropRollupClause is called when entering the dropRollupClause production.
	EnterDropRollupClause(c *DropRollupClauseContext)

	// EnterAddRollupClause is called when entering the addRollupClause production.
	EnterAddRollupClause(c *AddRollupClauseContext)

	// EnterAddColumnClause is called when entering the addColumnClause production.
	EnterAddColumnClause(c *AddColumnClauseContext)

	// EnterAddColumnsClause is called when entering the addColumnsClause production.
	EnterAddColumnsClause(c *AddColumnsClauseContext)

	// EnterDropColumnClause is called when entering the dropColumnClause production.
	EnterDropColumnClause(c *DropColumnClauseContext)

	// EnterModifyColumnClause is called when entering the modifyColumnClause production.
	EnterModifyColumnClause(c *ModifyColumnClauseContext)

	// EnterReorderColumnsClause is called when entering the reorderColumnsClause production.
	EnterReorderColumnsClause(c *ReorderColumnsClauseContext)

	// EnterAddPartitionClause is called when entering the addPartitionClause production.
	EnterAddPartitionClause(c *AddPartitionClauseContext)

	// EnterDropPartitionClause is called when entering the dropPartitionClause production.
	EnterDropPartitionClause(c *DropPartitionClauseContext)

	// EnterModifyPartitionClause is called when entering the modifyPartitionClause production.
	EnterModifyPartitionClause(c *ModifyPartitionClauseContext)

	// EnterReplacePartitionClause is called when entering the replacePartitionClause production.
	EnterReplacePartitionClause(c *ReplacePartitionClauseContext)

	// EnterReplaceTableClause is called when entering the replaceTableClause production.
	EnterReplaceTableClause(c *ReplaceTableClauseContext)

	// EnterRenameClause is called when entering the renameClause production.
	EnterRenameClause(c *RenameClauseContext)

	// EnterRenameRollupClause is called when entering the renameRollupClause production.
	EnterRenameRollupClause(c *RenameRollupClauseContext)

	// EnterRenamePartitionClause is called when entering the renamePartitionClause production.
	EnterRenamePartitionClause(c *RenamePartitionClauseContext)

	// EnterRenameColumnClause is called when entering the renameColumnClause production.
	EnterRenameColumnClause(c *RenameColumnClauseContext)

	// EnterAddIndexClause is called when entering the addIndexClause production.
	EnterAddIndexClause(c *AddIndexClauseContext)

	// EnterDropIndexClause is called when entering the dropIndexClause production.
	EnterDropIndexClause(c *DropIndexClauseContext)

	// EnterEnableFeatureClause is called when entering the enableFeatureClause production.
	EnterEnableFeatureClause(c *EnableFeatureClauseContext)

	// EnterModifyDistributionClause is called when entering the modifyDistributionClause production.
	EnterModifyDistributionClause(c *ModifyDistributionClauseContext)

	// EnterModifyTableCommentClause is called when entering the modifyTableCommentClause production.
	EnterModifyTableCommentClause(c *ModifyTableCommentClauseContext)

	// EnterModifyColumnCommentClause is called when entering the modifyColumnCommentClause production.
	EnterModifyColumnCommentClause(c *ModifyColumnCommentClauseContext)

	// EnterModifyEngineClause is called when entering the modifyEngineClause production.
	EnterModifyEngineClause(c *ModifyEngineClauseContext)

	// EnterAlterMultiPartitionClause is called when entering the alterMultiPartitionClause production.
	EnterAlterMultiPartitionClause(c *AlterMultiPartitionClauseContext)

	// EnterCreateOrReplaceTagClauses is called when entering the createOrReplaceTagClauses production.
	EnterCreateOrReplaceTagClauses(c *CreateOrReplaceTagClausesContext)

	// EnterCreateOrReplaceBranchClauses is called when entering the createOrReplaceBranchClauses production.
	EnterCreateOrReplaceBranchClauses(c *CreateOrReplaceBranchClausesContext)

	// EnterDropBranchClauses is called when entering the dropBranchClauses production.
	EnterDropBranchClauses(c *DropBranchClausesContext)

	// EnterDropTagClauses is called when entering the dropTagClauses production.
	EnterDropTagClauses(c *DropTagClausesContext)

	// EnterCreateOrReplaceTagClause is called when entering the createOrReplaceTagClause production.
	EnterCreateOrReplaceTagClause(c *CreateOrReplaceTagClauseContext)

	// EnterCreateOrReplaceBranchClause is called when entering the createOrReplaceBranchClause production.
	EnterCreateOrReplaceBranchClause(c *CreateOrReplaceBranchClauseContext)

	// EnterTagOptions is called when entering the tagOptions production.
	EnterTagOptions(c *TagOptionsContext)

	// EnterBranchOptions is called when entering the branchOptions production.
	EnterBranchOptions(c *BranchOptionsContext)

	// EnterRetainTime is called when entering the retainTime production.
	EnterRetainTime(c *RetainTimeContext)

	// EnterRetentionSnapshot is called when entering the retentionSnapshot production.
	EnterRetentionSnapshot(c *RetentionSnapshotContext)

	// EnterMinSnapshotsToKeep is called when entering the minSnapshotsToKeep production.
	EnterMinSnapshotsToKeep(c *MinSnapshotsToKeepContext)

	// EnterTimeValueWithUnit is called when entering the timeValueWithUnit production.
	EnterTimeValueWithUnit(c *TimeValueWithUnitContext)

	// EnterDropBranchClause is called when entering the dropBranchClause production.
	EnterDropBranchClause(c *DropBranchClauseContext)

	// EnterDropTagClause is called when entering the dropTagClause production.
	EnterDropTagClause(c *DropTagClauseContext)

	// EnterColumnPosition is called when entering the columnPosition production.
	EnterColumnPosition(c *ColumnPositionContext)

	// EnterToRollup is called when entering the toRollup production.
	EnterToRollup(c *ToRollupContext)

	// EnterFromRollup is called when entering the fromRollup production.
	EnterFromRollup(c *FromRollupContext)

	// EnterShowAnalyze is called when entering the showAnalyze production.
	EnterShowAnalyze(c *ShowAnalyzeContext)

	// EnterShowQueuedAnalyzeJobs is called when entering the showQueuedAnalyzeJobs production.
	EnterShowQueuedAnalyzeJobs(c *ShowQueuedAnalyzeJobsContext)

	// EnterShowColumnHistogramStats is called when entering the showColumnHistogramStats production.
	EnterShowColumnHistogramStats(c *ShowColumnHistogramStatsContext)

	// EnterAnalyzeDatabase is called when entering the analyzeDatabase production.
	EnterAnalyzeDatabase(c *AnalyzeDatabaseContext)

	// EnterAnalyzeTable is called when entering the analyzeTable production.
	EnterAnalyzeTable(c *AnalyzeTableContext)

	// EnterAlterTableStats is called when entering the alterTableStats production.
	EnterAlterTableStats(c *AlterTableStatsContext)

	// EnterAlterColumnStats is called when entering the alterColumnStats production.
	EnterAlterColumnStats(c *AlterColumnStatsContext)

	// EnterShowIndexStats is called when entering the showIndexStats production.
	EnterShowIndexStats(c *ShowIndexStatsContext)

	// EnterDropStats is called when entering the dropStats production.
	EnterDropStats(c *DropStatsContext)

	// EnterDropCachedStats is called when entering the dropCachedStats production.
	EnterDropCachedStats(c *DropCachedStatsContext)

	// EnterDropExpiredStats is called when entering the dropExpiredStats production.
	EnterDropExpiredStats(c *DropExpiredStatsContext)

	// EnterKillAnalyzeJob is called when entering the killAnalyzeJob production.
	EnterKillAnalyzeJob(c *KillAnalyzeJobContext)

	// EnterDropAnalyzeJob is called when entering the dropAnalyzeJob production.
	EnterDropAnalyzeJob(c *DropAnalyzeJobContext)

	// EnterShowTableStats is called when entering the showTableStats production.
	EnterShowTableStats(c *ShowTableStatsContext)

	// EnterShowColumnStats is called when entering the showColumnStats production.
	EnterShowColumnStats(c *ShowColumnStatsContext)

	// EnterShowAnalyzeTask is called when entering the showAnalyzeTask production.
	EnterShowAnalyzeTask(c *ShowAnalyzeTaskContext)

	// EnterAnalyzeProperties is called when entering the analyzeProperties production.
	EnterAnalyzeProperties(c *AnalyzePropertiesContext)

	// EnterWorkloadPolicyActions is called when entering the workloadPolicyActions production.
	EnterWorkloadPolicyActions(c *WorkloadPolicyActionsContext)

	// EnterWorkloadPolicyAction is called when entering the workloadPolicyAction production.
	EnterWorkloadPolicyAction(c *WorkloadPolicyActionContext)

	// EnterWorkloadPolicyConditions is called when entering the workloadPolicyConditions production.
	EnterWorkloadPolicyConditions(c *WorkloadPolicyConditionsContext)

	// EnterWorkloadPolicyCondition is called when entering the workloadPolicyCondition production.
	EnterWorkloadPolicyCondition(c *WorkloadPolicyConditionContext)

	// EnterStorageBackend is called when entering the storageBackend production.
	EnterStorageBackend(c *StorageBackendContext)

	// EnterPasswordOption is called when entering the passwordOption production.
	EnterPasswordOption(c *PasswordOptionContext)

	// EnterFunctionArguments is called when entering the functionArguments production.
	EnterFunctionArguments(c *FunctionArgumentsContext)

	// EnterDataTypeList is called when entering the dataTypeList production.
	EnterDataTypeList(c *DataTypeListContext)

	// EnterSetOptions is called when entering the setOptions production.
	EnterSetOptions(c *SetOptionsContext)

	// EnterSetDefaultStorageVault is called when entering the setDefaultStorageVault production.
	EnterSetDefaultStorageVault(c *SetDefaultStorageVaultContext)

	// EnterSetUserProperties is called when entering the setUserProperties production.
	EnterSetUserProperties(c *SetUserPropertiesContext)

	// EnterSetTransaction is called when entering the setTransaction production.
	EnterSetTransaction(c *SetTransactionContext)

	// EnterSetVariableWithType is called when entering the setVariableWithType production.
	EnterSetVariableWithType(c *SetVariableWithTypeContext)

	// EnterSetNames is called when entering the setNames production.
	EnterSetNames(c *SetNamesContext)

	// EnterSetCharset is called when entering the setCharset production.
	EnterSetCharset(c *SetCharsetContext)

	// EnterSetCollate is called when entering the setCollate production.
	EnterSetCollate(c *SetCollateContext)

	// EnterSetPassword is called when entering the setPassword production.
	EnterSetPassword(c *SetPasswordContext)

	// EnterSetLdapAdminPassword is called when entering the setLdapAdminPassword production.
	EnterSetLdapAdminPassword(c *SetLdapAdminPasswordContext)

	// EnterSetVariableWithoutType is called when entering the setVariableWithoutType production.
	EnterSetVariableWithoutType(c *SetVariableWithoutTypeContext)

	// EnterSetSystemVariable is called when entering the setSystemVariable production.
	EnterSetSystemVariable(c *SetSystemVariableContext)

	// EnterSetUserVariable is called when entering the setUserVariable production.
	EnterSetUserVariable(c *SetUserVariableContext)

	// EnterTransactionAccessMode is called when entering the transactionAccessMode production.
	EnterTransactionAccessMode(c *TransactionAccessModeContext)

	// EnterIsolationLevel is called when entering the isolationLevel production.
	EnterIsolationLevel(c *IsolationLevelContext)

	// EnterSupportedUnsetStatement is called when entering the supportedUnsetStatement production.
	EnterSupportedUnsetStatement(c *SupportedUnsetStatementContext)

	// EnterSwitchCatalog is called when entering the switchCatalog production.
	EnterSwitchCatalog(c *SwitchCatalogContext)

	// EnterUseDatabase is called when entering the useDatabase production.
	EnterUseDatabase(c *UseDatabaseContext)

	// EnterUseCloudCluster is called when entering the useCloudCluster production.
	EnterUseCloudCluster(c *UseCloudClusterContext)

	// EnterStageAndPattern is called when entering the stageAndPattern production.
	EnterStageAndPattern(c *StageAndPatternContext)

	// EnterDescribeTableValuedFunction is called when entering the describeTableValuedFunction production.
	EnterDescribeTableValuedFunction(c *DescribeTableValuedFunctionContext)

	// EnterDescribeTableAll is called when entering the describeTableAll production.
	EnterDescribeTableAll(c *DescribeTableAllContext)

	// EnterDescribeTable is called when entering the describeTable production.
	EnterDescribeTable(c *DescribeTableContext)

	// EnterDescribeDictionary is called when entering the describeDictionary production.
	EnterDescribeDictionary(c *DescribeDictionaryContext)

	// EnterConstraint is called when entering the constraint production.
	EnterConstraint(c *ConstraintContext)

	// EnterPartitionSpec is called when entering the partitionSpec production.
	EnterPartitionSpec(c *PartitionSpecContext)

	// EnterPartitionTable is called when entering the partitionTable production.
	EnterPartitionTable(c *PartitionTableContext)

	// EnterIdentityOrFunctionList is called when entering the identityOrFunctionList production.
	EnterIdentityOrFunctionList(c *IdentityOrFunctionListContext)

	// EnterIdentityOrFunction is called when entering the identityOrFunction production.
	EnterIdentityOrFunction(c *IdentityOrFunctionContext)

	// EnterDataDesc is called when entering the dataDesc production.
	EnterDataDesc(c *DataDescContext)

	// EnterStatementScope is called when entering the statementScope production.
	EnterStatementScope(c *StatementScopeContext)

	// EnterBuildMode is called when entering the buildMode production.
	EnterBuildMode(c *BuildModeContext)

	// EnterRefreshTrigger is called when entering the refreshTrigger production.
	EnterRefreshTrigger(c *RefreshTriggerContext)

	// EnterRefreshSchedule is called when entering the refreshSchedule production.
	EnterRefreshSchedule(c *RefreshScheduleContext)

	// EnterRefreshMethod is called when entering the refreshMethod production.
	EnterRefreshMethod(c *RefreshMethodContext)

	// EnterMvPartition is called when entering the mvPartition production.
	EnterMvPartition(c *MvPartitionContext)

	// EnterIdentifierOrText is called when entering the identifierOrText production.
	EnterIdentifierOrText(c *IdentifierOrTextContext)

	// EnterIdentifierOrTextOrAsterisk is called when entering the identifierOrTextOrAsterisk production.
	EnterIdentifierOrTextOrAsterisk(c *IdentifierOrTextOrAsteriskContext)

	// EnterMultipartIdentifierOrAsterisk is called when entering the multipartIdentifierOrAsterisk production.
	EnterMultipartIdentifierOrAsterisk(c *MultipartIdentifierOrAsteriskContext)

	// EnterIdentifierOrAsterisk is called when entering the identifierOrAsterisk production.
	EnterIdentifierOrAsterisk(c *IdentifierOrAsteriskContext)

	// EnterUserIdentify is called when entering the userIdentify production.
	EnterUserIdentify(c *UserIdentifyContext)

	// EnterGrantUserIdentify is called when entering the grantUserIdentify production.
	EnterGrantUserIdentify(c *GrantUserIdentifyContext)

	// EnterExplain is called when entering the explain production.
	EnterExplain(c *ExplainContext)

	// EnterExplainCommand is called when entering the explainCommand production.
	EnterExplainCommand(c *ExplainCommandContext)

	// EnterPlanType is called when entering the planType production.
	EnterPlanType(c *PlanTypeContext)

	// EnterReplayCommand is called when entering the replayCommand production.
	EnterReplayCommand(c *ReplayCommandContext)

	// EnterReplayType is called when entering the replayType production.
	EnterReplayType(c *ReplayTypeContext)

	// EnterMergeType is called when entering the mergeType production.
	EnterMergeType(c *MergeTypeContext)

	// EnterPreFilterClause is called when entering the preFilterClause production.
	EnterPreFilterClause(c *PreFilterClauseContext)

	// EnterDeleteOnClause is called when entering the deleteOnClause production.
	EnterDeleteOnClause(c *DeleteOnClauseContext)

	// EnterSequenceColClause is called when entering the sequenceColClause production.
	EnterSequenceColClause(c *SequenceColClauseContext)

	// EnterColFromPath is called when entering the colFromPath production.
	EnterColFromPath(c *ColFromPathContext)

	// EnterColMappingList is called when entering the colMappingList production.
	EnterColMappingList(c *ColMappingListContext)

	// EnterMappingExpr is called when entering the mappingExpr production.
	EnterMappingExpr(c *MappingExprContext)

	// EnterWithRemoteStorageSystem is called when entering the withRemoteStorageSystem production.
	EnterWithRemoteStorageSystem(c *WithRemoteStorageSystemContext)

	// EnterResourceDesc is called when entering the resourceDesc production.
	EnterResourceDesc(c *ResourceDescContext)

	// EnterMysqlDataDesc is called when entering the mysqlDataDesc production.
	EnterMysqlDataDesc(c *MysqlDataDescContext)

	// EnterSkipLines is called when entering the skipLines production.
	EnterSkipLines(c *SkipLinesContext)

	// EnterOutFileClause is called when entering the outFileClause production.
	EnterOutFileClause(c *OutFileClauseContext)

	// EnterQuery is called when entering the query production.
	EnterQuery(c *QueryContext)

	// EnterQueryTermDefault is called when entering the queryTermDefault production.
	EnterQueryTermDefault(c *QueryTermDefaultContext)

	// EnterSetOperation is called when entering the setOperation production.
	EnterSetOperation(c *SetOperationContext)

	// EnterSetQuantifier is called when entering the setQuantifier production.
	EnterSetQuantifier(c *SetQuantifierContext)

	// EnterQueryPrimaryDefault is called when entering the queryPrimaryDefault production.
	EnterQueryPrimaryDefault(c *QueryPrimaryDefaultContext)

	// EnterSubquery is called when entering the subquery production.
	EnterSubquery(c *SubqueryContext)

	// EnterValuesTable is called when entering the valuesTable production.
	EnterValuesTable(c *ValuesTableContext)

	// EnterRegularQuerySpecification is called when entering the regularQuerySpecification production.
	EnterRegularQuerySpecification(c *RegularQuerySpecificationContext)

	// EnterCte is called when entering the cte production.
	EnterCte(c *CteContext)

	// EnterAliasQuery is called when entering the aliasQuery production.
	EnterAliasQuery(c *AliasQueryContext)

	// EnterColumnAliases is called when entering the columnAliases production.
	EnterColumnAliases(c *ColumnAliasesContext)

	// EnterSelectClause is called when entering the selectClause production.
	EnterSelectClause(c *SelectClauseContext)

	// EnterSelectColumnClause is called when entering the selectColumnClause production.
	EnterSelectColumnClause(c *SelectColumnClauseContext)

	// EnterWhereClause is called when entering the whereClause production.
	EnterWhereClause(c *WhereClauseContext)

	// EnterFromClause is called when entering the fromClause production.
	EnterFromClause(c *FromClauseContext)

	// EnterIntoClause is called when entering the intoClause production.
	EnterIntoClause(c *IntoClauseContext)

	// EnterBulkCollectClause is called when entering the bulkCollectClause production.
	EnterBulkCollectClause(c *BulkCollectClauseContext)

	// EnterTableRow is called when entering the tableRow production.
	EnterTableRow(c *TableRowContext)

	// EnterRelations is called when entering the relations production.
	EnterRelations(c *RelationsContext)

	// EnterRelation is called when entering the relation production.
	EnterRelation(c *RelationContext)

	// EnterJoinRelation is called when entering the joinRelation production.
	EnterJoinRelation(c *JoinRelationContext)

	// EnterBracketDistributeType is called when entering the bracketDistributeType production.
	EnterBracketDistributeType(c *BracketDistributeTypeContext)

	// EnterCommentDistributeType is called when entering the commentDistributeType production.
	EnterCommentDistributeType(c *CommentDistributeTypeContext)

	// EnterBracketRelationHint is called when entering the bracketRelationHint production.
	EnterBracketRelationHint(c *BracketRelationHintContext)

	// EnterCommentRelationHint is called when entering the commentRelationHint production.
	EnterCommentRelationHint(c *CommentRelationHintContext)

	// EnterAggClause is called when entering the aggClause production.
	EnterAggClause(c *AggClauseContext)

	// EnterGroupingElement is called when entering the groupingElement production.
	EnterGroupingElement(c *GroupingElementContext)

	// EnterGroupingSet is called when entering the groupingSet production.
	EnterGroupingSet(c *GroupingSetContext)

	// EnterHavingClause is called when entering the havingClause production.
	EnterHavingClause(c *HavingClauseContext)

	// EnterQualifyClause is called when entering the qualifyClause production.
	EnterQualifyClause(c *QualifyClauseContext)

	// EnterSelectHint is called when entering the selectHint production.
	EnterSelectHint(c *SelectHintContext)

	// EnterHintStatement is called when entering the hintStatement production.
	EnterHintStatement(c *HintStatementContext)

	// EnterHintAssignment is called when entering the hintAssignment production.
	EnterHintAssignment(c *HintAssignmentContext)

	// EnterUpdateAssignment is called when entering the updateAssignment production.
	EnterUpdateAssignment(c *UpdateAssignmentContext)

	// EnterUpdateAssignmentSeq is called when entering the updateAssignmentSeq production.
	EnterUpdateAssignmentSeq(c *UpdateAssignmentSeqContext)

	// EnterLateralView is called when entering the lateralView production.
	EnterLateralView(c *LateralViewContext)

	// EnterQueryOrganization is called when entering the queryOrganization production.
	EnterQueryOrganization(c *QueryOrganizationContext)

	// EnterSortClause is called when entering the sortClause production.
	EnterSortClause(c *SortClauseContext)

	// EnterSortItem is called when entering the sortItem production.
	EnterSortItem(c *SortItemContext)

	// EnterLimitClause is called when entering the limitClause production.
	EnterLimitClause(c *LimitClauseContext)

	// EnterPartitionClause is called when entering the partitionClause production.
	EnterPartitionClause(c *PartitionClauseContext)

	// EnterJoinType is called when entering the joinType production.
	EnterJoinType(c *JoinTypeContext)

	// EnterJoinCriteria is called when entering the joinCriteria production.
	EnterJoinCriteria(c *JoinCriteriaContext)

	// EnterIdentifierList is called when entering the identifierList production.
	EnterIdentifierList(c *IdentifierListContext)

	// EnterIdentifierSeq is called when entering the identifierSeq production.
	EnterIdentifierSeq(c *IdentifierSeqContext)

	// EnterOptScanParams is called when entering the optScanParams production.
	EnterOptScanParams(c *OptScanParamsContext)

	// EnterTableName is called when entering the tableName production.
	EnterTableName(c *TableNameContext)

	// EnterAliasedQuery is called when entering the aliasedQuery production.
	EnterAliasedQuery(c *AliasedQueryContext)

	// EnterTableValuedFunction is called when entering the tableValuedFunction production.
	EnterTableValuedFunction(c *TableValuedFunctionContext)

	// EnterRelationList is called when entering the relationList production.
	EnterRelationList(c *RelationListContext)

	// EnterMaterializedViewName is called when entering the materializedViewName production.
	EnterMaterializedViewName(c *MaterializedViewNameContext)

	// EnterPropertyClause is called when entering the propertyClause production.
	EnterPropertyClause(c *PropertyClauseContext)

	// EnterPropertyItemList is called when entering the propertyItemList production.
	EnterPropertyItemList(c *PropertyItemListContext)

	// EnterPropertyItem is called when entering the propertyItem production.
	EnterPropertyItem(c *PropertyItemContext)

	// EnterPropertyKey is called when entering the propertyKey production.
	EnterPropertyKey(c *PropertyKeyContext)

	// EnterPropertyValue is called when entering the propertyValue production.
	EnterPropertyValue(c *PropertyValueContext)

	// EnterTableAlias is called when entering the tableAlias production.
	EnterTableAlias(c *TableAliasContext)

	// EnterMultipartIdentifier is called when entering the multipartIdentifier production.
	EnterMultipartIdentifier(c *MultipartIdentifierContext)

	// EnterSimpleColumnDefs is called when entering the simpleColumnDefs production.
	EnterSimpleColumnDefs(c *SimpleColumnDefsContext)

	// EnterSimpleColumnDef is called when entering the simpleColumnDef production.
	EnterSimpleColumnDef(c *SimpleColumnDefContext)

	// EnterColumnDefs is called when entering the columnDefs production.
	EnterColumnDefs(c *ColumnDefsContext)

	// EnterColumnDef is called when entering the columnDef production.
	EnterColumnDef(c *ColumnDefContext)

	// EnterIndexDefs is called when entering the indexDefs production.
	EnterIndexDefs(c *IndexDefsContext)

	// EnterIndexDef is called when entering the indexDef production.
	EnterIndexDef(c *IndexDefContext)

	// EnterPartitionsDef is called when entering the partitionsDef production.
	EnterPartitionsDef(c *PartitionsDefContext)

	// EnterPartitionDef is called when entering the partitionDef production.
	EnterPartitionDef(c *PartitionDefContext)

	// EnterLessThanPartitionDef is called when entering the lessThanPartitionDef production.
	EnterLessThanPartitionDef(c *LessThanPartitionDefContext)

	// EnterFixedPartitionDef is called when entering the fixedPartitionDef production.
	EnterFixedPartitionDef(c *FixedPartitionDefContext)

	// EnterStepPartitionDef is called when entering the stepPartitionDef production.
	EnterStepPartitionDef(c *StepPartitionDefContext)

	// EnterInPartitionDef is called when entering the inPartitionDef production.
	EnterInPartitionDef(c *InPartitionDefContext)

	// EnterPartitionValueList is called when entering the partitionValueList production.
	EnterPartitionValueList(c *PartitionValueListContext)

	// EnterPartitionValueDef is called when entering the partitionValueDef production.
	EnterPartitionValueDef(c *PartitionValueDefContext)

	// EnterRollupDefs is called when entering the rollupDefs production.
	EnterRollupDefs(c *RollupDefsContext)

	// EnterRollupDef is called when entering the rollupDef production.
	EnterRollupDef(c *RollupDefContext)

	// EnterAggTypeDef is called when entering the aggTypeDef production.
	EnterAggTypeDef(c *AggTypeDefContext)

	// EnterTabletList is called when entering the tabletList production.
	EnterTabletList(c *TabletListContext)

	// EnterInlineTable is called when entering the inlineTable production.
	EnterInlineTable(c *InlineTableContext)

	// EnterNamedExpression is called when entering the namedExpression production.
	EnterNamedExpression(c *NamedExpressionContext)

	// EnterNamedExpressionSeq is called when entering the namedExpressionSeq production.
	EnterNamedExpressionSeq(c *NamedExpressionSeqContext)

	// EnterExpression is called when entering the expression production.
	EnterExpression(c *ExpressionContext)

	// EnterLambdaExpression is called when entering the lambdaExpression production.
	EnterLambdaExpression(c *LambdaExpressionContext)

	// EnterExist is called when entering the exist production.
	EnterExist(c *ExistContext)

	// EnterLogicalNot is called when entering the logicalNot production.
	EnterLogicalNot(c *LogicalNotContext)

	// EnterPredicated is called when entering the predicated production.
	EnterPredicated(c *PredicatedContext)

	// EnterIsnull is called when entering the isnull production.
	EnterIsnull(c *IsnullContext)

	// EnterIs_not_null_pred is called when entering the is_not_null_pred production.
	EnterIs_not_null_pred(c *Is_not_null_predContext)

	// EnterLogicalBinary is called when entering the logicalBinary production.
	EnterLogicalBinary(c *LogicalBinaryContext)

	// EnterDoublePipes is called when entering the doublePipes production.
	EnterDoublePipes(c *DoublePipesContext)

	// EnterRowConstructor is called when entering the rowConstructor production.
	EnterRowConstructor(c *RowConstructorContext)

	// EnterRowConstructorItem is called when entering the rowConstructorItem production.
	EnterRowConstructorItem(c *RowConstructorItemContext)

	// EnterPredicate is called when entering the predicate production.
	EnterPredicate(c *PredicateContext)

	// EnterValueExpressionDefault is called when entering the valueExpressionDefault production.
	EnterValueExpressionDefault(c *ValueExpressionDefaultContext)

	// EnterComparison is called when entering the comparison production.
	EnterComparison(c *ComparisonContext)

	// EnterArithmeticBinary is called when entering the arithmeticBinary production.
	EnterArithmeticBinary(c *ArithmeticBinaryContext)

	// EnterArithmeticUnary is called when entering the arithmeticUnary production.
	EnterArithmeticUnary(c *ArithmeticUnaryContext)

	// EnterDereference is called when entering the dereference production.
	EnterDereference(c *DereferenceContext)

	// EnterCurrentDate is called when entering the currentDate production.
	EnterCurrentDate(c *CurrentDateContext)

	// EnterCast is called when entering the cast production.
	EnterCast(c *CastContext)

	// EnterParenthesizedExpression is called when entering the parenthesizedExpression production.
	EnterParenthesizedExpression(c *ParenthesizedExpressionContext)

	// EnterUserVariable is called when entering the userVariable production.
	EnterUserVariable(c *UserVariableContext)

	// EnterElementAt is called when entering the elementAt production.
	EnterElementAt(c *ElementAtContext)

	// EnterLocalTimestamp is called when entering the localTimestamp production.
	EnterLocalTimestamp(c *LocalTimestampContext)

	// EnterCharFunction is called when entering the charFunction production.
	EnterCharFunction(c *CharFunctionContext)

	// EnterIntervalLiteral is called when entering the intervalLiteral production.
	EnterIntervalLiteral(c *IntervalLiteralContext)

	// EnterSimpleCase is called when entering the simpleCase production.
	EnterSimpleCase(c *SimpleCaseContext)

	// EnterColumnReference is called when entering the columnReference production.
	EnterColumnReference(c *ColumnReferenceContext)

	// EnterStar is called when entering the star production.
	EnterStar(c *StarContext)

	// EnterSessionUser is called when entering the sessionUser production.
	EnterSessionUser(c *SessionUserContext)

	// EnterConvertType is called when entering the convertType production.
	EnterConvertType(c *ConvertTypeContext)

	// EnterConvertCharSet is called when entering the convertCharSet production.
	EnterConvertCharSet(c *ConvertCharSetContext)

	// EnterSubqueryExpression is called when entering the subqueryExpression production.
	EnterSubqueryExpression(c *SubqueryExpressionContext)

	// EnterEncryptKey is called when entering the encryptKey production.
	EnterEncryptKey(c *EncryptKeyContext)

	// EnterCurrentTime is called when entering the currentTime production.
	EnterCurrentTime(c *CurrentTimeContext)

	// EnterLocalTime is called when entering the localTime production.
	EnterLocalTime(c *LocalTimeContext)

	// EnterSystemVariable is called when entering the systemVariable production.
	EnterSystemVariable(c *SystemVariableContext)

	// EnterCollate is called when entering the collate production.
	EnterCollate(c *CollateContext)

	// EnterCurrentUser is called when entering the currentUser production.
	EnterCurrentUser(c *CurrentUserContext)

	// EnterConstantDefault is called when entering the constantDefault production.
	EnterConstantDefault(c *ConstantDefaultContext)

	// EnterExtract is called when entering the extract production.
	EnterExtract(c *ExtractContext)

	// EnterCurrentTimestamp is called when entering the currentTimestamp production.
	EnterCurrentTimestamp(c *CurrentTimestampContext)

	// EnterFunctionCall is called when entering the functionCall production.
	EnterFunctionCall(c *FunctionCallContext)

	// EnterArraySlice is called when entering the arraySlice production.
	EnterArraySlice(c *ArraySliceContext)

	// EnterSearchedCase is called when entering the searchedCase production.
	EnterSearchedCase(c *SearchedCaseContext)

	// EnterExcept is called when entering the except production.
	EnterExcept(c *ExceptContext)

	// EnterReplace is called when entering the replace production.
	EnterReplace(c *ReplaceContext)

	// EnterCastDataType is called when entering the castDataType production.
	EnterCastDataType(c *CastDataTypeContext)

	// EnterFunctionCallExpression is called when entering the functionCallExpression production.
	EnterFunctionCallExpression(c *FunctionCallExpressionContext)

	// EnterFunctionIdentifier is called when entering the functionIdentifier production.
	EnterFunctionIdentifier(c *FunctionIdentifierContext)

	// EnterFunctionNameIdentifier is called when entering the functionNameIdentifier production.
	EnterFunctionNameIdentifier(c *FunctionNameIdentifierContext)

	// EnterWindowSpec is called when entering the windowSpec production.
	EnterWindowSpec(c *WindowSpecContext)

	// EnterWindowFrame is called when entering the windowFrame production.
	EnterWindowFrame(c *WindowFrameContext)

	// EnterFrameUnits is called when entering the frameUnits production.
	EnterFrameUnits(c *FrameUnitsContext)

	// EnterFrameBoundary is called when entering the frameBoundary production.
	EnterFrameBoundary(c *FrameBoundaryContext)

	// EnterQualifiedName is called when entering the qualifiedName production.
	EnterQualifiedName(c *QualifiedNameContext)

	// EnterSpecifiedPartition is called when entering the specifiedPartition production.
	EnterSpecifiedPartition(c *SpecifiedPartitionContext)

	// EnterNullLiteral is called when entering the nullLiteral production.
	EnterNullLiteral(c *NullLiteralContext)

	// EnterTypeConstructor is called when entering the typeConstructor production.
	EnterTypeConstructor(c *TypeConstructorContext)

	// EnterNumericLiteral is called when entering the numericLiteral production.
	EnterNumericLiteral(c *NumericLiteralContext)

	// EnterBooleanLiteral is called when entering the booleanLiteral production.
	EnterBooleanLiteral(c *BooleanLiteralContext)

	// EnterStringLiteral is called when entering the stringLiteral production.
	EnterStringLiteral(c *StringLiteralContext)

	// EnterArrayLiteral is called when entering the arrayLiteral production.
	EnterArrayLiteral(c *ArrayLiteralContext)

	// EnterMapLiteral is called when entering the mapLiteral production.
	EnterMapLiteral(c *MapLiteralContext)

	// EnterStructLiteral is called when entering the structLiteral production.
	EnterStructLiteral(c *StructLiteralContext)

	// EnterPlaceholder is called when entering the placeholder production.
	EnterPlaceholder(c *PlaceholderContext)

	// EnterComparisonOperator is called when entering the comparisonOperator production.
	EnterComparisonOperator(c *ComparisonOperatorContext)

	// EnterBooleanValue is called when entering the booleanValue production.
	EnterBooleanValue(c *BooleanValueContext)

	// EnterWhenClause is called when entering the whenClause production.
	EnterWhenClause(c *WhenClauseContext)

	// EnterInterval is called when entering the interval production.
	EnterInterval(c *IntervalContext)

	// EnterUnitIdentifier is called when entering the unitIdentifier production.
	EnterUnitIdentifier(c *UnitIdentifierContext)

	// EnterDataTypeWithNullable is called when entering the dataTypeWithNullable production.
	EnterDataTypeWithNullable(c *DataTypeWithNullableContext)

	// EnterComplexDataType is called when entering the complexDataType production.
	EnterComplexDataType(c *ComplexDataTypeContext)

	// EnterAggStateDataType is called when entering the aggStateDataType production.
	EnterAggStateDataType(c *AggStateDataTypeContext)

	// EnterPrimitiveDataType is called when entering the primitiveDataType production.
	EnterPrimitiveDataType(c *PrimitiveDataTypeContext)

	// EnterPrimitiveColType is called when entering the primitiveColType production.
	EnterPrimitiveColType(c *PrimitiveColTypeContext)

	// EnterComplexColTypeList is called when entering the complexColTypeList production.
	EnterComplexColTypeList(c *ComplexColTypeListContext)

	// EnterComplexColType is called when entering the complexColType production.
	EnterComplexColType(c *ComplexColTypeContext)

	// EnterCommentSpec is called when entering the commentSpec production.
	EnterCommentSpec(c *CommentSpecContext)

	// EnterSample is called when entering the sample production.
	EnterSample(c *SampleContext)

	// EnterSampleByPercentile is called when entering the sampleByPercentile production.
	EnterSampleByPercentile(c *SampleByPercentileContext)

	// EnterSampleByRows is called when entering the sampleByRows production.
	EnterSampleByRows(c *SampleByRowsContext)

	// EnterTableSnapshot is called when entering the tableSnapshot production.
	EnterTableSnapshot(c *TableSnapshotContext)

	// EnterErrorCapturingIdentifier is called when entering the errorCapturingIdentifier production.
	EnterErrorCapturingIdentifier(c *ErrorCapturingIdentifierContext)

	// EnterErrorIdent is called when entering the errorIdent production.
	EnterErrorIdent(c *ErrorIdentContext)

	// EnterRealIdent is called when entering the realIdent production.
	EnterRealIdent(c *RealIdentContext)

	// EnterIdentifier is called when entering the identifier production.
	EnterIdentifier(c *IdentifierContext)

	// EnterUnquotedIdentifier is called when entering the unquotedIdentifier production.
	EnterUnquotedIdentifier(c *UnquotedIdentifierContext)

	// EnterQuotedIdentifierAlternative is called when entering the quotedIdentifierAlternative production.
	EnterQuotedIdentifierAlternative(c *QuotedIdentifierAlternativeContext)

	// EnterQuotedIdentifier is called when entering the quotedIdentifier production.
	EnterQuotedIdentifier(c *QuotedIdentifierContext)

	// EnterIntegerLiteral is called when entering the integerLiteral production.
	EnterIntegerLiteral(c *IntegerLiteralContext)

	// EnterDecimalLiteral is called when entering the decimalLiteral production.
	EnterDecimalLiteral(c *DecimalLiteralContext)

	// EnterNonReserved is called when entering the nonReserved production.
	EnterNonReserved(c *NonReservedContext)

	// ExitMultiStatements is called when exiting the multiStatements production.
	ExitMultiStatements(c *MultiStatementsContext)

	// ExitSingleStatement is called when exiting the singleStatement production.
	ExitSingleStatement(c *SingleStatementContext)

	// ExitStatementBaseAlias is called when exiting the statementBaseAlias production.
	ExitStatementBaseAlias(c *StatementBaseAliasContext)

	// ExitCallProcedure is called when exiting the callProcedure production.
	ExitCallProcedure(c *CallProcedureContext)

	// ExitCreateProcedure is called when exiting the createProcedure production.
	ExitCreateProcedure(c *CreateProcedureContext)

	// ExitDropProcedure is called when exiting the dropProcedure production.
	ExitDropProcedure(c *DropProcedureContext)

	// ExitShowProcedureStatus is called when exiting the showProcedureStatus production.
	ExitShowProcedureStatus(c *ShowProcedureStatusContext)

	// ExitShowCreateProcedure is called when exiting the showCreateProcedure production.
	ExitShowCreateProcedure(c *ShowCreateProcedureContext)

	// ExitShowConfig is called when exiting the showConfig production.
	ExitShowConfig(c *ShowConfigContext)

	// ExitStatementDefault is called when exiting the statementDefault production.
	ExitStatementDefault(c *StatementDefaultContext)

	// ExitSupportedDmlStatementAlias is called when exiting the supportedDmlStatementAlias production.
	ExitSupportedDmlStatementAlias(c *SupportedDmlStatementAliasContext)

	// ExitSupportedCreateStatementAlias is called when exiting the supportedCreateStatementAlias production.
	ExitSupportedCreateStatementAlias(c *SupportedCreateStatementAliasContext)

	// ExitSupportedAlterStatementAlias is called when exiting the supportedAlterStatementAlias production.
	ExitSupportedAlterStatementAlias(c *SupportedAlterStatementAliasContext)

	// ExitMaterializedViewStatementAlias is called when exiting the materializedViewStatementAlias production.
	ExitMaterializedViewStatementAlias(c *MaterializedViewStatementAliasContext)

	// ExitSupportedJobStatementAlias is called when exiting the supportedJobStatementAlias production.
	ExitSupportedJobStatementAlias(c *SupportedJobStatementAliasContext)

	// ExitConstraintStatementAlias is called when exiting the constraintStatementAlias production.
	ExitConstraintStatementAlias(c *ConstraintStatementAliasContext)

	// ExitSupportedCleanStatementAlias is called when exiting the supportedCleanStatementAlias production.
	ExitSupportedCleanStatementAlias(c *SupportedCleanStatementAliasContext)

	// ExitSupportedDescribeStatementAlias is called when exiting the supportedDescribeStatementAlias production.
	ExitSupportedDescribeStatementAlias(c *SupportedDescribeStatementAliasContext)

	// ExitSupportedDropStatementAlias is called when exiting the supportedDropStatementAlias production.
	ExitSupportedDropStatementAlias(c *SupportedDropStatementAliasContext)

	// ExitSupportedSetStatementAlias is called when exiting the supportedSetStatementAlias production.
	ExitSupportedSetStatementAlias(c *SupportedSetStatementAliasContext)

	// ExitSupportedUnsetStatementAlias is called when exiting the supportedUnsetStatementAlias production.
	ExitSupportedUnsetStatementAlias(c *SupportedUnsetStatementAliasContext)

	// ExitSupportedRefreshStatementAlias is called when exiting the supportedRefreshStatementAlias production.
	ExitSupportedRefreshStatementAlias(c *SupportedRefreshStatementAliasContext)

	// ExitSupportedShowStatementAlias is called when exiting the supportedShowStatementAlias production.
	ExitSupportedShowStatementAlias(c *SupportedShowStatementAliasContext)

	// ExitSupportedLoadStatementAlias is called when exiting the supportedLoadStatementAlias production.
	ExitSupportedLoadStatementAlias(c *SupportedLoadStatementAliasContext)

	// ExitSupportedCancelStatementAlias is called when exiting the supportedCancelStatementAlias production.
	ExitSupportedCancelStatementAlias(c *SupportedCancelStatementAliasContext)

	// ExitSupportedRecoverStatementAlias is called when exiting the supportedRecoverStatementAlias production.
	ExitSupportedRecoverStatementAlias(c *SupportedRecoverStatementAliasContext)

	// ExitSupportedAdminStatementAlias is called when exiting the supportedAdminStatementAlias production.
	ExitSupportedAdminStatementAlias(c *SupportedAdminStatementAliasContext)

	// ExitSupportedUseStatementAlias is called when exiting the supportedUseStatementAlias production.
	ExitSupportedUseStatementAlias(c *SupportedUseStatementAliasContext)

	// ExitSupportedOtherStatementAlias is called when exiting the supportedOtherStatementAlias production.
	ExitSupportedOtherStatementAlias(c *SupportedOtherStatementAliasContext)

	// ExitSupportedKillStatementAlias is called when exiting the supportedKillStatementAlias production.
	ExitSupportedKillStatementAlias(c *SupportedKillStatementAliasContext)

	// ExitSupportedStatsStatementAlias is called when exiting the supportedStatsStatementAlias production.
	ExitSupportedStatsStatementAlias(c *SupportedStatsStatementAliasContext)

	// ExitSupportedTransactionStatementAlias is called when exiting the supportedTransactionStatementAlias production.
	ExitSupportedTransactionStatementAlias(c *SupportedTransactionStatementAliasContext)

	// ExitSupportedGrantRevokeStatementAlias is called when exiting the supportedGrantRevokeStatementAlias production.
	ExitSupportedGrantRevokeStatementAlias(c *SupportedGrantRevokeStatementAliasContext)

	// ExitUnsupported is called when exiting the unsupported production.
	ExitUnsupported(c *UnsupportedContext)

	// ExitUnsupportedStatement is called when exiting the unsupportedStatement production.
	ExitUnsupportedStatement(c *UnsupportedStatementContext)

	// ExitCreateMTMV is called when exiting the createMTMV production.
	ExitCreateMTMV(c *CreateMTMVContext)

	// ExitRefreshMTMV is called when exiting the refreshMTMV production.
	ExitRefreshMTMV(c *RefreshMTMVContext)

	// ExitAlterMTMV is called when exiting the alterMTMV production.
	ExitAlterMTMV(c *AlterMTMVContext)

	// ExitDropMTMV is called when exiting the dropMTMV production.
	ExitDropMTMV(c *DropMTMVContext)

	// ExitPauseMTMV is called when exiting the pauseMTMV production.
	ExitPauseMTMV(c *PauseMTMVContext)

	// ExitResumeMTMV is called when exiting the resumeMTMV production.
	ExitResumeMTMV(c *ResumeMTMVContext)

	// ExitCancelMTMVTask is called when exiting the cancelMTMVTask production.
	ExitCancelMTMVTask(c *CancelMTMVTaskContext)

	// ExitShowCreateMTMV is called when exiting the showCreateMTMV production.
	ExitShowCreateMTMV(c *ShowCreateMTMVContext)

	// ExitCreateScheduledJob is called when exiting the createScheduledJob production.
	ExitCreateScheduledJob(c *CreateScheduledJobContext)

	// ExitPauseJob is called when exiting the pauseJob production.
	ExitPauseJob(c *PauseJobContext)

	// ExitDropJob is called when exiting the dropJob production.
	ExitDropJob(c *DropJobContext)

	// ExitResumeJob is called when exiting the resumeJob production.
	ExitResumeJob(c *ResumeJobContext)

	// ExitCancelJobTask is called when exiting the cancelJobTask production.
	ExitCancelJobTask(c *CancelJobTaskContext)

	// ExitAddConstraint is called when exiting the addConstraint production.
	ExitAddConstraint(c *AddConstraintContext)

	// ExitDropConstraint is called when exiting the dropConstraint production.
	ExitDropConstraint(c *DropConstraintContext)

	// ExitShowConstraint is called when exiting the showConstraint production.
	ExitShowConstraint(c *ShowConstraintContext)

	// ExitInsertTable is called when exiting the insertTable production.
	ExitInsertTable(c *InsertTableContext)

	// ExitUpdate is called when exiting the update production.
	ExitUpdate(c *UpdateContext)

	// ExitDelete is called when exiting the delete production.
	ExitDelete(c *DeleteContext)

	// ExitLoad is called when exiting the load production.
	ExitLoad(c *LoadContext)

	// ExitExport is called when exiting the export production.
	ExitExport(c *ExportContext)

	// ExitReplay is called when exiting the replay production.
	ExitReplay(c *ReplayContext)

	// ExitCopyInto is called when exiting the copyInto production.
	ExitCopyInto(c *CopyIntoContext)

	// ExitTruncateTable is called when exiting the truncateTable production.
	ExitTruncateTable(c *TruncateTableContext)

	// ExitCreateTable is called when exiting the createTable production.
	ExitCreateTable(c *CreateTableContext)

	// ExitCreateView is called when exiting the createView production.
	ExitCreateView(c *CreateViewContext)

	// ExitCreateFile is called when exiting the createFile production.
	ExitCreateFile(c *CreateFileContext)

	// ExitCreateTableLike is called when exiting the createTableLike production.
	ExitCreateTableLike(c *CreateTableLikeContext)

	// ExitCreateRole is called when exiting the createRole production.
	ExitCreateRole(c *CreateRoleContext)

	// ExitCreateWorkloadGroup is called when exiting the createWorkloadGroup production.
	ExitCreateWorkloadGroup(c *CreateWorkloadGroupContext)

	// ExitCreateCatalog is called when exiting the createCatalog production.
	ExitCreateCatalog(c *CreateCatalogContext)

	// ExitCreateRowPolicy is called when exiting the createRowPolicy production.
	ExitCreateRowPolicy(c *CreateRowPolicyContext)

	// ExitCreateStoragePolicy is called when exiting the createStoragePolicy production.
	ExitCreateStoragePolicy(c *CreateStoragePolicyContext)

	// ExitBuildIndex is called when exiting the buildIndex production.
	ExitBuildIndex(c *BuildIndexContext)

	// ExitCreateIndex is called when exiting the createIndex production.
	ExitCreateIndex(c *CreateIndexContext)

	// ExitCreateWorkloadPolicy is called when exiting the createWorkloadPolicy production.
	ExitCreateWorkloadPolicy(c *CreateWorkloadPolicyContext)

	// ExitCreateSqlBlockRule is called when exiting the createSqlBlockRule production.
	ExitCreateSqlBlockRule(c *CreateSqlBlockRuleContext)

	// ExitCreateEncryptkey is called when exiting the createEncryptkey production.
	ExitCreateEncryptkey(c *CreateEncryptkeyContext)

	// ExitCreateUserDefineFunction is called when exiting the createUserDefineFunction production.
	ExitCreateUserDefineFunction(c *CreateUserDefineFunctionContext)

	// ExitCreateAliasFunction is called when exiting the createAliasFunction production.
	ExitCreateAliasFunction(c *CreateAliasFunctionContext)

	// ExitCreateUser is called when exiting the createUser production.
	ExitCreateUser(c *CreateUserContext)

	// ExitCreateDatabase is called when exiting the createDatabase production.
	ExitCreateDatabase(c *CreateDatabaseContext)

	// ExitCreateRepository is called when exiting the createRepository production.
	ExitCreateRepository(c *CreateRepositoryContext)

	// ExitCreateResource is called when exiting the createResource production.
	ExitCreateResource(c *CreateResourceContext)

	// ExitCreateDictionary is called when exiting the createDictionary production.
	ExitCreateDictionary(c *CreateDictionaryContext)

	// ExitCreateStage is called when exiting the createStage production.
	ExitCreateStage(c *CreateStageContext)

	// ExitCreateStorageVault is called when exiting the createStorageVault production.
	ExitCreateStorageVault(c *CreateStorageVaultContext)

	// ExitCreateIndexAnalyzer is called when exiting the createIndexAnalyzer production.
	ExitCreateIndexAnalyzer(c *CreateIndexAnalyzerContext)

	// ExitCreateIndexTokenizer is called when exiting the createIndexTokenizer production.
	ExitCreateIndexTokenizer(c *CreateIndexTokenizerContext)

	// ExitCreateIndexTokenFilter is called when exiting the createIndexTokenFilter production.
	ExitCreateIndexTokenFilter(c *CreateIndexTokenFilterContext)

	// ExitDictionaryColumnDefs is called when exiting the dictionaryColumnDefs production.
	ExitDictionaryColumnDefs(c *DictionaryColumnDefsContext)

	// ExitDictionaryColumnDef is called when exiting the dictionaryColumnDef production.
	ExitDictionaryColumnDef(c *DictionaryColumnDefContext)

	// ExitAlterSystem is called when exiting the alterSystem production.
	ExitAlterSystem(c *AlterSystemContext)

	// ExitAlterView is called when exiting the alterView production.
	ExitAlterView(c *AlterViewContext)

	// ExitAlterCatalogRename is called when exiting the alterCatalogRename production.
	ExitAlterCatalogRename(c *AlterCatalogRenameContext)

	// ExitAlterRole is called when exiting the alterRole production.
	ExitAlterRole(c *AlterRoleContext)

	// ExitAlterStorageVault is called when exiting the alterStorageVault production.
	ExitAlterStorageVault(c *AlterStorageVaultContext)

	// ExitAlterWorkloadGroup is called when exiting the alterWorkloadGroup production.
	ExitAlterWorkloadGroup(c *AlterWorkloadGroupContext)

	// ExitAlterCatalogProperties is called when exiting the alterCatalogProperties production.
	ExitAlterCatalogProperties(c *AlterCatalogPropertiesContext)

	// ExitAlterWorkloadPolicy is called when exiting the alterWorkloadPolicy production.
	ExitAlterWorkloadPolicy(c *AlterWorkloadPolicyContext)

	// ExitAlterSqlBlockRule is called when exiting the alterSqlBlockRule production.
	ExitAlterSqlBlockRule(c *AlterSqlBlockRuleContext)

	// ExitAlterCatalogComment is called when exiting the alterCatalogComment production.
	ExitAlterCatalogComment(c *AlterCatalogCommentContext)

	// ExitAlterDatabaseRename is called when exiting the alterDatabaseRename production.
	ExitAlterDatabaseRename(c *AlterDatabaseRenameContext)

	// ExitAlterStoragePolicy is called when exiting the alterStoragePolicy production.
	ExitAlterStoragePolicy(c *AlterStoragePolicyContext)

	// ExitAlterTable is called when exiting the alterTable production.
	ExitAlterTable(c *AlterTableContext)

	// ExitAlterTableAddRollup is called when exiting the alterTableAddRollup production.
	ExitAlterTableAddRollup(c *AlterTableAddRollupContext)

	// ExitAlterTableDropRollup is called when exiting the alterTableDropRollup production.
	ExitAlterTableDropRollup(c *AlterTableDropRollupContext)

	// ExitAlterTableProperties is called when exiting the alterTableProperties production.
	ExitAlterTableProperties(c *AlterTablePropertiesContext)

	// ExitAlterDatabaseSetQuota is called when exiting the alterDatabaseSetQuota production.
	ExitAlterDatabaseSetQuota(c *AlterDatabaseSetQuotaContext)

	// ExitAlterDatabaseProperties is called when exiting the alterDatabaseProperties production.
	ExitAlterDatabaseProperties(c *AlterDatabasePropertiesContext)

	// ExitAlterSystemRenameComputeGroup is called when exiting the alterSystemRenameComputeGroup production.
	ExitAlterSystemRenameComputeGroup(c *AlterSystemRenameComputeGroupContext)

	// ExitAlterResource is called when exiting the alterResource production.
	ExitAlterResource(c *AlterResourceContext)

	// ExitAlterRepository is called when exiting the alterRepository production.
	ExitAlterRepository(c *AlterRepositoryContext)

	// ExitAlterRoutineLoad is called when exiting the alterRoutineLoad production.
	ExitAlterRoutineLoad(c *AlterRoutineLoadContext)

	// ExitAlterColocateGroup is called when exiting the alterColocateGroup production.
	ExitAlterColocateGroup(c *AlterColocateGroupContext)

	// ExitAlterUser is called when exiting the alterUser production.
	ExitAlterUser(c *AlterUserContext)

	// ExitDropCatalogRecycleBin is called when exiting the dropCatalogRecycleBin production.
	ExitDropCatalogRecycleBin(c *DropCatalogRecycleBinContext)

	// ExitDropEncryptkey is called when exiting the dropEncryptkey production.
	ExitDropEncryptkey(c *DropEncryptkeyContext)

	// ExitDropRole is called when exiting the dropRole production.
	ExitDropRole(c *DropRoleContext)

	// ExitDropSqlBlockRule is called when exiting the dropSqlBlockRule production.
	ExitDropSqlBlockRule(c *DropSqlBlockRuleContext)

	// ExitDropUser is called when exiting the dropUser production.
	ExitDropUser(c *DropUserContext)

	// ExitDropStoragePolicy is called when exiting the dropStoragePolicy production.
	ExitDropStoragePolicy(c *DropStoragePolicyContext)

	// ExitDropWorkloadGroup is called when exiting the dropWorkloadGroup production.
	ExitDropWorkloadGroup(c *DropWorkloadGroupContext)

	// ExitDropCatalog is called when exiting the dropCatalog production.
	ExitDropCatalog(c *DropCatalogContext)

	// ExitDropFile is called when exiting the dropFile production.
	ExitDropFile(c *DropFileContext)

	// ExitDropWorkloadPolicy is called when exiting the dropWorkloadPolicy production.
	ExitDropWorkloadPolicy(c *DropWorkloadPolicyContext)

	// ExitDropRepository is called when exiting the dropRepository production.
	ExitDropRepository(c *DropRepositoryContext)

	// ExitDropTable is called when exiting the dropTable production.
	ExitDropTable(c *DropTableContext)

	// ExitDropDatabase is called when exiting the dropDatabase production.
	ExitDropDatabase(c *DropDatabaseContext)

	// ExitDropFunction is called when exiting the dropFunction production.
	ExitDropFunction(c *DropFunctionContext)

	// ExitDropIndex is called when exiting the dropIndex production.
	ExitDropIndex(c *DropIndexContext)

	// ExitDropResource is called when exiting the dropResource production.
	ExitDropResource(c *DropResourceContext)

	// ExitDropRowPolicy is called when exiting the dropRowPolicy production.
	ExitDropRowPolicy(c *DropRowPolicyContext)

	// ExitDropDictionary is called when exiting the dropDictionary production.
	ExitDropDictionary(c *DropDictionaryContext)

	// ExitDropStage is called when exiting the dropStage production.
	ExitDropStage(c *DropStageContext)

	// ExitDropView is called when exiting the dropView production.
	ExitDropView(c *DropViewContext)

	// ExitDropIndexAnalyzer is called when exiting the dropIndexAnalyzer production.
	ExitDropIndexAnalyzer(c *DropIndexAnalyzerContext)

	// ExitDropIndexTokenizer is called when exiting the dropIndexTokenizer production.
	ExitDropIndexTokenizer(c *DropIndexTokenizerContext)

	// ExitDropIndexTokenFilter is called when exiting the dropIndexTokenFilter production.
	ExitDropIndexTokenFilter(c *DropIndexTokenFilterContext)

	// ExitShowVariables is called when exiting the showVariables production.
	ExitShowVariables(c *ShowVariablesContext)

	// ExitShowAuthors is called when exiting the showAuthors production.
	ExitShowAuthors(c *ShowAuthorsContext)

	// ExitShowAlterTable is called when exiting the showAlterTable production.
	ExitShowAlterTable(c *ShowAlterTableContext)

	// ExitShowCreateDatabase is called when exiting the showCreateDatabase production.
	ExitShowCreateDatabase(c *ShowCreateDatabaseContext)

	// ExitShowBackup is called when exiting the showBackup production.
	ExitShowBackup(c *ShowBackupContext)

	// ExitShowBroker is called when exiting the showBroker production.
	ExitShowBroker(c *ShowBrokerContext)

	// ExitShowBuildIndex is called when exiting the showBuildIndex production.
	ExitShowBuildIndex(c *ShowBuildIndexContext)

	// ExitShowDynamicPartition is called when exiting the showDynamicPartition production.
	ExitShowDynamicPartition(c *ShowDynamicPartitionContext)

	// ExitShowEvents is called when exiting the showEvents production.
	ExitShowEvents(c *ShowEventsContext)

	// ExitShowExport is called when exiting the showExport production.
	ExitShowExport(c *ShowExportContext)

	// ExitShowLastInsert is called when exiting the showLastInsert production.
	ExitShowLastInsert(c *ShowLastInsertContext)

	// ExitShowCharset is called when exiting the showCharset production.
	ExitShowCharset(c *ShowCharsetContext)

	// ExitShowDelete is called when exiting the showDelete production.
	ExitShowDelete(c *ShowDeleteContext)

	// ExitShowCreateFunction is called when exiting the showCreateFunction production.
	ExitShowCreateFunction(c *ShowCreateFunctionContext)

	// ExitShowFunctions is called when exiting the showFunctions production.
	ExitShowFunctions(c *ShowFunctionsContext)

	// ExitShowGlobalFunctions is called when exiting the showGlobalFunctions production.
	ExitShowGlobalFunctions(c *ShowGlobalFunctionsContext)

	// ExitShowGrants is called when exiting the showGrants production.
	ExitShowGrants(c *ShowGrantsContext)

	// ExitShowGrantsForUser is called when exiting the showGrantsForUser production.
	ExitShowGrantsForUser(c *ShowGrantsForUserContext)

	// ExitShowCreateUser is called when exiting the showCreateUser production.
	ExitShowCreateUser(c *ShowCreateUserContext)

	// ExitShowSnapshot is called when exiting the showSnapshot production.
	ExitShowSnapshot(c *ShowSnapshotContext)

	// ExitShowLoadProfile is called when exiting the showLoadProfile production.
	ExitShowLoadProfile(c *ShowLoadProfileContext)

	// ExitShowCreateRepository is called when exiting the showCreateRepository production.
	ExitShowCreateRepository(c *ShowCreateRepositoryContext)

	// ExitShowView is called when exiting the showView production.
	ExitShowView(c *ShowViewContext)

	// ExitShowPlugins is called when exiting the showPlugins production.
	ExitShowPlugins(c *ShowPluginsContext)

	// ExitShowStorageVault is called when exiting the showStorageVault production.
	ExitShowStorageVault(c *ShowStorageVaultContext)

	// ExitShowRepositories is called when exiting the showRepositories production.
	ExitShowRepositories(c *ShowRepositoriesContext)

	// ExitShowEncryptKeys is called when exiting the showEncryptKeys production.
	ExitShowEncryptKeys(c *ShowEncryptKeysContext)

	// ExitShowCreateTable is called when exiting the showCreateTable production.
	ExitShowCreateTable(c *ShowCreateTableContext)

	// ExitShowProcessList is called when exiting the showProcessList production.
	ExitShowProcessList(c *ShowProcessListContext)

	// ExitShowPartitions is called when exiting the showPartitions production.
	ExitShowPartitions(c *ShowPartitionsContext)

	// ExitShowRestore is called when exiting the showRestore production.
	ExitShowRestore(c *ShowRestoreContext)

	// ExitShowRoles is called when exiting the showRoles production.
	ExitShowRoles(c *ShowRolesContext)

	// ExitShowPartitionId is called when exiting the showPartitionId production.
	ExitShowPartitionId(c *ShowPartitionIdContext)

	// ExitShowPrivileges is called when exiting the showPrivileges production.
	ExitShowPrivileges(c *ShowPrivilegesContext)

	// ExitShowProc is called when exiting the showProc production.
	ExitShowProc(c *ShowProcContext)

	// ExitShowSmallFiles is called when exiting the showSmallFiles production.
	ExitShowSmallFiles(c *ShowSmallFilesContext)

	// ExitShowStorageEngines is called when exiting the showStorageEngines production.
	ExitShowStorageEngines(c *ShowStorageEnginesContext)

	// ExitShowCreateCatalog is called when exiting the showCreateCatalog production.
	ExitShowCreateCatalog(c *ShowCreateCatalogContext)

	// ExitShowCatalog is called when exiting the showCatalog production.
	ExitShowCatalog(c *ShowCatalogContext)

	// ExitShowCatalogs is called when exiting the showCatalogs production.
	ExitShowCatalogs(c *ShowCatalogsContext)

	// ExitShowUserProperties is called when exiting the showUserProperties production.
	ExitShowUserProperties(c *ShowUserPropertiesContext)

	// ExitShowAllProperties is called when exiting the showAllProperties production.
	ExitShowAllProperties(c *ShowAllPropertiesContext)

	// ExitShowCollation is called when exiting the showCollation production.
	ExitShowCollation(c *ShowCollationContext)

	// ExitShowRowPolicy is called when exiting the showRowPolicy production.
	ExitShowRowPolicy(c *ShowRowPolicyContext)

	// ExitShowStoragePolicy is called when exiting the showStoragePolicy production.
	ExitShowStoragePolicy(c *ShowStoragePolicyContext)

	// ExitShowSqlBlockRule is called when exiting the showSqlBlockRule production.
	ExitShowSqlBlockRule(c *ShowSqlBlockRuleContext)

	// ExitShowCreateView is called when exiting the showCreateView production.
	ExitShowCreateView(c *ShowCreateViewContext)

	// ExitShowDataTypes is called when exiting the showDataTypes production.
	ExitShowDataTypes(c *ShowDataTypesContext)

	// ExitShowData is called when exiting the showData production.
	ExitShowData(c *ShowDataContext)

	// ExitShowCreateMaterializedView is called when exiting the showCreateMaterializedView production.
	ExitShowCreateMaterializedView(c *ShowCreateMaterializedViewContext)

	// ExitShowWarningErrors is called when exiting the showWarningErrors production.
	ExitShowWarningErrors(c *ShowWarningErrorsContext)

	// ExitShowWarningErrorCount is called when exiting the showWarningErrorCount production.
	ExitShowWarningErrorCount(c *ShowWarningErrorCountContext)

	// ExitShowBackends is called when exiting the showBackends production.
	ExitShowBackends(c *ShowBackendsContext)

	// ExitShowStages is called when exiting the showStages production.
	ExitShowStages(c *ShowStagesContext)

	// ExitShowReplicaDistribution is called when exiting the showReplicaDistribution production.
	ExitShowReplicaDistribution(c *ShowReplicaDistributionContext)

	// ExitShowResources is called when exiting the showResources production.
	ExitShowResources(c *ShowResourcesContext)

	// ExitShowLoad is called when exiting the showLoad production.
	ExitShowLoad(c *ShowLoadContext)

	// ExitShowLoadWarings is called when exiting the showLoadWarings production.
	ExitShowLoadWarings(c *ShowLoadWaringsContext)

	// ExitShowTriggers is called when exiting the showTriggers production.
	ExitShowTriggers(c *ShowTriggersContext)

	// ExitShowDiagnoseTablet is called when exiting the showDiagnoseTablet production.
	ExitShowDiagnoseTablet(c *ShowDiagnoseTabletContext)

	// ExitShowOpenTables is called when exiting the showOpenTables production.
	ExitShowOpenTables(c *ShowOpenTablesContext)

	// ExitShowFrontends is called when exiting the showFrontends production.
	ExitShowFrontends(c *ShowFrontendsContext)

	// ExitShowDatabaseId is called when exiting the showDatabaseId production.
	ExitShowDatabaseId(c *ShowDatabaseIdContext)

	// ExitShowColumns is called when exiting the showColumns production.
	ExitShowColumns(c *ShowColumnsContext)

	// ExitShowTableId is called when exiting the showTableId production.
	ExitShowTableId(c *ShowTableIdContext)

	// ExitShowTrash is called when exiting the showTrash production.
	ExitShowTrash(c *ShowTrashContext)

	// ExitShowTypeCast is called when exiting the showTypeCast production.
	ExitShowTypeCast(c *ShowTypeCastContext)

	// ExitShowClusters is called when exiting the showClusters production.
	ExitShowClusters(c *ShowClustersContext)

	// ExitShowStatus is called when exiting the showStatus production.
	ExitShowStatus(c *ShowStatusContext)

	// ExitShowWhitelist is called when exiting the showWhitelist production.
	ExitShowWhitelist(c *ShowWhitelistContext)

	// ExitShowTabletsBelong is called when exiting the showTabletsBelong production.
	ExitShowTabletsBelong(c *ShowTabletsBelongContext)

	// ExitShowDataSkew is called when exiting the showDataSkew production.
	ExitShowDataSkew(c *ShowDataSkewContext)

	// ExitShowTableCreation is called when exiting the showTableCreation production.
	ExitShowTableCreation(c *ShowTableCreationContext)

	// ExitShowTabletStorageFormat is called when exiting the showTabletStorageFormat production.
	ExitShowTabletStorageFormat(c *ShowTabletStorageFormatContext)

	// ExitShowQueryProfile is called when exiting the showQueryProfile production.
	ExitShowQueryProfile(c *ShowQueryProfileContext)

	// ExitShowConvertLsc is called when exiting the showConvertLsc production.
	ExitShowConvertLsc(c *ShowConvertLscContext)

	// ExitShowTables is called when exiting the showTables production.
	ExitShowTables(c *ShowTablesContext)

	// ExitShowViews is called when exiting the showViews production.
	ExitShowViews(c *ShowViewsContext)

	// ExitShowTableStatus is called when exiting the showTableStatus production.
	ExitShowTableStatus(c *ShowTableStatusContext)

	// ExitShowDatabases is called when exiting the showDatabases production.
	ExitShowDatabases(c *ShowDatabasesContext)

	// ExitShowTabletsFromTable is called when exiting the showTabletsFromTable production.
	ExitShowTabletsFromTable(c *ShowTabletsFromTableContext)

	// ExitShowCatalogRecycleBin is called when exiting the showCatalogRecycleBin production.
	ExitShowCatalogRecycleBin(c *ShowCatalogRecycleBinContext)

	// ExitShowTabletId is called when exiting the showTabletId production.
	ExitShowTabletId(c *ShowTabletIdContext)

	// ExitShowDictionaries is called when exiting the showDictionaries production.
	ExitShowDictionaries(c *ShowDictionariesContext)

	// ExitShowTransaction is called when exiting the showTransaction production.
	ExitShowTransaction(c *ShowTransactionContext)

	// ExitShowReplicaStatus is called when exiting the showReplicaStatus production.
	ExitShowReplicaStatus(c *ShowReplicaStatusContext)

	// ExitShowWorkloadGroups is called when exiting the showWorkloadGroups production.
	ExitShowWorkloadGroups(c *ShowWorkloadGroupsContext)

	// ExitShowCopy is called when exiting the showCopy production.
	ExitShowCopy(c *ShowCopyContext)

	// ExitShowQueryStats is called when exiting the showQueryStats production.
	ExitShowQueryStats(c *ShowQueryStatsContext)

	// ExitShowIndex is called when exiting the showIndex production.
	ExitShowIndex(c *ShowIndexContext)

	// ExitShowWarmUpJob is called when exiting the showWarmUpJob production.
	ExitShowWarmUpJob(c *ShowWarmUpJobContext)

	// ExitSync is called when exiting the sync production.
	ExitSync(c *SyncContext)

	// ExitCreateRoutineLoadAlias is called when exiting the createRoutineLoadAlias production.
	ExitCreateRoutineLoadAlias(c *CreateRoutineLoadAliasContext)

	// ExitShowCreateRoutineLoad is called when exiting the showCreateRoutineLoad production.
	ExitShowCreateRoutineLoad(c *ShowCreateRoutineLoadContext)

	// ExitPauseRoutineLoad is called when exiting the pauseRoutineLoad production.
	ExitPauseRoutineLoad(c *PauseRoutineLoadContext)

	// ExitPauseAllRoutineLoad is called when exiting the pauseAllRoutineLoad production.
	ExitPauseAllRoutineLoad(c *PauseAllRoutineLoadContext)

	// ExitResumeRoutineLoad is called when exiting the resumeRoutineLoad production.
	ExitResumeRoutineLoad(c *ResumeRoutineLoadContext)

	// ExitResumeAllRoutineLoad is called when exiting the resumeAllRoutineLoad production.
	ExitResumeAllRoutineLoad(c *ResumeAllRoutineLoadContext)

	// ExitStopRoutineLoad is called when exiting the stopRoutineLoad production.
	ExitStopRoutineLoad(c *StopRoutineLoadContext)

	// ExitShowRoutineLoad is called when exiting the showRoutineLoad production.
	ExitShowRoutineLoad(c *ShowRoutineLoadContext)

	// ExitShowRoutineLoadTask is called when exiting the showRoutineLoadTask production.
	ExitShowRoutineLoadTask(c *ShowRoutineLoadTaskContext)

	// ExitShowIndexAnalyzer is called when exiting the showIndexAnalyzer production.
	ExitShowIndexAnalyzer(c *ShowIndexAnalyzerContext)

	// ExitShowIndexTokenizer is called when exiting the showIndexTokenizer production.
	ExitShowIndexTokenizer(c *ShowIndexTokenizerContext)

	// ExitShowIndexTokenFilter is called when exiting the showIndexTokenFilter production.
	ExitShowIndexTokenFilter(c *ShowIndexTokenFilterContext)

	// ExitKillConnection is called when exiting the killConnection production.
	ExitKillConnection(c *KillConnectionContext)

	// ExitKillQuery is called when exiting the killQuery production.
	ExitKillQuery(c *KillQueryContext)

	// ExitHelp is called when exiting the help production.
	ExitHelp(c *HelpContext)

	// ExitUnlockTables is called when exiting the unlockTables production.
	ExitUnlockTables(c *UnlockTablesContext)

	// ExitInstallPlugin is called when exiting the installPlugin production.
	ExitInstallPlugin(c *InstallPluginContext)

	// ExitUninstallPlugin is called when exiting the uninstallPlugin production.
	ExitUninstallPlugin(c *UninstallPluginContext)

	// ExitLockTables is called when exiting the lockTables production.
	ExitLockTables(c *LockTablesContext)

	// ExitRestore is called when exiting the restore production.
	ExitRestore(c *RestoreContext)

	// ExitWarmUpCluster is called when exiting the warmUpCluster production.
	ExitWarmUpCluster(c *WarmUpClusterContext)

	// ExitBackup is called when exiting the backup production.
	ExitBackup(c *BackupContext)

	// ExitUnsupportedStartTransaction is called when exiting the unsupportedStartTransaction production.
	ExitUnsupportedStartTransaction(c *UnsupportedStartTransactionContext)

	// ExitWarmUpItem is called when exiting the warmUpItem production.
	ExitWarmUpItem(c *WarmUpItemContext)

	// ExitLockTable is called when exiting the lockTable production.
	ExitLockTable(c *LockTableContext)

	// ExitCreateRoutineLoad is called when exiting the createRoutineLoad production.
	ExitCreateRoutineLoad(c *CreateRoutineLoadContext)

	// ExitMysqlLoad is called when exiting the mysqlLoad production.
	ExitMysqlLoad(c *MysqlLoadContext)

	// ExitShowCreateLoad is called when exiting the showCreateLoad production.
	ExitShowCreateLoad(c *ShowCreateLoadContext)

	// ExitSeparator is called when exiting the separator production.
	ExitSeparator(c *SeparatorContext)

	// ExitImportColumns is called when exiting the importColumns production.
	ExitImportColumns(c *ImportColumnsContext)

	// ExitImportPrecedingFilter is called when exiting the importPrecedingFilter production.
	ExitImportPrecedingFilter(c *ImportPrecedingFilterContext)

	// ExitImportWhere is called when exiting the importWhere production.
	ExitImportWhere(c *ImportWhereContext)

	// ExitImportDeleteOn is called when exiting the importDeleteOn production.
	ExitImportDeleteOn(c *ImportDeleteOnContext)

	// ExitImportSequence is called when exiting the importSequence production.
	ExitImportSequence(c *ImportSequenceContext)

	// ExitImportPartitions is called when exiting the importPartitions production.
	ExitImportPartitions(c *ImportPartitionsContext)

	// ExitImportSequenceStatement is called when exiting the importSequenceStatement production.
	ExitImportSequenceStatement(c *ImportSequenceStatementContext)

	// ExitImportDeleteOnStatement is called when exiting the importDeleteOnStatement production.
	ExitImportDeleteOnStatement(c *ImportDeleteOnStatementContext)

	// ExitImportWhereStatement is called when exiting the importWhereStatement production.
	ExitImportWhereStatement(c *ImportWhereStatementContext)

	// ExitImportPrecedingFilterStatement is called when exiting the importPrecedingFilterStatement production.
	ExitImportPrecedingFilterStatement(c *ImportPrecedingFilterStatementContext)

	// ExitImportColumnsStatement is called when exiting the importColumnsStatement production.
	ExitImportColumnsStatement(c *ImportColumnsStatementContext)

	// ExitImportColumnDesc is called when exiting the importColumnDesc production.
	ExitImportColumnDesc(c *ImportColumnDescContext)

	// ExitRefreshCatalog is called when exiting the refreshCatalog production.
	ExitRefreshCatalog(c *RefreshCatalogContext)

	// ExitRefreshDatabase is called when exiting the refreshDatabase production.
	ExitRefreshDatabase(c *RefreshDatabaseContext)

	// ExitRefreshTable is called when exiting the refreshTable production.
	ExitRefreshTable(c *RefreshTableContext)

	// ExitRefreshDictionary is called when exiting the refreshDictionary production.
	ExitRefreshDictionary(c *RefreshDictionaryContext)

	// ExitRefreshLdap is called when exiting the refreshLdap production.
	ExitRefreshLdap(c *RefreshLdapContext)

	// ExitCleanAllProfile is called when exiting the cleanAllProfile production.
	ExitCleanAllProfile(c *CleanAllProfileContext)

	// ExitCleanLabel is called when exiting the cleanLabel production.
	ExitCleanLabel(c *CleanLabelContext)

	// ExitCleanQueryStats is called when exiting the cleanQueryStats production.
	ExitCleanQueryStats(c *CleanQueryStatsContext)

	// ExitCleanAllQueryStats is called when exiting the cleanAllQueryStats production.
	ExitCleanAllQueryStats(c *CleanAllQueryStatsContext)

	// ExitCancelLoad is called when exiting the cancelLoad production.
	ExitCancelLoad(c *CancelLoadContext)

	// ExitCancelExport is called when exiting the cancelExport production.
	ExitCancelExport(c *CancelExportContext)

	// ExitCancelWarmUpJob is called when exiting the cancelWarmUpJob production.
	ExitCancelWarmUpJob(c *CancelWarmUpJobContext)

	// ExitCancelDecommisionBackend is called when exiting the cancelDecommisionBackend production.
	ExitCancelDecommisionBackend(c *CancelDecommisionBackendContext)

	// ExitCancelBackup is called when exiting the cancelBackup production.
	ExitCancelBackup(c *CancelBackupContext)

	// ExitCancelRestore is called when exiting the cancelRestore production.
	ExitCancelRestore(c *CancelRestoreContext)

	// ExitCancelBuildIndex is called when exiting the cancelBuildIndex production.
	ExitCancelBuildIndex(c *CancelBuildIndexContext)

	// ExitCancelAlterTable is called when exiting the cancelAlterTable production.
	ExitCancelAlterTable(c *CancelAlterTableContext)

	// ExitAdminShowReplicaDistribution is called when exiting the adminShowReplicaDistribution production.
	ExitAdminShowReplicaDistribution(c *AdminShowReplicaDistributionContext)

	// ExitAdminRebalanceDisk is called when exiting the adminRebalanceDisk production.
	ExitAdminRebalanceDisk(c *AdminRebalanceDiskContext)

	// ExitAdminCancelRebalanceDisk is called when exiting the adminCancelRebalanceDisk production.
	ExitAdminCancelRebalanceDisk(c *AdminCancelRebalanceDiskContext)

	// ExitAdminDiagnoseTablet is called when exiting the adminDiagnoseTablet production.
	ExitAdminDiagnoseTablet(c *AdminDiagnoseTabletContext)

	// ExitAdminShowReplicaStatus is called when exiting the adminShowReplicaStatus production.
	ExitAdminShowReplicaStatus(c *AdminShowReplicaStatusContext)

	// ExitAdminCompactTable is called when exiting the adminCompactTable production.
	ExitAdminCompactTable(c *AdminCompactTableContext)

	// ExitAdminCheckTablets is called when exiting the adminCheckTablets production.
	ExitAdminCheckTablets(c *AdminCheckTabletsContext)

	// ExitAdminShowTabletStorageFormat is called when exiting the adminShowTabletStorageFormat production.
	ExitAdminShowTabletStorageFormat(c *AdminShowTabletStorageFormatContext)

	// ExitAdminSetFrontendConfig is called when exiting the adminSetFrontendConfig production.
	ExitAdminSetFrontendConfig(c *AdminSetFrontendConfigContext)

	// ExitAdminCleanTrash is called when exiting the adminCleanTrash production.
	ExitAdminCleanTrash(c *AdminCleanTrashContext)

	// ExitAdminSetReplicaVersion is called when exiting the adminSetReplicaVersion production.
	ExitAdminSetReplicaVersion(c *AdminSetReplicaVersionContext)

	// ExitAdminSetTableStatus is called when exiting the adminSetTableStatus production.
	ExitAdminSetTableStatus(c *AdminSetTableStatusContext)

	// ExitAdminSetReplicaStatus is called when exiting the adminSetReplicaStatus production.
	ExitAdminSetReplicaStatus(c *AdminSetReplicaStatusContext)

	// ExitAdminRepairTable is called when exiting the adminRepairTable production.
	ExitAdminRepairTable(c *AdminRepairTableContext)

	// ExitAdminCancelRepairTable is called when exiting the adminCancelRepairTable production.
	ExitAdminCancelRepairTable(c *AdminCancelRepairTableContext)

	// ExitAdminCopyTablet is called when exiting the adminCopyTablet production.
	ExitAdminCopyTablet(c *AdminCopyTabletContext)

	// ExitRecoverDatabase is called when exiting the recoverDatabase production.
	ExitRecoverDatabase(c *RecoverDatabaseContext)

	// ExitRecoverTable is called when exiting the recoverTable production.
	ExitRecoverTable(c *RecoverTableContext)

	// ExitRecoverPartition is called when exiting the recoverPartition production.
	ExitRecoverPartition(c *RecoverPartitionContext)

	// ExitAdminSetPartitionVersion is called when exiting the adminSetPartitionVersion production.
	ExitAdminSetPartitionVersion(c *AdminSetPartitionVersionContext)

	// ExitBaseTableRef is called when exiting the baseTableRef production.
	ExitBaseTableRef(c *BaseTableRefContext)

	// ExitWildWhere is called when exiting the wildWhere production.
	ExitWildWhere(c *WildWhereContext)

	// ExitTransactionBegin is called when exiting the transactionBegin production.
	ExitTransactionBegin(c *TransactionBeginContext)

	// ExitTranscationCommit is called when exiting the transcationCommit production.
	ExitTranscationCommit(c *TranscationCommitContext)

	// ExitTransactionRollback is called when exiting the transactionRollback production.
	ExitTransactionRollback(c *TransactionRollbackContext)

	// ExitGrantTablePrivilege is called when exiting the grantTablePrivilege production.
	ExitGrantTablePrivilege(c *GrantTablePrivilegeContext)

	// ExitGrantResourcePrivilege is called when exiting the grantResourcePrivilege production.
	ExitGrantResourcePrivilege(c *GrantResourcePrivilegeContext)

	// ExitGrantRole is called when exiting the grantRole production.
	ExitGrantRole(c *GrantRoleContext)

	// ExitRevokeRole is called when exiting the revokeRole production.
	ExitRevokeRole(c *RevokeRoleContext)

	// ExitRevokeResourcePrivilege is called when exiting the revokeResourcePrivilege production.
	ExitRevokeResourcePrivilege(c *RevokeResourcePrivilegeContext)

	// ExitRevokeTablePrivilege is called when exiting the revokeTablePrivilege production.
	ExitRevokeTablePrivilege(c *RevokeTablePrivilegeContext)

	// ExitPrivilege is called when exiting the privilege production.
	ExitPrivilege(c *PrivilegeContext)

	// ExitPrivilegeList is called when exiting the privilegeList production.
	ExitPrivilegeList(c *PrivilegeListContext)

	// ExitAddBackendClause is called when exiting the addBackendClause production.
	ExitAddBackendClause(c *AddBackendClauseContext)

	// ExitDropBackendClause is called when exiting the dropBackendClause production.
	ExitDropBackendClause(c *DropBackendClauseContext)

	// ExitDecommissionBackendClause is called when exiting the decommissionBackendClause production.
	ExitDecommissionBackendClause(c *DecommissionBackendClauseContext)

	// ExitAddObserverClause is called when exiting the addObserverClause production.
	ExitAddObserverClause(c *AddObserverClauseContext)

	// ExitDropObserverClause is called when exiting the dropObserverClause production.
	ExitDropObserverClause(c *DropObserverClauseContext)

	// ExitAddFollowerClause is called when exiting the addFollowerClause production.
	ExitAddFollowerClause(c *AddFollowerClauseContext)

	// ExitDropFollowerClause is called when exiting the dropFollowerClause production.
	ExitDropFollowerClause(c *DropFollowerClauseContext)

	// ExitAddBrokerClause is called when exiting the addBrokerClause production.
	ExitAddBrokerClause(c *AddBrokerClauseContext)

	// ExitDropBrokerClause is called when exiting the dropBrokerClause production.
	ExitDropBrokerClause(c *DropBrokerClauseContext)

	// ExitDropAllBrokerClause is called when exiting the dropAllBrokerClause production.
	ExitDropAllBrokerClause(c *DropAllBrokerClauseContext)

	// ExitAlterLoadErrorUrlClause is called when exiting the alterLoadErrorUrlClause production.
	ExitAlterLoadErrorUrlClause(c *AlterLoadErrorUrlClauseContext)

	// ExitModifyBackendClause is called when exiting the modifyBackendClause production.
	ExitModifyBackendClause(c *ModifyBackendClauseContext)

	// ExitModifyFrontendOrBackendHostNameClause is called when exiting the modifyFrontendOrBackendHostNameClause production.
	ExitModifyFrontendOrBackendHostNameClause(c *ModifyFrontendOrBackendHostNameClauseContext)

	// ExitDropRollupClause is called when exiting the dropRollupClause production.
	ExitDropRollupClause(c *DropRollupClauseContext)

	// ExitAddRollupClause is called when exiting the addRollupClause production.
	ExitAddRollupClause(c *AddRollupClauseContext)

	// ExitAddColumnClause is called when exiting the addColumnClause production.
	ExitAddColumnClause(c *AddColumnClauseContext)

	// ExitAddColumnsClause is called when exiting the addColumnsClause production.
	ExitAddColumnsClause(c *AddColumnsClauseContext)

	// ExitDropColumnClause is called when exiting the dropColumnClause production.
	ExitDropColumnClause(c *DropColumnClauseContext)

	// ExitModifyColumnClause is called when exiting the modifyColumnClause production.
	ExitModifyColumnClause(c *ModifyColumnClauseContext)

	// ExitReorderColumnsClause is called when exiting the reorderColumnsClause production.
	ExitReorderColumnsClause(c *ReorderColumnsClauseContext)

	// ExitAddPartitionClause is called when exiting the addPartitionClause production.
	ExitAddPartitionClause(c *AddPartitionClauseContext)

	// ExitDropPartitionClause is called when exiting the dropPartitionClause production.
	ExitDropPartitionClause(c *DropPartitionClauseContext)

	// ExitModifyPartitionClause is called when exiting the modifyPartitionClause production.
	ExitModifyPartitionClause(c *ModifyPartitionClauseContext)

	// ExitReplacePartitionClause is called when exiting the replacePartitionClause production.
	ExitReplacePartitionClause(c *ReplacePartitionClauseContext)

	// ExitReplaceTableClause is called when exiting the replaceTableClause production.
	ExitReplaceTableClause(c *ReplaceTableClauseContext)

	// ExitRenameClause is called when exiting the renameClause production.
	ExitRenameClause(c *RenameClauseContext)

	// ExitRenameRollupClause is called when exiting the renameRollupClause production.
	ExitRenameRollupClause(c *RenameRollupClauseContext)

	// ExitRenamePartitionClause is called when exiting the renamePartitionClause production.
	ExitRenamePartitionClause(c *RenamePartitionClauseContext)

	// ExitRenameColumnClause is called when exiting the renameColumnClause production.
	ExitRenameColumnClause(c *RenameColumnClauseContext)

	// ExitAddIndexClause is called when exiting the addIndexClause production.
	ExitAddIndexClause(c *AddIndexClauseContext)

	// ExitDropIndexClause is called when exiting the dropIndexClause production.
	ExitDropIndexClause(c *DropIndexClauseContext)

	// ExitEnableFeatureClause is called when exiting the enableFeatureClause production.
	ExitEnableFeatureClause(c *EnableFeatureClauseContext)

	// ExitModifyDistributionClause is called when exiting the modifyDistributionClause production.
	ExitModifyDistributionClause(c *ModifyDistributionClauseContext)

	// ExitModifyTableCommentClause is called when exiting the modifyTableCommentClause production.
	ExitModifyTableCommentClause(c *ModifyTableCommentClauseContext)

	// ExitModifyColumnCommentClause is called when exiting the modifyColumnCommentClause production.
	ExitModifyColumnCommentClause(c *ModifyColumnCommentClauseContext)

	// ExitModifyEngineClause is called when exiting the modifyEngineClause production.
	ExitModifyEngineClause(c *ModifyEngineClauseContext)

	// ExitAlterMultiPartitionClause is called when exiting the alterMultiPartitionClause production.
	ExitAlterMultiPartitionClause(c *AlterMultiPartitionClauseContext)

	// ExitCreateOrReplaceTagClauses is called when exiting the createOrReplaceTagClauses production.
	ExitCreateOrReplaceTagClauses(c *CreateOrReplaceTagClausesContext)

	// ExitCreateOrReplaceBranchClauses is called when exiting the createOrReplaceBranchClauses production.
	ExitCreateOrReplaceBranchClauses(c *CreateOrReplaceBranchClausesContext)

	// ExitDropBranchClauses is called when exiting the dropBranchClauses production.
	ExitDropBranchClauses(c *DropBranchClausesContext)

	// ExitDropTagClauses is called when exiting the dropTagClauses production.
	ExitDropTagClauses(c *DropTagClausesContext)

	// ExitCreateOrReplaceTagClause is called when exiting the createOrReplaceTagClause production.
	ExitCreateOrReplaceTagClause(c *CreateOrReplaceTagClauseContext)

	// ExitCreateOrReplaceBranchClause is called when exiting the createOrReplaceBranchClause production.
	ExitCreateOrReplaceBranchClause(c *CreateOrReplaceBranchClauseContext)

	// ExitTagOptions is called when exiting the tagOptions production.
	ExitTagOptions(c *TagOptionsContext)

	// ExitBranchOptions is called when exiting the branchOptions production.
	ExitBranchOptions(c *BranchOptionsContext)

	// ExitRetainTime is called when exiting the retainTime production.
	ExitRetainTime(c *RetainTimeContext)

	// ExitRetentionSnapshot is called when exiting the retentionSnapshot production.
	ExitRetentionSnapshot(c *RetentionSnapshotContext)

	// ExitMinSnapshotsToKeep is called when exiting the minSnapshotsToKeep production.
	ExitMinSnapshotsToKeep(c *MinSnapshotsToKeepContext)

	// ExitTimeValueWithUnit is called when exiting the timeValueWithUnit production.
	ExitTimeValueWithUnit(c *TimeValueWithUnitContext)

	// ExitDropBranchClause is called when exiting the dropBranchClause production.
	ExitDropBranchClause(c *DropBranchClauseContext)

	// ExitDropTagClause is called when exiting the dropTagClause production.
	ExitDropTagClause(c *DropTagClauseContext)

	// ExitColumnPosition is called when exiting the columnPosition production.
	ExitColumnPosition(c *ColumnPositionContext)

	// ExitToRollup is called when exiting the toRollup production.
	ExitToRollup(c *ToRollupContext)

	// ExitFromRollup is called when exiting the fromRollup production.
	ExitFromRollup(c *FromRollupContext)

	// ExitShowAnalyze is called when exiting the showAnalyze production.
	ExitShowAnalyze(c *ShowAnalyzeContext)

	// ExitShowQueuedAnalyzeJobs is called when exiting the showQueuedAnalyzeJobs production.
	ExitShowQueuedAnalyzeJobs(c *ShowQueuedAnalyzeJobsContext)

	// ExitShowColumnHistogramStats is called when exiting the showColumnHistogramStats production.
	ExitShowColumnHistogramStats(c *ShowColumnHistogramStatsContext)

	// ExitAnalyzeDatabase is called when exiting the analyzeDatabase production.
	ExitAnalyzeDatabase(c *AnalyzeDatabaseContext)

	// ExitAnalyzeTable is called when exiting the analyzeTable production.
	ExitAnalyzeTable(c *AnalyzeTableContext)

	// ExitAlterTableStats is called when exiting the alterTableStats production.
	ExitAlterTableStats(c *AlterTableStatsContext)

	// ExitAlterColumnStats is called when exiting the alterColumnStats production.
	ExitAlterColumnStats(c *AlterColumnStatsContext)

	// ExitShowIndexStats is called when exiting the showIndexStats production.
	ExitShowIndexStats(c *ShowIndexStatsContext)

	// ExitDropStats is called when exiting the dropStats production.
	ExitDropStats(c *DropStatsContext)

	// ExitDropCachedStats is called when exiting the dropCachedStats production.
	ExitDropCachedStats(c *DropCachedStatsContext)

	// ExitDropExpiredStats is called when exiting the dropExpiredStats production.
	ExitDropExpiredStats(c *DropExpiredStatsContext)

	// ExitKillAnalyzeJob is called when exiting the killAnalyzeJob production.
	ExitKillAnalyzeJob(c *KillAnalyzeJobContext)

	// ExitDropAnalyzeJob is called when exiting the dropAnalyzeJob production.
	ExitDropAnalyzeJob(c *DropAnalyzeJobContext)

	// ExitShowTableStats is called when exiting the showTableStats production.
	ExitShowTableStats(c *ShowTableStatsContext)

	// ExitShowColumnStats is called when exiting the showColumnStats production.
	ExitShowColumnStats(c *ShowColumnStatsContext)

	// ExitShowAnalyzeTask is called when exiting the showAnalyzeTask production.
	ExitShowAnalyzeTask(c *ShowAnalyzeTaskContext)

	// ExitAnalyzeProperties is called when exiting the analyzeProperties production.
	ExitAnalyzeProperties(c *AnalyzePropertiesContext)

	// ExitWorkloadPolicyActions is called when exiting the workloadPolicyActions production.
	ExitWorkloadPolicyActions(c *WorkloadPolicyActionsContext)

	// ExitWorkloadPolicyAction is called when exiting the workloadPolicyAction production.
	ExitWorkloadPolicyAction(c *WorkloadPolicyActionContext)

	// ExitWorkloadPolicyConditions is called when exiting the workloadPolicyConditions production.
	ExitWorkloadPolicyConditions(c *WorkloadPolicyConditionsContext)

	// ExitWorkloadPolicyCondition is called when exiting the workloadPolicyCondition production.
	ExitWorkloadPolicyCondition(c *WorkloadPolicyConditionContext)

	// ExitStorageBackend is called when exiting the storageBackend production.
	ExitStorageBackend(c *StorageBackendContext)

	// ExitPasswordOption is called when exiting the passwordOption production.
	ExitPasswordOption(c *PasswordOptionContext)

	// ExitFunctionArguments is called when exiting the functionArguments production.
	ExitFunctionArguments(c *FunctionArgumentsContext)

	// ExitDataTypeList is called when exiting the dataTypeList production.
	ExitDataTypeList(c *DataTypeListContext)

	// ExitSetOptions is called when exiting the setOptions production.
	ExitSetOptions(c *SetOptionsContext)

	// ExitSetDefaultStorageVault is called when exiting the setDefaultStorageVault production.
	ExitSetDefaultStorageVault(c *SetDefaultStorageVaultContext)

	// ExitSetUserProperties is called when exiting the setUserProperties production.
	ExitSetUserProperties(c *SetUserPropertiesContext)

	// ExitSetTransaction is called when exiting the setTransaction production.
	ExitSetTransaction(c *SetTransactionContext)

	// ExitSetVariableWithType is called when exiting the setVariableWithType production.
	ExitSetVariableWithType(c *SetVariableWithTypeContext)

	// ExitSetNames is called when exiting the setNames production.
	ExitSetNames(c *SetNamesContext)

	// ExitSetCharset is called when exiting the setCharset production.
	ExitSetCharset(c *SetCharsetContext)

	// ExitSetCollate is called when exiting the setCollate production.
	ExitSetCollate(c *SetCollateContext)

	// ExitSetPassword is called when exiting the setPassword production.
	ExitSetPassword(c *SetPasswordContext)

	// ExitSetLdapAdminPassword is called when exiting the setLdapAdminPassword production.
	ExitSetLdapAdminPassword(c *SetLdapAdminPasswordContext)

	// ExitSetVariableWithoutType is called when exiting the setVariableWithoutType production.
	ExitSetVariableWithoutType(c *SetVariableWithoutTypeContext)

	// ExitSetSystemVariable is called when exiting the setSystemVariable production.
	ExitSetSystemVariable(c *SetSystemVariableContext)

	// ExitSetUserVariable is called when exiting the setUserVariable production.
	ExitSetUserVariable(c *SetUserVariableContext)

	// ExitTransactionAccessMode is called when exiting the transactionAccessMode production.
	ExitTransactionAccessMode(c *TransactionAccessModeContext)

	// ExitIsolationLevel is called when exiting the isolationLevel production.
	ExitIsolationLevel(c *IsolationLevelContext)

	// ExitSupportedUnsetStatement is called when exiting the supportedUnsetStatement production.
	ExitSupportedUnsetStatement(c *SupportedUnsetStatementContext)

	// ExitSwitchCatalog is called when exiting the switchCatalog production.
	ExitSwitchCatalog(c *SwitchCatalogContext)

	// ExitUseDatabase is called when exiting the useDatabase production.
	ExitUseDatabase(c *UseDatabaseContext)

	// ExitUseCloudCluster is called when exiting the useCloudCluster production.
	ExitUseCloudCluster(c *UseCloudClusterContext)

	// ExitStageAndPattern is called when exiting the stageAndPattern production.
	ExitStageAndPattern(c *StageAndPatternContext)

	// ExitDescribeTableValuedFunction is called when exiting the describeTableValuedFunction production.
	ExitDescribeTableValuedFunction(c *DescribeTableValuedFunctionContext)

	// ExitDescribeTableAll is called when exiting the describeTableAll production.
	ExitDescribeTableAll(c *DescribeTableAllContext)

	// ExitDescribeTable is called when exiting the describeTable production.
	ExitDescribeTable(c *DescribeTableContext)

	// ExitDescribeDictionary is called when exiting the describeDictionary production.
	ExitDescribeDictionary(c *DescribeDictionaryContext)

	// ExitConstraint is called when exiting the constraint production.
	ExitConstraint(c *ConstraintContext)

	// ExitPartitionSpec is called when exiting the partitionSpec production.
	ExitPartitionSpec(c *PartitionSpecContext)

	// ExitPartitionTable is called when exiting the partitionTable production.
	ExitPartitionTable(c *PartitionTableContext)

	// ExitIdentityOrFunctionList is called when exiting the identityOrFunctionList production.
	ExitIdentityOrFunctionList(c *IdentityOrFunctionListContext)

	// ExitIdentityOrFunction is called when exiting the identityOrFunction production.
	ExitIdentityOrFunction(c *IdentityOrFunctionContext)

	// ExitDataDesc is called when exiting the dataDesc production.
	ExitDataDesc(c *DataDescContext)

	// ExitStatementScope is called when exiting the statementScope production.
	ExitStatementScope(c *StatementScopeContext)

	// ExitBuildMode is called when exiting the buildMode production.
	ExitBuildMode(c *BuildModeContext)

	// ExitRefreshTrigger is called when exiting the refreshTrigger production.
	ExitRefreshTrigger(c *RefreshTriggerContext)

	// ExitRefreshSchedule is called when exiting the refreshSchedule production.
	ExitRefreshSchedule(c *RefreshScheduleContext)

	// ExitRefreshMethod is called when exiting the refreshMethod production.
	ExitRefreshMethod(c *RefreshMethodContext)

	// ExitMvPartition is called when exiting the mvPartition production.
	ExitMvPartition(c *MvPartitionContext)

	// ExitIdentifierOrText is called when exiting the identifierOrText production.
	ExitIdentifierOrText(c *IdentifierOrTextContext)

	// ExitIdentifierOrTextOrAsterisk is called when exiting the identifierOrTextOrAsterisk production.
	ExitIdentifierOrTextOrAsterisk(c *IdentifierOrTextOrAsteriskContext)

	// ExitMultipartIdentifierOrAsterisk is called when exiting the multipartIdentifierOrAsterisk production.
	ExitMultipartIdentifierOrAsterisk(c *MultipartIdentifierOrAsteriskContext)

	// ExitIdentifierOrAsterisk is called when exiting the identifierOrAsterisk production.
	ExitIdentifierOrAsterisk(c *IdentifierOrAsteriskContext)

	// ExitUserIdentify is called when exiting the userIdentify production.
	ExitUserIdentify(c *UserIdentifyContext)

	// ExitGrantUserIdentify is called when exiting the grantUserIdentify production.
	ExitGrantUserIdentify(c *GrantUserIdentifyContext)

	// ExitExplain is called when exiting the explain production.
	ExitExplain(c *ExplainContext)

	// ExitExplainCommand is called when exiting the explainCommand production.
	ExitExplainCommand(c *ExplainCommandContext)

	// ExitPlanType is called when exiting the planType production.
	ExitPlanType(c *PlanTypeContext)

	// ExitReplayCommand is called when exiting the replayCommand production.
	ExitReplayCommand(c *ReplayCommandContext)

	// ExitReplayType is called when exiting the replayType production.
	ExitReplayType(c *ReplayTypeContext)

	// ExitMergeType is called when exiting the mergeType production.
	ExitMergeType(c *MergeTypeContext)

	// ExitPreFilterClause is called when exiting the preFilterClause production.
	ExitPreFilterClause(c *PreFilterClauseContext)

	// ExitDeleteOnClause is called when exiting the deleteOnClause production.
	ExitDeleteOnClause(c *DeleteOnClauseContext)

	// ExitSequenceColClause is called when exiting the sequenceColClause production.
	ExitSequenceColClause(c *SequenceColClauseContext)

	// ExitColFromPath is called when exiting the colFromPath production.
	ExitColFromPath(c *ColFromPathContext)

	// ExitColMappingList is called when exiting the colMappingList production.
	ExitColMappingList(c *ColMappingListContext)

	// ExitMappingExpr is called when exiting the mappingExpr production.
	ExitMappingExpr(c *MappingExprContext)

	// ExitWithRemoteStorageSystem is called when exiting the withRemoteStorageSystem production.
	ExitWithRemoteStorageSystem(c *WithRemoteStorageSystemContext)

	// ExitResourceDesc is called when exiting the resourceDesc production.
	ExitResourceDesc(c *ResourceDescContext)

	// ExitMysqlDataDesc is called when exiting the mysqlDataDesc production.
	ExitMysqlDataDesc(c *MysqlDataDescContext)

	// ExitSkipLines is called when exiting the skipLines production.
	ExitSkipLines(c *SkipLinesContext)

	// ExitOutFileClause is called when exiting the outFileClause production.
	ExitOutFileClause(c *OutFileClauseContext)

	// ExitQuery is called when exiting the query production.
	ExitQuery(c *QueryContext)

	// ExitQueryTermDefault is called when exiting the queryTermDefault production.
	ExitQueryTermDefault(c *QueryTermDefaultContext)

	// ExitSetOperation is called when exiting the setOperation production.
	ExitSetOperation(c *SetOperationContext)

	// ExitSetQuantifier is called when exiting the setQuantifier production.
	ExitSetQuantifier(c *SetQuantifierContext)

	// ExitQueryPrimaryDefault is called when exiting the queryPrimaryDefault production.
	ExitQueryPrimaryDefault(c *QueryPrimaryDefaultContext)

	// ExitSubquery is called when exiting the subquery production.
	ExitSubquery(c *SubqueryContext)

	// ExitValuesTable is called when exiting the valuesTable production.
	ExitValuesTable(c *ValuesTableContext)

	// ExitRegularQuerySpecification is called when exiting the regularQuerySpecification production.
	ExitRegularQuerySpecification(c *RegularQuerySpecificationContext)

	// ExitCte is called when exiting the cte production.
	ExitCte(c *CteContext)

	// ExitAliasQuery is called when exiting the aliasQuery production.
	ExitAliasQuery(c *AliasQueryContext)

	// ExitColumnAliases is called when exiting the columnAliases production.
	ExitColumnAliases(c *ColumnAliasesContext)

	// ExitSelectClause is called when exiting the selectClause production.
	ExitSelectClause(c *SelectClauseContext)

	// ExitSelectColumnClause is called when exiting the selectColumnClause production.
	ExitSelectColumnClause(c *SelectColumnClauseContext)

	// ExitWhereClause is called when exiting the whereClause production.
	ExitWhereClause(c *WhereClauseContext)

	// ExitFromClause is called when exiting the fromClause production.
	ExitFromClause(c *FromClauseContext)

	// ExitIntoClause is called when exiting the intoClause production.
	ExitIntoClause(c *IntoClauseContext)

	// ExitBulkCollectClause is called when exiting the bulkCollectClause production.
	ExitBulkCollectClause(c *BulkCollectClauseContext)

	// ExitTableRow is called when exiting the tableRow production.
	ExitTableRow(c *TableRowContext)

	// ExitRelations is called when exiting the relations production.
	ExitRelations(c *RelationsContext)

	// ExitRelation is called when exiting the relation production.
	ExitRelation(c *RelationContext)

	// ExitJoinRelation is called when exiting the joinRelation production.
	ExitJoinRelation(c *JoinRelationContext)

	// ExitBracketDistributeType is called when exiting the bracketDistributeType production.
	ExitBracketDistributeType(c *BracketDistributeTypeContext)

	// ExitCommentDistributeType is called when exiting the commentDistributeType production.
	ExitCommentDistributeType(c *CommentDistributeTypeContext)

	// ExitBracketRelationHint is called when exiting the bracketRelationHint production.
	ExitBracketRelationHint(c *BracketRelationHintContext)

	// ExitCommentRelationHint is called when exiting the commentRelationHint production.
	ExitCommentRelationHint(c *CommentRelationHintContext)

	// ExitAggClause is called when exiting the aggClause production.
	ExitAggClause(c *AggClauseContext)

	// ExitGroupingElement is called when exiting the groupingElement production.
	ExitGroupingElement(c *GroupingElementContext)

	// ExitGroupingSet is called when exiting the groupingSet production.
	ExitGroupingSet(c *GroupingSetContext)

	// ExitHavingClause is called when exiting the havingClause production.
	ExitHavingClause(c *HavingClauseContext)

	// ExitQualifyClause is called when exiting the qualifyClause production.
	ExitQualifyClause(c *QualifyClauseContext)

	// ExitSelectHint is called when exiting the selectHint production.
	ExitSelectHint(c *SelectHintContext)

	// ExitHintStatement is called when exiting the hintStatement production.
	ExitHintStatement(c *HintStatementContext)

	// ExitHintAssignment is called when exiting the hintAssignment production.
	ExitHintAssignment(c *HintAssignmentContext)

	// ExitUpdateAssignment is called when exiting the updateAssignment production.
	ExitUpdateAssignment(c *UpdateAssignmentContext)

	// ExitUpdateAssignmentSeq is called when exiting the updateAssignmentSeq production.
	ExitUpdateAssignmentSeq(c *UpdateAssignmentSeqContext)

	// ExitLateralView is called when exiting the lateralView production.
	ExitLateralView(c *LateralViewContext)

	// ExitQueryOrganization is called when exiting the queryOrganization production.
	ExitQueryOrganization(c *QueryOrganizationContext)

	// ExitSortClause is called when exiting the sortClause production.
	ExitSortClause(c *SortClauseContext)

	// ExitSortItem is called when exiting the sortItem production.
	ExitSortItem(c *SortItemContext)

	// ExitLimitClause is called when exiting the limitClause production.
	ExitLimitClause(c *LimitClauseContext)

	// ExitPartitionClause is called when exiting the partitionClause production.
	ExitPartitionClause(c *PartitionClauseContext)

	// ExitJoinType is called when exiting the joinType production.
	ExitJoinType(c *JoinTypeContext)

	// ExitJoinCriteria is called when exiting the joinCriteria production.
	ExitJoinCriteria(c *JoinCriteriaContext)

	// ExitIdentifierList is called when exiting the identifierList production.
	ExitIdentifierList(c *IdentifierListContext)

	// ExitIdentifierSeq is called when exiting the identifierSeq production.
	ExitIdentifierSeq(c *IdentifierSeqContext)

	// ExitOptScanParams is called when exiting the optScanParams production.
	ExitOptScanParams(c *OptScanParamsContext)

	// ExitTableName is called when exiting the tableName production.
	ExitTableName(c *TableNameContext)

	// ExitAliasedQuery is called when exiting the aliasedQuery production.
	ExitAliasedQuery(c *AliasedQueryContext)

	// ExitTableValuedFunction is called when exiting the tableValuedFunction production.
	ExitTableValuedFunction(c *TableValuedFunctionContext)

	// ExitRelationList is called when exiting the relationList production.
	ExitRelationList(c *RelationListContext)

	// ExitMaterializedViewName is called when exiting the materializedViewName production.
	ExitMaterializedViewName(c *MaterializedViewNameContext)

	// ExitPropertyClause is called when exiting the propertyClause production.
	ExitPropertyClause(c *PropertyClauseContext)

	// ExitPropertyItemList is called when exiting the propertyItemList production.
	ExitPropertyItemList(c *PropertyItemListContext)

	// ExitPropertyItem is called when exiting the propertyItem production.
	ExitPropertyItem(c *PropertyItemContext)

	// ExitPropertyKey is called when exiting the propertyKey production.
	ExitPropertyKey(c *PropertyKeyContext)

	// ExitPropertyValue is called when exiting the propertyValue production.
	ExitPropertyValue(c *PropertyValueContext)

	// ExitTableAlias is called when exiting the tableAlias production.
	ExitTableAlias(c *TableAliasContext)

	// ExitMultipartIdentifier is called when exiting the multipartIdentifier production.
	ExitMultipartIdentifier(c *MultipartIdentifierContext)

	// ExitSimpleColumnDefs is called when exiting the simpleColumnDefs production.
	ExitSimpleColumnDefs(c *SimpleColumnDefsContext)

	// ExitSimpleColumnDef is called when exiting the simpleColumnDef production.
	ExitSimpleColumnDef(c *SimpleColumnDefContext)

	// ExitColumnDefs is called when exiting the columnDefs production.
	ExitColumnDefs(c *ColumnDefsContext)

	// ExitColumnDef is called when exiting the columnDef production.
	ExitColumnDef(c *ColumnDefContext)

	// ExitIndexDefs is called when exiting the indexDefs production.
	ExitIndexDefs(c *IndexDefsContext)

	// ExitIndexDef is called when exiting the indexDef production.
	ExitIndexDef(c *IndexDefContext)

	// ExitPartitionsDef is called when exiting the partitionsDef production.
	ExitPartitionsDef(c *PartitionsDefContext)

	// ExitPartitionDef is called when exiting the partitionDef production.
	ExitPartitionDef(c *PartitionDefContext)

	// ExitLessThanPartitionDef is called when exiting the lessThanPartitionDef production.
	ExitLessThanPartitionDef(c *LessThanPartitionDefContext)

	// ExitFixedPartitionDef is called when exiting the fixedPartitionDef production.
	ExitFixedPartitionDef(c *FixedPartitionDefContext)

	// ExitStepPartitionDef is called when exiting the stepPartitionDef production.
	ExitStepPartitionDef(c *StepPartitionDefContext)

	// ExitInPartitionDef is called when exiting the inPartitionDef production.
	ExitInPartitionDef(c *InPartitionDefContext)

	// ExitPartitionValueList is called when exiting the partitionValueList production.
	ExitPartitionValueList(c *PartitionValueListContext)

	// ExitPartitionValueDef is called when exiting the partitionValueDef production.
	ExitPartitionValueDef(c *PartitionValueDefContext)

	// ExitRollupDefs is called when exiting the rollupDefs production.
	ExitRollupDefs(c *RollupDefsContext)

	// ExitRollupDef is called when exiting the rollupDef production.
	ExitRollupDef(c *RollupDefContext)

	// ExitAggTypeDef is called when exiting the aggTypeDef production.
	ExitAggTypeDef(c *AggTypeDefContext)

	// ExitTabletList is called when exiting the tabletList production.
	ExitTabletList(c *TabletListContext)

	// ExitInlineTable is called when exiting the inlineTable production.
	ExitInlineTable(c *InlineTableContext)

	// ExitNamedExpression is called when exiting the namedExpression production.
	ExitNamedExpression(c *NamedExpressionContext)

	// ExitNamedExpressionSeq is called when exiting the namedExpressionSeq production.
	ExitNamedExpressionSeq(c *NamedExpressionSeqContext)

	// ExitExpression is called when exiting the expression production.
	ExitExpression(c *ExpressionContext)

	// ExitLambdaExpression is called when exiting the lambdaExpression production.
	ExitLambdaExpression(c *LambdaExpressionContext)

	// ExitExist is called when exiting the exist production.
	ExitExist(c *ExistContext)

	// ExitLogicalNot is called when exiting the logicalNot production.
	ExitLogicalNot(c *LogicalNotContext)

	// ExitPredicated is called when exiting the predicated production.
	ExitPredicated(c *PredicatedContext)

	// ExitIsnull is called when exiting the isnull production.
	ExitIsnull(c *IsnullContext)

	// ExitIs_not_null_pred is called when exiting the is_not_null_pred production.
	ExitIs_not_null_pred(c *Is_not_null_predContext)

	// ExitLogicalBinary is called when exiting the logicalBinary production.
	ExitLogicalBinary(c *LogicalBinaryContext)

	// ExitDoublePipes is called when exiting the doublePipes production.
	ExitDoublePipes(c *DoublePipesContext)

	// ExitRowConstructor is called when exiting the rowConstructor production.
	ExitRowConstructor(c *RowConstructorContext)

	// ExitRowConstructorItem is called when exiting the rowConstructorItem production.
	ExitRowConstructorItem(c *RowConstructorItemContext)

	// ExitPredicate is called when exiting the predicate production.
	ExitPredicate(c *PredicateContext)

	// ExitValueExpressionDefault is called when exiting the valueExpressionDefault production.
	ExitValueExpressionDefault(c *ValueExpressionDefaultContext)

	// ExitComparison is called when exiting the comparison production.
	ExitComparison(c *ComparisonContext)

	// ExitArithmeticBinary is called when exiting the arithmeticBinary production.
	ExitArithmeticBinary(c *ArithmeticBinaryContext)

	// ExitArithmeticUnary is called when exiting the arithmeticUnary production.
	ExitArithmeticUnary(c *ArithmeticUnaryContext)

	// ExitDereference is called when exiting the dereference production.
	ExitDereference(c *DereferenceContext)

	// ExitCurrentDate is called when exiting the currentDate production.
	ExitCurrentDate(c *CurrentDateContext)

	// ExitCast is called when exiting the cast production.
	ExitCast(c *CastContext)

	// ExitParenthesizedExpression is called when exiting the parenthesizedExpression production.
	ExitParenthesizedExpression(c *ParenthesizedExpressionContext)

	// ExitUserVariable is called when exiting the userVariable production.
	ExitUserVariable(c *UserVariableContext)

	// ExitElementAt is called when exiting the elementAt production.
	ExitElementAt(c *ElementAtContext)

	// ExitLocalTimestamp is called when exiting the localTimestamp production.
	ExitLocalTimestamp(c *LocalTimestampContext)

	// ExitCharFunction is called when exiting the charFunction production.
	ExitCharFunction(c *CharFunctionContext)

	// ExitIntervalLiteral is called when exiting the intervalLiteral production.
	ExitIntervalLiteral(c *IntervalLiteralContext)

	// ExitSimpleCase is called when exiting the simpleCase production.
	ExitSimpleCase(c *SimpleCaseContext)

	// ExitColumnReference is called when exiting the columnReference production.
	ExitColumnReference(c *ColumnReferenceContext)

	// ExitStar is called when exiting the star production.
	ExitStar(c *StarContext)

	// ExitSessionUser is called when exiting the sessionUser production.
	ExitSessionUser(c *SessionUserContext)

	// ExitConvertType is called when exiting the convertType production.
	ExitConvertType(c *ConvertTypeContext)

	// ExitConvertCharSet is called when exiting the convertCharSet production.
	ExitConvertCharSet(c *ConvertCharSetContext)

	// ExitSubqueryExpression is called when exiting the subqueryExpression production.
	ExitSubqueryExpression(c *SubqueryExpressionContext)

	// ExitEncryptKey is called when exiting the encryptKey production.
	ExitEncryptKey(c *EncryptKeyContext)

	// ExitCurrentTime is called when exiting the currentTime production.
	ExitCurrentTime(c *CurrentTimeContext)

	// ExitLocalTime is called when exiting the localTime production.
	ExitLocalTime(c *LocalTimeContext)

	// ExitSystemVariable is called when exiting the systemVariable production.
	ExitSystemVariable(c *SystemVariableContext)

	// ExitCollate is called when exiting the collate production.
	ExitCollate(c *CollateContext)

	// ExitCurrentUser is called when exiting the currentUser production.
	ExitCurrentUser(c *CurrentUserContext)

	// ExitConstantDefault is called when exiting the constantDefault production.
	ExitConstantDefault(c *ConstantDefaultContext)

	// ExitExtract is called when exiting the extract production.
	ExitExtract(c *ExtractContext)

	// ExitCurrentTimestamp is called when exiting the currentTimestamp production.
	ExitCurrentTimestamp(c *CurrentTimestampContext)

	// ExitFunctionCall is called when exiting the functionCall production.
	ExitFunctionCall(c *FunctionCallContext)

	// ExitArraySlice is called when exiting the arraySlice production.
	ExitArraySlice(c *ArraySliceContext)

	// ExitSearchedCase is called when exiting the searchedCase production.
	ExitSearchedCase(c *SearchedCaseContext)

	// ExitExcept is called when exiting the except production.
	ExitExcept(c *ExceptContext)

	// ExitReplace is called when exiting the replace production.
	ExitReplace(c *ReplaceContext)

	// ExitCastDataType is called when exiting the castDataType production.
	ExitCastDataType(c *CastDataTypeContext)

	// ExitFunctionCallExpression is called when exiting the functionCallExpression production.
	ExitFunctionCallExpression(c *FunctionCallExpressionContext)

	// ExitFunctionIdentifier is called when exiting the functionIdentifier production.
	ExitFunctionIdentifier(c *FunctionIdentifierContext)

	// ExitFunctionNameIdentifier is called when exiting the functionNameIdentifier production.
	ExitFunctionNameIdentifier(c *FunctionNameIdentifierContext)

	// ExitWindowSpec is called when exiting the windowSpec production.
	ExitWindowSpec(c *WindowSpecContext)

	// ExitWindowFrame is called when exiting the windowFrame production.
	ExitWindowFrame(c *WindowFrameContext)

	// ExitFrameUnits is called when exiting the frameUnits production.
	ExitFrameUnits(c *FrameUnitsContext)

	// ExitFrameBoundary is called when exiting the frameBoundary production.
	ExitFrameBoundary(c *FrameBoundaryContext)

	// ExitQualifiedName is called when exiting the qualifiedName production.
	ExitQualifiedName(c *QualifiedNameContext)

	// ExitSpecifiedPartition is called when exiting the specifiedPartition production.
	ExitSpecifiedPartition(c *SpecifiedPartitionContext)

	// ExitNullLiteral is called when exiting the nullLiteral production.
	ExitNullLiteral(c *NullLiteralContext)

	// ExitTypeConstructor is called when exiting the typeConstructor production.
	ExitTypeConstructor(c *TypeConstructorContext)

	// ExitNumericLiteral is called when exiting the numericLiteral production.
	ExitNumericLiteral(c *NumericLiteralContext)

	// ExitBooleanLiteral is called when exiting the booleanLiteral production.
	ExitBooleanLiteral(c *BooleanLiteralContext)

	// ExitStringLiteral is called when exiting the stringLiteral production.
	ExitStringLiteral(c *StringLiteralContext)

	// ExitArrayLiteral is called when exiting the arrayLiteral production.
	ExitArrayLiteral(c *ArrayLiteralContext)

	// ExitMapLiteral is called when exiting the mapLiteral production.
	ExitMapLiteral(c *MapLiteralContext)

	// ExitStructLiteral is called when exiting the structLiteral production.
	ExitStructLiteral(c *StructLiteralContext)

	// ExitPlaceholder is called when exiting the placeholder production.
	ExitPlaceholder(c *PlaceholderContext)

	// ExitComparisonOperator is called when exiting the comparisonOperator production.
	ExitComparisonOperator(c *ComparisonOperatorContext)

	// ExitBooleanValue is called when exiting the booleanValue production.
	ExitBooleanValue(c *BooleanValueContext)

	// ExitWhenClause is called when exiting the whenClause production.
	ExitWhenClause(c *WhenClauseContext)

	// ExitInterval is called when exiting the interval production.
	ExitInterval(c *IntervalContext)

	// ExitUnitIdentifier is called when exiting the unitIdentifier production.
	ExitUnitIdentifier(c *UnitIdentifierContext)

	// ExitDataTypeWithNullable is called when exiting the dataTypeWithNullable production.
	ExitDataTypeWithNullable(c *DataTypeWithNullableContext)

	// ExitComplexDataType is called when exiting the complexDataType production.
	ExitComplexDataType(c *ComplexDataTypeContext)

	// ExitAggStateDataType is called when exiting the aggStateDataType production.
	ExitAggStateDataType(c *AggStateDataTypeContext)

	// ExitPrimitiveDataType is called when exiting the primitiveDataType production.
	ExitPrimitiveDataType(c *PrimitiveDataTypeContext)

	// ExitPrimitiveColType is called when exiting the primitiveColType production.
	ExitPrimitiveColType(c *PrimitiveColTypeContext)

	// ExitComplexColTypeList is called when exiting the complexColTypeList production.
	ExitComplexColTypeList(c *ComplexColTypeListContext)

	// ExitComplexColType is called when exiting the complexColType production.
	ExitComplexColType(c *ComplexColTypeContext)

	// ExitCommentSpec is called when exiting the commentSpec production.
	ExitCommentSpec(c *CommentSpecContext)

	// ExitSample is called when exiting the sample production.
	ExitSample(c *SampleContext)

	// ExitSampleByPercentile is called when exiting the sampleByPercentile production.
	ExitSampleByPercentile(c *SampleByPercentileContext)

	// ExitSampleByRows is called when exiting the sampleByRows production.
	ExitSampleByRows(c *SampleByRowsContext)

	// ExitTableSnapshot is called when exiting the tableSnapshot production.
	ExitTableSnapshot(c *TableSnapshotContext)

	// ExitErrorCapturingIdentifier is called when exiting the errorCapturingIdentifier production.
	ExitErrorCapturingIdentifier(c *ErrorCapturingIdentifierContext)

	// ExitErrorIdent is called when exiting the errorIdent production.
	ExitErrorIdent(c *ErrorIdentContext)

	// ExitRealIdent is called when exiting the realIdent production.
	ExitRealIdent(c *RealIdentContext)

	// ExitIdentifier is called when exiting the identifier production.
	ExitIdentifier(c *IdentifierContext)

	// ExitUnquotedIdentifier is called when exiting the unquotedIdentifier production.
	ExitUnquotedIdentifier(c *UnquotedIdentifierContext)

	// ExitQuotedIdentifierAlternative is called when exiting the quotedIdentifierAlternative production.
	ExitQuotedIdentifierAlternative(c *QuotedIdentifierAlternativeContext)

	// ExitQuotedIdentifier is called when exiting the quotedIdentifier production.
	ExitQuotedIdentifier(c *QuotedIdentifierContext)

	// ExitIntegerLiteral is called when exiting the integerLiteral production.
	ExitIntegerLiteral(c *IntegerLiteralContext)

	// ExitDecimalLiteral is called when exiting the decimalLiteral production.
	ExitDecimalLiteral(c *DecimalLiteralContext)

	// ExitNonReserved is called when exiting the nonReserved production.
	ExitNonReserved(c *NonReservedContext)
}
