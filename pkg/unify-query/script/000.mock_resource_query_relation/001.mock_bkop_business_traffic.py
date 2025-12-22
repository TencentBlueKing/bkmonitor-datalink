#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
Mock BKOP Business 2 Traffic to SurrealDB with Metrics

This script generates mock resource association data for BKOP Business 2,
including static relations and dynamic traffic with metrics (flow_total, flow_seconds, flow_error).

This script is idempotent - it can be run multiple times without causing data conflicts.
It uses UPSERT with MERGE to ensure that created_at timestamps are preserved across runs.

Storage Backends:
    - native: Direct connection to SurrealDB via HTTP REST API
    - bkbase: Access SurrealDB through BKBase unified query API

Usage:
    # Use native SurrealDB (default)
    python 001.mock_bkop_business_traffic.py --backend native
    
    # Use BKBase SurrealDB
    python 001.mock_bkop_business_traffic.py --backend bkbase
    
    # Enable debug logging
    python 001.mock_bkop_business_traffic.py --backend native --debug

Configuration:
    All configuration is managed through environment variables.
    Copy .env.example to .env and customize for your environment.
    
    Required for native backend:
        SURREAL_URL, SURREAL_USER, SURREAL_PASS, SURREAL_NS, SURREAL_DB
    
    Required for bkbase backend:
        BKBASE_API_URL, BKBASE_APP_CODE, BKBASE_APP_SECRET, BKBASE_RESULT_TABLE_ID
"""

import abc
import argparse
import json
import logging
import os
import random
from datetime import datetime, timedelta
from enum import Enum
from typing import Dict, List, Any, Optional, Tuple

import requests

# ============================================================================
# Logging Configuration (Early initialization)
# ============================================================================

logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(levelname)s - %(message)s',
    datefmt='%Y-%m-%d %H:%M:%S'
)
logger = logging.getLogger(__name__)

# ============================================================================
# Smart Configuration Loading with Priority
# ============================================================================
# Configuration priority (highest to lowest):
# 1. Environment variables (for config values)
# 2. .env.{backend}.local (local development, not in git)
# 3. .env.{backend}       (backend defaults)
# 4. .env                 (base defaults)
#
# Strategy: Load in reverse priority order, so higher priority overrides lower
try:
    from dotenv import load_dotenv
    import os as _os
    import sys as _sys
    
    # Determine backend: Command line > Environment > Default
    storage_backend = None
    
    # 1. Check command line arguments first (highest priority for backend selection)
    for i, arg in enumerate(_sys.argv):
        if arg == '--backend' and i + 1 < len(_sys.argv):
            storage_backend = _sys.argv[i + 1]
            break
    
    # 2. If not in command line, check environment variable
    if not storage_backend:
        storage_backend = _os.getenv('STORAGE_BACKEND')
    
    # 3. Default to native if still not set
    if not storage_backend:
        storage_backend = 'native'
    
    logger.debug(f"Loading configurations for backend: {storage_backend}")
    
    # Load configurations with proper priority handling
    # Lower priority files are loaded first with override=False
    # Higher priority files are loaded later with override=True
    
    # Priority 4 (lowest): Base defaults - never override
    if _os.path.exists('.env'):
        load_dotenv('.env', override=False)
        logger.debug("✓ Loaded: .env (base defaults)")
    
    # Priority 3: Backend-specific defaults - can override base
    backend_env = f'.env.{storage_backend}'
    if _os.path.exists(backend_env):
        load_dotenv(backend_env, override=True)
        logger.debug(f"✓ Loaded: {backend_env} (backend defaults)")
    
    # Priority 2 (highest in files): Local development overrides - overrides everything from files
    backend_local_env = f'.env.{storage_backend}.local'
    if _os.path.exists(backend_local_env):
        load_dotenv(backend_local_env, override=True)
        logger.debug(f"✓ Loaded: {backend_local_env} (local overrides)")
    
    # Priority 1 (highest overall): Environment variables were set before script started
    # They are not affected by load_dotenv at all
        
except ImportError:
    logger.debug("python-dotenv not installed, using environment variables only")
    pass  # dotenv is optional


# ============================================================================
# Configuration
# ============================================================================

class StorageBackend(Enum):
    """Storage backend enumeration"""
    NATIVE = "native"
    BKBASE = "bkbase"


class SurrealDBConfig:
    """Native SurrealDB connection configuration"""
    URL = os.getenv("SURREAL_URL", "http://localhost:8000")
    USERNAME = os.getenv("SURREAL_USER", "root")
    PASSWORD = os.getenv("SURREAL_PASS", "root")
    NAMESPACE = os.getenv("SURREAL_NS", "test")
    DATABASE = os.getenv("SURREAL_DB", "test")


class BKBaseConfig:
    """BKBase API configuration (no default values for security)"""
    API_URL = os.getenv("BKBASE_API_URL", "")
    USERNAME = os.getenv("BKBASE_USERNAME", "")
    APP_CODE = os.getenv("BKBASE_APP_CODE", "")
    APP_SECRET = os.getenv("BKBASE_APP_SECRET", "")
    RESULT_TABLE_ID = os.getenv("BKBASE_RESULT_TABLE_ID", "")
    AUTH_METHOD = os.getenv("BKBASE_AUTH_METHOD", "user")
    PREFER_STORAGE = os.getenv("BKBASE_PREFER_STORAGE", "surrealdb")


class MockConfig:
    """Mock data generation configuration for BKOP Business 2"""

    # Business specific
    BIZ_ID = os.getenv("BIZ_ID", "2")
    BIZ_NAME = os.getenv("BIZ_NAME", "bkop")
    CLUSTER_ID = os.getenv("CLUSTER_ID", "BCS-K8S-00002")
    NAMESPACE = os.getenv("NAMESPACE", "bkop")

    # Result table ID for metrics (业务2的结果表)
    RESULT_TABLE_ID = os.getenv("RESULT_TABLE_ID", "2_bkmonitor_bkop_2")

    SERVICE_LIST = ["api", "web", "worker"]

    # Number of resources to generate
    NUM_PODS = int(os.getenv("NUM_PODS", "10"))
    NUM_DEPLOYMENTS = int(os.getenv("NUM_DEPLOYMENTS", "3"))
    NUM_NODES = int(os.getenv("NUM_NODES", "3"))

    # Traffic generation
    POD_TO_POD_TRAFFIC_PROBABILITY = float(os.getenv("POD_TO_POD_TRAFFIC_PROBABILITY", "0.4"))

    # Metric value ranges
    FLOW_TOTAL_RANGE = (
        int(os.getenv("FLOW_TOTAL_MIN", "10")),
        int(os.getenv("FLOW_TOTAL_MAX", "1000"))
    )
    FLOW_SECONDS_RANGE = (
        float(os.getenv("FLOW_SECONDS_MIN", "0.01")),
        float(os.getenv("FLOW_SECONDS_MAX", "2.0"))
    )
    FLOW_ERROR_RATE_RANGE = (
        float(os.getenv("FLOW_ERROR_RATE_MIN", "0.0")),
        float(os.getenv("FLOW_ERROR_RATE_MAX", "0.1"))
    )

    # 默认回转时间
    DEFAULT_TIME_BACK_HOURS = int(os.getenv("DEFAULT_TIME_BACK_HOURS", "1"))

    # Time range for mock data
    START_TIME = datetime.now().replace(tzinfo=None) - timedelta(hours=DEFAULT_TIME_BACK_HOURS)
    END_TIME = datetime.now().replace(tzinfo=None)
    METRIC_TIME_POINTS = int(os.getenv("METRIC_TIME_POINTS", "12"))


# ============================================================================
# Enums
# ============================================================================

class ResourceType(Enum):
    """Resource type enumeration"""
    # Kubernetes resources
    POD = "pod"
    NODE = "node"
    SERVICE = "service"
    DEPLOYMENT = "deployment"
    REPLICASET = "replicaset"
    NAMESPACE = "namespace"
    CLUSTER = "cluster"

    # CMDB resources
    BIZ = "biz"

    # Metric
    METRIC = "metric"


class RelationType(Enum):
    """Relation type enumeration"""
    # Static relations
    NODE_WITH_POD = "node_with_pod"
    POD_WITH_SERVICE = "pod_with_service"
    DEPLOYMENT_WITH_REPLICASET = "deployment_with_replicaset"
    POD_WITH_REPLICASET = "pod_with_replicaset"

    # Dynamic relations
    POD_TO_POD = "pod_to_pod"

    # Metric relations
    RELATION_HAS_METRIC = "relation_has_metric"


class MetricType(Enum):
    """Metric type enumeration"""
    COUNTER = "counter"
    GAUGE = "gauge"
    HISTOGRAM = "histogram"


# ============================================================================
# Resource Index Fields Definition
# ============================================================================

class ResourceIndexFields:
    """Resource index fields definition"""

    FIELDS = {
        ResourceType.POD: ["bcs_cluster_id", "namespace", "pod"],
        ResourceType.NODE: ["bcs_cluster_id", "node"],
        ResourceType.SERVICE: ["bcs_cluster_id", "namespace", "service"],
        ResourceType.DEPLOYMENT: ["bcs_cluster_id", "namespace", "deployment"],
        ResourceType.REPLICASET: ["bcs_cluster_id", "namespace", "replicaset"],
        ResourceType.NAMESPACE: ["bcs_cluster_id", "namespace"],
        ResourceType.CLUSTER: ["bcs_cluster_id"],
        ResourceType.BIZ: ["bk_biz_id"],
        ResourceType.METRIC: ["metric_name"],
    }

    @classmethod
    def get_fields(cls, resource_type: ResourceType) -> List[str]:
        """Get index fields for resource type"""
        return cls.FIELDS.get(resource_type, [])


# ============================================================================
# ID Generation Utilities
# ============================================================================

class IDGenerator:
    """ID generator following documentation rules"""

    @staticmethod
    def generate_node_id(resource_type: ResourceType, data: Dict[str, Any]) -> str:
        """Generate node ID"""
        index_fields = ResourceIndexFields.get_fields(resource_type)
        sorted_keys = sorted(index_fields)
        pairs = [f"{key}={data.get(key, '')}" for key in sorted_keys]
        return f"{resource_type.value}:{','.join(pairs)}"

    @staticmethod
    def generate_directional_relation_id(
            relation_type: RelationType,
            source_type: ResourceType,
            source_data: Dict[str, Any],
            target_type: ResourceType,
            target_data: Dict[str, Any]
    ) -> str:
        """Generate directional relation ID"""
        source_fields = ResourceIndexFields.get_fields(source_type)
        target_fields = ResourceIndexFields.get_fields(target_type)

        sorted_source_keys = sorted(source_fields)
        sorted_target_keys = sorted(target_fields)

        source_pairs = [f"{key}={source_data.get(key, '')}" for key in sorted_source_keys]
        target_pairs = [f"{key}={target_data.get(key, '')}" for key in sorted_target_keys]

        source_part = ','.join(source_pairs)
        target_part = ','.join(target_pairs)

        return f"{relation_type.value}:{source_part}|{target_part}"


# ============================================================================
# Storage Client Abstract Interface
# ============================================================================

class StorageClient(abc.ABC):
    """Abstract storage client interface"""

    @abc.abstractmethod
    def execute_sql(self, sql: str) -> List[Dict[str, Any]]:
        """Execute SQL query and return results"""
        pass

    @abc.abstractmethod
    def format_datetime(self, dt: datetime) -> str:
        """Format datetime for storage backend"""
        pass

    @abc.abstractmethod
    def batch_upsert_nodes(
            self,
            resource_type: ResourceType,
            nodes: List[Dict[str, Any]],
            created_at: datetime,
            updated_at: datetime
    ) -> Dict[str, Any]:
        """Batch upsert nodes"""
        pass

    @abc.abstractmethod
    def upsert_node(
            self,
            resource_type: ResourceType,
            data: Dict[str, Any],
            created_at: datetime,
            updated_at: datetime
    ) -> Dict[str, Any]:
        """Upsert a single node"""
        pass

    @abc.abstractmethod
    def upsert_relation(
            self,
            relation_type: RelationType,
            from_resource_type: ResourceType,
            from_data: Dict[str, Any],
            to_resource_type: ResourceType,
            to_data: Dict[str, Any],
            created_at: datetime,
            updated_at: datetime,
            extra_fields: Optional[Dict[str, Any]] = None
    ) -> Dict[str, Any]:
        """Upsert a relation"""
        pass


# ============================================================================
# SurrealDB Client with Batch Support
# ============================================================================

class SurrealDBClient(StorageClient):
    """SurrealDB HTTP REST API client with batch insert support"""

    def __init__(
            self,
            url: str = SurrealDBConfig.URL,
            username: str = SurrealDBConfig.USERNAME,
            password: str = SurrealDBConfig.PASSWORD,
            namespace: str = SurrealDBConfig.NAMESPACE,
            database: str = SurrealDBConfig.DATABASE
    ):
        self.url = url
        self.username = username
        self.password = password
        self.namespace = namespace
        self.database = database
        self.session = requests.Session()
        logger.info(f"SurrealDB client initialized: {url}/{namespace}/{database}")

    def execute_sql(self, sql: str) -> List[Dict[str, Any]]:
        """Execute SQL query via HTTP REST API"""
        # Prepend USE statement
        full_sql = f"USE NS {self.namespace} DB {self.database}; {sql}"

        response = self.session.post(
            f"{self.url}/sql",
            headers={
                'Content-Type': 'text/plain; charset=utf-8',
                'Accept': 'application/json'
            },
            auth=(self.username, self.password),
            data=full_sql.encode('utf-8')
        )

        if response.status_code != 200:
            raise Exception(f"HTTP error {response.status_code}: {response.text}")

        results = response.json()

        # Check for SQL errors (skip the first result which is the USE statement)
        for i, result in enumerate(results):
            if result.get('status') == 'ERR':
                error_detail = result.get('detail') or result.get('result', 'Unknown error')
                raise Exception(f"SQL error in statement {i}: {error_detail}")

        return results[1:] if len(results) > 1 else results

    def format_datetime(self, dt: datetime) -> str:
        """Format datetime for SurrealDB"""
        return dt.strftime('%Y-%m-%dT%H:%M:%SZ')

    def batch_upsert_nodes(
            self,
            resource_type: ResourceType,
            nodes: List[Dict[str, Any]],
            created_at: datetime,
            updated_at: datetime
    ) -> Dict[str, Any]:
        """Batch upsert nodes ensuring idempotency (protects created_at)"""
        if not nodes:
            return {}

        logger.debug(f"Batch upserting {len(nodes)} {resource_type.value} nodes")

        # Build batch upsert SQL
        upsert_statements = []
        for data in nodes:
            node_id = IDGenerator.generate_node_id(resource_type, data)
            
            # Build SET clause with all fields + timestamp logic
            set_parts = []
            for key, value in data.items():
                if isinstance(value, (int, float)):
                    set_parts.append(f"{key} = {value}")
                else:
                    set_parts.append(f"{key} = '{value}'")
            
            # Add timestamp fields with idempotent logic
            set_parts.append(f"created_at = created_at OR type::datetime('{self.format_datetime(created_at)}')")
            set_parts.append(f"updated_at = type::datetime('{self.format_datetime(updated_at)}')")
            
            set_clause = ',\n                '.join(set_parts)
            
            upsert_statements.append(f"""
            UPSERT {resource_type.value}:`{node_id}` SET
                {set_clause};
            """)

        # Execute in transaction
        sql = "BEGIN TRANSACTION;\n" + "\n".join(upsert_statements) + "\nCOMMIT TRANSACTION;"
        results = self.execute_sql(sql)
        logger.info(f"✓ Batch upserted {len(nodes)} {resource_type.value} nodes")
        return results

    def upsert_node(
            self,
            resource_type: ResourceType,
            data: Dict[str, Any],
            created_at: datetime,
            updated_at: datetime
    ) -> Dict[str, Any]:
        """Upsert a single node ensuring idempotency (protects created_at)"""
        node_id = IDGenerator.generate_node_id(resource_type, data)
        
        # Build SET clause with all fields + timestamp logic
        set_parts = []
        for key, value in data.items():
            if isinstance(value, (int, float)):
                set_parts.append(f"{key} = {value}")
            else:
                set_parts.append(f"{key} = '{value}'")
        
        # Add timestamp fields with idempotent logic
        set_parts.append(f"created_at = created_at OR type::datetime('{self.format_datetime(created_at)}')")
        set_parts.append(f"updated_at = type::datetime('{self.format_datetime(updated_at)}')")
        
        set_clause = ',\n            '.join(set_parts)
        
        sql = f"""
        UPSERT {resource_type.value}:`{node_id}` SET
            {set_clause};
        """

        result = self.execute_sql(sql)
        return result[0].get('result', [])

    def upsert_relation(
            self,
            relation_type: RelationType,
            from_resource_type: ResourceType,
            from_data: Dict[str, Any],
            to_resource_type: ResourceType,
            to_data: Dict[str, Any],
            created_at: datetime,
            updated_at: datetime,
            extra_fields: Optional[Dict[str, Any]] = None
    ) -> Dict[str, Any]:
        """Upsert a relation with idempotency (protects created_at)"""
        from_id = IDGenerator.generate_node_id(from_resource_type, from_data)
        to_id = IDGenerator.generate_node_id(to_resource_type, to_data)

        # Build extra fields
        extra_parts = []
        if extra_fields:
            for key, value in extra_fields.items():
                if isinstance(value, (int, float)):
                    extra_parts.append(f"{key} = {value}")
                else:
                    extra_parts.append(f"{key} = '{value}'")
        
        extra_str = ',\n            ' + ',\n            '.join(extra_parts) if extra_parts else ''

        # Use OR to protect created_at from being overwritten
        sql = f"""
        RELATE {from_resource_type.value}:`{from_id}`->{relation_type.value}->{to_resource_type.value}:`{to_id}` SET
            created_at = created_at OR type::datetime('{self.format_datetime(created_at)}'),
            updated_at = type::datetime('{self.format_datetime(updated_at)}'){extra_str};
        """

        result = self.execute_sql(sql)
        return result[0].get('result', [])


# ============================================================================
# BKBase SurrealDB Client
# ============================================================================

class BKBaseClient(StorageClient):
    """BKBase API client for SurrealDB access"""

    def __init__(
            self,
            api_url: str = BKBaseConfig.API_URL,
            username: str = BKBaseConfig.USERNAME,
            app_code: str = BKBaseConfig.APP_CODE,
            app_secret: str = BKBaseConfig.APP_SECRET,
            result_table_id: str = BKBaseConfig.RESULT_TABLE_ID,
            auth_method: str = BKBaseConfig.AUTH_METHOD,
            prefer_storage: str = BKBaseConfig.PREFER_STORAGE
    ):
        # Validate required configuration
        if not api_url:
            raise ValueError("BKBASE_API_URL is required for BKBase backend")
        if not app_secret:
            raise ValueError("BKBASE_APP_SECRET is required for BKBase backend")
        if not result_table_id:
            raise ValueError("BKBASE_RESULT_TABLE_ID is required for BKBase backend")

        self.api_url = api_url
        self.username = username
        self.app_code = app_code
        self.app_secret = app_secret
        self.result_table_id = result_table_id
        self.auth_method = auth_method
        self.prefer_storage = prefer_storage
        self.session = requests.Session()
        logger.info(f"BKBase client initialized: {api_url}")
        logger.info(f"  Result Table ID: {result_table_id}")
        logger.info(f"  Prefer Storage: {prefer_storage}")

    def execute_sql(self, sql: str) -> List[Dict[str, Any]]:
        """Execute SQL query via BKBase API"""
        # Build BKBase request payload
        payload = {
            "sql": json.dumps({
                "dsl": sql,
                "result_table_id": self.result_table_id
            }),
            "bkdata_authentication_method": self.auth_method,
            "prefer_storage": self.prefer_storage,
            "bk_username": self.username,
            "bk_app_code": self.app_code,
            "bk_app_secret": self.app_secret
        }

        logger.debug(f"Executing BKBase query: {sql[:100]}...")

        response = self.session.post(
            self.api_url,
            headers={'Content-Type': 'application/json'},
            json=payload,
            timeout=60
        )

        if response.status_code != 200:
            raise Exception(f"BKBase API error {response.status_code}: {response.text}")

        result = response.json()

        # Check BKBase API response
        if not result.get('result', False):
            error_msg = result.get('message') or result.get('errors') or 'Unknown error'
            raise Exception(f"BKBase query failed: {error_msg}")

        # Extract data from BKBase response
        data = result.get('data', {})
        records = data.get('list', [])

        # Convert to SurrealDB-like result format
        return [{'result': records}]

    def format_datetime(self, dt: datetime) -> str:
        """Format datetime for SurrealDB"""
        return dt.strftime('%Y-%m-%dT%H:%M:%SZ')

    def batch_upsert_nodes(
            self,
            resource_type: ResourceType,
            nodes: List[Dict[str, Any]],
            created_at: datetime,
            updated_at: datetime
    ) -> Dict[str, Any]:
        """Batch upsert nodes via BKBase"""
        if not nodes:
            return {}

        logger.debug(f"Batch upserting {len(nodes)} {resource_type.value} nodes via BKBase")

        # Build batch upsert SQL
        upsert_statements = []
        for data in nodes:
            node_id = IDGenerator.generate_node_id(resource_type, data)
            
            # Build SET clause with all fields + timestamp logic
            set_parts = []
            for key, value in data.items():
                if isinstance(value, (int, float)):
                    set_parts.append(f"{key} = {value}")
                else:
                    set_parts.append(f"{key} = '{value}'")
            
            # Add timestamp fields with idempotent logic
            set_parts.append(f"created_at = created_at OR type::datetime('{self.format_datetime(created_at)}')")
            set_parts.append(f"updated_at = type::datetime('{self.format_datetime(updated_at)}')")
            
            set_clause = ',\n                '.join(set_parts)
            
            upsert_statements.append(f"""
            UPSERT {resource_type.value}:`{node_id}` SET
                {set_clause};
            """)

        # Execute in transaction
        sql = "BEGIN TRANSACTION;\n" + "\n".join(upsert_statements) + "\nCOMMIT TRANSACTION;"
        results = self.execute_sql(sql)
        logger.info(f"✓ Batch upserted {len(nodes)} {resource_type.value} nodes via BKBase")
        return results

    def upsert_node(
            self,
            resource_type: ResourceType,
            data: Dict[str, Any],
            created_at: datetime,
            updated_at: datetime
    ) -> Dict[str, Any]:
        """Upsert a single node via BKBase"""
        node_id = IDGenerator.generate_node_id(resource_type, data)
        
        # Build SET clause with all fields + timestamp logic
        set_parts = []
        for key, value in data.items():
            if isinstance(value, (int, float)):
                set_parts.append(f"{key} = {value}")
            else:
                set_parts.append(f"{key} = '{value}'")
        
        # Add timestamp fields with idempotent logic
        set_parts.append(f"created_at = created_at OR type::datetime('{self.format_datetime(created_at)}')")
        set_parts.append(f"updated_at = type::datetime('{self.format_datetime(updated_at)}')")
        
        set_clause = ',\n            '.join(set_parts)
        
        sql = f"""
        UPSERT {resource_type.value}:`{node_id}` SET
            {set_clause};
        """

        result = self.execute_sql(sql)
        return result[0].get('result', [])

    def upsert_relation(
            self,
            relation_type: RelationType,
            from_resource_type: ResourceType,
            from_data: Dict[str, Any],
            to_resource_type: ResourceType,
            to_data: Dict[str, Any],
            created_at: datetime,
            updated_at: datetime,
            extra_fields: Optional[Dict[str, Any]] = None
    ) -> Dict[str, Any]:
        """Upsert a relation via BKBase"""
        from_id = IDGenerator.generate_node_id(from_resource_type, from_data)
        to_id = IDGenerator.generate_node_id(to_resource_type, to_data)

        # Build extra fields
        extra_parts = []
        if extra_fields:
            for key, value in extra_fields.items():
                if isinstance(value, (int, float)):
                    extra_parts.append(f"{key} = {value}")
                else:
                    extra_parts.append(f"{key} = '{value}'")
        
        extra_str = ',\n            ' + ',\n            '.join(extra_parts) if extra_parts else ''

        # Use OR to protect created_at from being overwritten
        sql = f"""
        RELATE {from_resource_type.value}:`{from_id}`->{relation_type.value}->{to_resource_type.value}:`{to_id}` SET
            created_at = created_at OR type::datetime('{self.format_datetime(created_at)}'),
            updated_at = type::datetime('{self.format_datetime(updated_at)}'){extra_str};
        """

        result = self.execute_sql(sql)
        return result[0].get('result', [])


# ============================================================================
# Mock Data Generator
# ============================================================================

class MockGenerator:
    """Generate mock data"""

    def __init__(self, client: StorageClient):
        self.client = client
        self.resources: Dict[ResourceType, List[Dict[str, Any]]] = {}
        self.current_time = MockConfig.END_TIME
        self.traffic_relations: List[Tuple[Dict, Dict]] = []  # Store (source_pod, target_pod) pairs

    def random_time_in_range(self) -> datetime:
        """Generate random time within configured range"""
        delta = MockConfig.END_TIME - MockConfig.START_TIME
        random_seconds = random.randint(0, int(delta.total_seconds()))
        return MockConfig.START_TIME + timedelta(seconds=random_seconds)

    def create_biz(self):
        """Create business node"""
        logger.info("Creating...")

        data = {"bk_biz_id": MockConfig.BIZ_ID}
        created_at = self.random_time_in_range()
        updated_at = self.current_time

        self.client.upsert_node(
            ResourceType.BIZ,
            data,
            created_at,
            updated_at
        )

        self.resources[ResourceType.BIZ] = [data]
        logger.info(f"✓ Created biz: {MockConfig.BIZ_NAME} (id={MockConfig.BIZ_ID})")

    def create_cluster(self):
        """Create cluster node"""
        logger.info("Creating cluster...")

        data = {"bcs_cluster_id": MockConfig.CLUSTER_ID}
        created_at = self.random_time_in_range()
        updated_at = self.current_time

        self.client.upsert_node(
            ResourceType.CLUSTER,
            data,
            created_at,
            updated_at
        )

        self.resources[ResourceType.CLUSTER] = [data]
        logger.info(f"✓ Created cluster: {MockConfig.CLUSTER_ID}")

    def create_namespace(self):
        """Create namespace node"""
        logger.info("Creating namespace...")

        data = {
            "bcs_cluster_id": MockConfig.CLUSTER_ID,
            "namespace": MockConfig.NAMESPACE
        }
        created_at = self.random_time_in_range()
        updated_at = self.current_time

        self.client.upsert_node(
            ResourceType.NAMESPACE,
            data,
            created_at,
            updated_at
        )

        self.resources[ResourceType.NAMESPACE] = [data]
        logger.info(f"✓ Created namespace: {MockConfig.NAMESPACE}")

    def create_nodes(self):
        """Create node resources (batch)"""
        logger.info(f"Creating {MockConfig.NUM_NODES} nodes...")

        nodes = []
        for i in range(MockConfig.NUM_NODES):
            nodes.append({
                "bcs_cluster_id": MockConfig.CLUSTER_ID,
                "node": f"{MockConfig.BIZ_NAME}-node-{i}"
            })

        created_at = self.random_time_in_range()
        updated_at = self.current_time

        self.client.batch_upsert_nodes(
            ResourceType.NODE,
            nodes,
            created_at,
            updated_at
        )

        self.resources[ResourceType.NODE] = nodes

    def create_pods(self):
        """Create pod resources (batch)"""
        logger.info(f"Creating {MockConfig.NUM_PODS} pods...")

        pods = []
        for i in range(MockConfig.NUM_PODS):
            pods.append({
                "bcs_cluster_id": MockConfig.CLUSTER_ID,
                "namespace": MockConfig.NAMESPACE,
                "pod": f"{MockConfig.BIZ_NAME}-pod-{i:03d}"
            })

        created_at = self.random_time_in_range()
        updated_at = self.current_time

        self.client.batch_upsert_nodes(
            ResourceType.POD,
            pods,
            created_at,
            updated_at
        )

        self.resources[ResourceType.POD] = pods

    def create_services(self):
        """Create service resources (batch)"""
        logger.info(f"Creating {len(MockConfig.SERVICE_LIST)} services...")

        services = []
        for svc_name in MockConfig.SERVICE_LIST:
            services.append({
                "bcs_cluster_id": MockConfig.CLUSTER_ID,
                "namespace": MockConfig.NAMESPACE,
                "service": f"{MockConfig.BIZ_NAME}-{svc_name}"
            })

        created_at = self.random_time_in_range()
        updated_at = self.current_time

        self.client.batch_upsert_nodes(
            ResourceType.SERVICE,
            services,
            created_at,
            updated_at
        )

        self.resources[ResourceType.SERVICE] = services

    def create_deployments(self):
        """Create deployment resources (batch)"""
        logger.info(f"Creating {MockConfig.NUM_DEPLOYMENTS} deployments...")

        deployments = []
        deployment_names = MockConfig.SERVICE_LIST
        for i in range(MockConfig.NUM_DEPLOYMENTS):
            deployments.append({
                "bcs_cluster_id": MockConfig.CLUSTER_ID,
                "namespace": MockConfig.NAMESPACE,
                "deployment": f"{MockConfig.BIZ_NAME}-{deployment_names[i]}-deploy"
            })

        created_at = self.random_time_in_range()
        updated_at = self.current_time

        self.client.batch_upsert_nodes(
            ResourceType.DEPLOYMENT,
            deployments,
            created_at,
            updated_at
        )

        self.resources[ResourceType.DEPLOYMENT] = deployments

    def create_static_relations(self):
        """Create static relations"""
        logger.info("Creating static relations...")
        
        # 1. Biz and Cluster relations (CMDB layer)
        self._create_biz_cluster_relations()
        
        # 2. Deployment -> ReplicaSet -> Pod chain
        self._create_deployment_chain_relations()
        
        # 3. Node with Pod
        self._create_node_with_pod_relations()
        
        # 4. Pod with Service
        self._create_pod_with_service_relations()

    def _create_node_with_pod_relations(self):
        """Create node_with_pod relations"""
        logger.info("Creating node_with_pod relations...")

        nodes = self.resources.get(ResourceType.NODE, [])
        pods = self.resources.get(ResourceType.POD, [])

        count = 0
        for i, pod in enumerate(pods):
            # Assign pod to node (round-robin)
            node = nodes[i % len(nodes)]

            created_at = self.random_time_in_range()
            updated_at = self.current_time

            self.client.upsert_relation(
                RelationType.NODE_WITH_POD,
                ResourceType.NODE,
                node,
                ResourceType.POD,
                pod,
                created_at,
                updated_at
            )
            count += 1

        logger.info(f"✓ Created {count} node_with_pod relations")

    def _create_pod_with_service_relations(self):
        """Create pod_with_service relations"""
        logger.info("Creating pod_with_service relations...")

        services = self.resources.get(ResourceType.SERVICE, [])
        pods = self.resources.get(ResourceType.POD, [])

        count = 0
        # Assign pods to services evenly
        pods_per_service = len(pods) // len(services)

        for i, service in enumerate(services):
            start_idx = i * pods_per_service
            end_idx = start_idx + pods_per_service if i < len(services) - 1 else len(pods)

            for pod in pods[start_idx:end_idx]:
                created_at = self.random_time_in_range()
                updated_at = self.current_time

                self.client.upsert_relation(
                    RelationType.POD_WITH_SERVICE,
                    ResourceType.POD,
                    pod,
                    ResourceType.SERVICE,
                    service,
                    created_at,
                    updated_at
                )
                count += 1

        logger.info(f"✓ Created {count} pod_with_service relations")
    
    def _create_biz_cluster_relations(self):
        """Create biz to cluster relations (CMDB layer)"""
        logger.info("Creating biz-cluster relations...")
        
        biz = self.resources.get(ResourceType.BIZ, [])
        cluster = self.resources.get(ResourceType.CLUSTER, [])
        
        if not biz or not cluster:
            logger.warning("  ⚠ Skipped: biz or cluster not found")
            return
        
        # NOTE: biz_with_cluster is not in the standard relation types
        # But we can use CMDB relations if needed
        # For now, we just log the association
        logger.info(f"  ℹ Business {biz[0]['bk_biz_id']} associated with cluster {cluster[0]['bcs_cluster_id']}")
        logger.info(f"  ℹ This association is implicit through namespace and resource tags")
    
    def _create_deployment_chain_relations(self):
        """
        Create Deployment -> ReplicaSet -> Pod chain
        
        This completes the static relation chain:
        Deployment -> ReplicaSet -> Pod -> Service
        """
        logger.info("Creating deployment chain relations...")
        
        deployments = self.resources.get(ResourceType.DEPLOYMENT, [])
        pods = self.resources.get(ResourceType.POD, [])
        
        if not deployments:
            logger.warning("  ⚠ No deployments found, skipping deployment chain")
            return
        
        replicasets = []
        deployment_rs_count = 0
        pod_rs_count = 0
        
        # Assign pods to deployments evenly
        pods_per_deployment = len(pods) // len(deployments)
        
        for i, deploy in enumerate(deployments):
            # 1. Create a ReplicaSet for this Deployment
            rs_data = {
                "bcs_cluster_id": deploy["bcs_cluster_id"],
                "namespace": deploy["namespace"],
                "replicaset": f"{deploy['deployment']}-rs-001"
            }
            
            created_at = self.random_time_in_range()
            updated_at = self.current_time
            
            self.client.upsert_node(
                ResourceType.REPLICASET,
                rs_data,
                created_at,
                updated_at
            )
            replicasets.append(rs_data)
            
            # 2. Create DEPLOYMENT_WITH_REPLICASET relation
            self.client.upsert_relation(
                RelationType.DEPLOYMENT_WITH_REPLICASET,
                ResourceType.DEPLOYMENT,
                deploy,
                ResourceType.REPLICASET,
                rs_data,
                created_at,
                updated_at
            )
            deployment_rs_count += 1
            
            # 3. Assign pods to this ReplicaSet
            start_idx = i * pods_per_deployment
            end_idx = start_idx + pods_per_deployment if i < len(deployments) - 1 else len(pods)
            assigned_pods = pods[start_idx:end_idx]
            
            for pod in assigned_pods:
                created_at = self.random_time_in_range()
                updated_at = self.current_time
                
                self.client.upsert_relation(
                    RelationType.POD_WITH_REPLICASET,
                    ResourceType.POD,
                    pod,
                    ResourceType.REPLICASET,
                    rs_data,
                    created_at,
                    updated_at
                )
                pod_rs_count += 1
        
        # Store replicasets for later use
        self.resources[ResourceType.REPLICASET] = replicasets
        
        logger.info(f"  ✓ Created {len(replicasets)} replicasets")
        logger.info(f"  ✓ Created {deployment_rs_count} deployment_with_replicaset relations")
        logger.info(f"  ✓ Created {pod_rs_count} pod_with_replicaset relations")


    def create_dynamic_relations(self):
        """Create dynamic pod_to_pod traffic relations"""
        logger.info("Creating pod_to_pod traffic relations...")

        pods = self.resources.get(ResourceType.POD, [])

        count = 0
        for source_pod in pods:
            if random.random() < MockConfig.POD_TO_POD_TRAFFIC_PROBABILITY:
                # Select random target pod (different from source)
                target_candidates = [p for p in pods if p != source_pod]
                if target_candidates:
                    target_pod = random.choice(target_candidates)

                    created_at = self.random_time_in_range()
                    updated_at = self.current_time

                    self.client.upsert_relation(
                        RelationType.POD_TO_POD,
                        ResourceType.POD,
                        source_pod,
                        ResourceType.POD,
                        target_pod,
                        created_at,
                        updated_at
                    )

                    # Store for metric generation
                    self.traffic_relations.append((source_pod, target_pod))
                    count += 1

        logger.info(f"✓ Created {count} pod_to_pod traffic relations")

    def create_metrics_metadata(self):
        """Create metric nodes following documentation 7.2.2"""
        logger.info("Creating metric metadata...")

        metrics = [
            {
                "metric_name": "pod_to_pod_flow_total",
                "metric_type": MetricType.COUNTER.value,
                "unit": "count",
                "description": "Pod到Pod的流量访问量"
            },
            {
                "metric_name": "pod_to_pod_flow_seconds",
                "metric_type": MetricType.GAUGE.value,
                "unit": "seconds",
                "description": "Pod到Pod的流量访问耗时"
            },
            {
                "metric_name": "pod_to_pod_flow_error",
                "metric_type": MetricType.COUNTER.value,
                "unit": "count",
                "description": "Pod到Pod的流量错误数"
            }
        ]

        created_at = self.random_time_in_range()
        updated_at = self.current_time

        for metric_data in metrics:
            self.client.upsert_node(
                ResourceType.METRIC,
                metric_data,
                created_at,
                updated_at
            )

        logger.info(f"✓ Created {len(metrics)} metric definitions")
        self.resources[ResourceType.METRIC] = metrics

    def create_relation_has_metric(self):
        """Create relation_has_metric following documentation 7.2.2"""
        logger.info("Creating relation_has_metric associations...")

        metrics = self.resources.get(ResourceType.METRIC, [])

        # Query all pod_to_pod relations
        result = self.client.execute_sql("SELECT id FROM pod_to_pod;")
        pod_to_pod_relations = result[0].get('result', [])

        count = 0
        for relation in pod_to_pod_relations:
            relation_id = relation['id']

            for metric_data in metrics:
                metric_id = IDGenerator.generate_node_id(ResourceType.METRIC, metric_data)

                # Create relation_has_metric with result_table_id
                sql = f"""
                RELATE pod_to_pod:`{relation_id}`->relation_has_metric->metric:`{metric_id}` SET
                    result_table_id = '{MockConfig.RESULT_TABLE_ID}_{metric_data["metric_name"]}',
                    created_at = type::datetime('{self.client.format_datetime(self.current_time)}'),
                    updated_at = type::datetime('{self.client.format_datetime(self.current_time)}');
                """

                self.client.execute_sql(sql)
                count += 1

        logger.info(f"✓ Created {count} relation_has_metric associations")

    def generate_metric_values(self):
        """Generate metric time series values (simulated VictoriaMetrics data)"""
        logger.info("Generating metric time series values...")
        logger.info(f"📊 NOTE: In production, these would be written to VictoriaMetrics")
        logger.info(f"    Result Table ID: {MockConfig.RESULT_TABLE_ID}_*")

        time_points = []
        delta = (MockConfig.END_TIME - MockConfig.START_TIME) / MockConfig.METRIC_TIME_POINTS
        for i in range(MockConfig.METRIC_TIME_POINTS):
            time_points.append(MockConfig.START_TIME + delta * i)

        metric_samples = []

        for source_pod, target_pod in self.traffic_relations:
            # Generate labels
            labels = {
                "source_bcs_cluster_id": source_pod["bcs_cluster_id"],
                "source_namespace": source_pod["namespace"],
                "source_pod": source_pod["pod"],
                "target_bcs_cluster_id": target_pod["bcs_cluster_id"],
                "target_namespace": target_pod["namespace"],
                "target_pod": target_pod["pod"]
            }

            # Generate flow_total (累计请求数)
            flow_total_values = []
            cumulative_total = 0
            for ts in time_points:
                increment = random.randint(*MockConfig.FLOW_TOTAL_RANGE)
                cumulative_total += increment
                flow_total_values.append({
                    "timestamp": int(ts.timestamp() * 1000),
                    "value": cumulative_total
                })

            metric_samples.append({
                "metric": "pod_to_pod_flow_total",
                "result_table_id": f"{MockConfig.RESULT_TABLE_ID}_pod_to_pod_flow_total",
                "labels": labels,
                "values": flow_total_values
            })

            # Generate flow_seconds (请求耗时)
            flow_seconds_values = []
            for ts in time_points:
                latency = random.uniform(*MockConfig.FLOW_SECONDS_RANGE)
                flow_seconds_values.append({
                    "timestamp": int(ts.timestamp() * 1000),
                    "value": round(latency, 3)
                })

            metric_samples.append({
                "metric": "pod_to_pod_flow_seconds",
                "result_table_id": f"{MockConfig.RESULT_TABLE_ID}_pod_to_pod_flow_seconds",
                "labels": labels,
                "values": flow_seconds_values
            })

            # Generate flow_error (错误数)
            flow_error_values = []
            cumulative_errors = 0
            for ts, total_sample in zip(time_points, flow_total_values):
                error_rate = random.uniform(*MockConfig.FLOW_ERROR_RATE_RANGE)
                errors = int(total_sample["value"] * error_rate)
                cumulative_errors = errors
                flow_error_values.append({
                    "timestamp": int(ts.timestamp() * 1000),
                    "value": cumulative_errors
                })

            metric_samples.append({
                "metric": "pod_to_pod_flow_error",
                "result_table_id": f"{MockConfig.RESULT_TABLE_ID}_pod_to_pod_flow_error",
                "labels": labels,
                "values": flow_error_values
            })

        # Print sample metric data
        logger.info(f"✓ Generated {len(metric_samples)} metric time series")
        logger.info("\n" + "=" * 70)
        logger.info("Sample Metric Data (VictoriaMetrics format):")
        logger.info("=" * 70)
        if metric_samples:
            sample = metric_samples[0]
            logger.info(f"\nMetric: {sample['metric']}")
            logger.info(f"Result Table ID: {sample['result_table_id']}")
            logger.info(f"Labels: {json.dumps(sample['labels'], indent=2)}")
            logger.info(f"Sample Values (first 3 of {len(sample['values'])}):")
            for value in sample['values'][:3]:
                ts = datetime.fromtimestamp(value['timestamp'] / 1000)
                logger.info(f"  {ts.strftime('%Y-%m-%d %H:%M:%S')}: {value['value']}")
        logger.info("=" * 70 + "\n")

        # Save to work directory
        output_file = "./metric_samples.json"
        with open(output_file, 'w') as f:
            json.dump(metric_samples, f, indent=2)
        logger.info(f"✓ Metric samples saved to: {output_file}")

        return metric_samples

    def generate_all(self):
        """Generate all mock data for BKOP Business 2"""
        logger.info("\n" + "=" * 70)
        logger.info("Starting BKOP Business 2 Mock Data Generation")
        logger.info("=" * 70 + "\n")

        # Create resources
        self.create_biz()
        self.create_cluster()
        self.create_namespace()
        self.create_nodes()
        self.create_pods()
        self.create_services()
        self.create_deployments()

        # Create relations
        self.create_static_relations()
        self.create_dynamic_relations()

        # Create metrics metadata and associations
        self.create_metrics_metadata()
        self.create_relation_has_metric()

        # Generate metric time series values (for VictoriaMetrics)
        self.generate_metric_values()

        logger.info("\n" + "=" * 70)
        logger.info("BKOP Business 2 Mock Data Generation Completed!")
        logger.info("=" * 70)
        self.print_summary()

    def print_summary(self):
        """Print generation summary"""
        logger.info("\n📊 Summary:")
        logger.info("-" * 70)
        logger.info(f"  Business ID: {MockConfig.BIZ_ID} ({MockConfig.BIZ_NAME})")
        logger.info(f"  Cluster: {MockConfig.CLUSTER_ID}")
        logger.info(f"  Namespace: {MockConfig.NAMESPACE}")
        logger.info(f"  Result Table ID: {MockConfig.RESULT_TABLE_ID}")
        logger.info("-" * 70)
        for resource_type, items in self.resources.items():
            logger.info(f"  {resource_type.value:20s}: {len(items):5d} items")
        logger.info(f"  {'traffic_relations':20s}: {len(self.traffic_relations):5d} items")
        logger.info("-" * 70)


# ============================================================================
# Main Function
# ============================================================================

def create_storage_client(backend: StorageBackend) -> StorageClient:
    """Factory function to create storage client based on backend type"""
    if backend == StorageBackend.NATIVE:
        logger.info("Creating Native SurrealDB client...")
        return SurrealDBClient()
    elif backend == StorageBackend.BKBASE:
        logger.info("Creating BKBase SurrealDB client...")
        return BKBaseClient()
    else:
        raise ValueError(f"Unsupported storage backend: {backend}")


def main():
    """Main function"""
    # Parse command line arguments
    parser = argparse.ArgumentParser(
        description='Mock BKOP Business 2 Traffic to SurrealDB',
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
  # Use native SurrealDB
  python %(prog)s --backend native
  
  # Use BKBase SurrealDB
  python %(prog)s --backend bkbase
  
Environment Variables:
  See .env.example for all available configuration options
        """
    )
    parser.add_argument(
        '--backend',
        type=str,
        default=os.getenv('STORAGE_BACKEND', 'native'),
        choices=['native', 'bkbase'],
        help='Storage backend to use (default: native)'
    )
    parser.add_argument(
        '--debug',
        action='store_true',
        help='Enable debug logging'
    )
    
    args = parser.parse_args()
    
    # Set logging level
    if args.debug:
        logging.getLogger().setLevel(logging.DEBUG)
    
    # Parse backend
    backend = StorageBackend(args.backend)
    
    logger.info("=" * 70)
    logger.info(" Mock BKOP Business 2 Traffic to SurrealDB")
    logger.info("=" * 70)
    logger.info(f"\nConfiguration:")
    logger.info(f"  Storage Backend: {backend.value}")
    
    if backend == StorageBackend.NATIVE:
        logger.info(f"  SurrealDB URL: {SurrealDBConfig.URL}")
        logger.info(f"  Namespace: {SurrealDBConfig.NAMESPACE}")
        logger.info(f"  Database: {SurrealDBConfig.DATABASE}")
    elif backend == StorageBackend.BKBASE:
        logger.info(f"  BKBase API URL: {BKBaseConfig.API_URL}")
        logger.info(f"  Result Table ID: {BKBaseConfig.RESULT_TABLE_ID}")
        logger.info(f"  Prefer Storage: {BKBaseConfig.PREFER_STORAGE}")
    
    logger.info(f"  Business ID: {MockConfig.BIZ_ID}")
    logger.info(f"  Business Name: {MockConfig.BIZ_NAME}")
    logger.info(f"  Cluster ID: {MockConfig.CLUSTER_ID}")
    logger.info(f"  Namespace: {MockConfig.NAMESPACE}")
    logger.info(f"  Time Range: {MockConfig.START_TIME} to {MockConfig.END_TIME}")
    logger.info(f"  Mode: Idempotent (supports multiple runs without data conflicts)")
    logger.info("")

    try:
        # Create client
        client = create_storage_client(backend)

        # Test connection
        logger.info(f"Testing {backend.value} connection...")
        
        if backend == StorageBackend.NATIVE:
            result = client.execute_sql("INFO FOR DB;")
        else:
            # For BKBase, just try a simple query
            result = client.execute_sql("SELECT * FROM pod LIMIT 1;")
        
        logger.info("Connection successful!\n")

        # Create generator
        generator = MockGenerator(client)

        # Generate data
        generator.generate_all()

        logger.info("\ndone ~")

    except ValueError as e:
        logger.error(f"\n❌ Configuration Error: {e}")
        logger.error("\n💡 Troubleshooting:")
        logger.error("  - Check your .env file or environment variables")
        logger.error("  - See .env.example for required configuration")
        return 1
    except Exception as e:
        logger.error(f"\n❌ Error: {e}")
        logger.error("\n💡 Troubleshooting:")
        
        if backend == StorageBackend.NATIVE:
            logger.error("  - Check if SurrealDB is running")
            logger.error("  - Verify database connection settings in environment variables")
        else:
            logger.error("  - Check BKBase API URL and credentials")
            logger.error("  - Verify BKBASE_APP_SECRET is set correctly")
            logger.error("  - Check network connectivity to BKBase API")
        
        import traceback
        traceback.print_exc()
        return 1

    return 0


if __name__ == "__main__":
    exit(main())
