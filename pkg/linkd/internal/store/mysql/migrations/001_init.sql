-- linkd:statement
CREATE TABLE IF NOT EXISTS linkd_events (
    bk_tenant_id VARBINARY(64) NOT NULL,
    event_id VARBINARY(160) NOT NULL,
	related_alert_id VARBINARY(160) NULL,
    version BIGINT UNSIGNED NOT NULL,
    processing_state VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    received_at_ns BIGINT NOT NULL,
    payload JSON NOT NULL,
    processing JSON NOT NULL,
    PRIMARY KEY (bk_tenant_id, event_id),
    KEY idx_linkd_events_unprocessed (processing_state, received_at_ns, bk_tenant_id, event_id),
	KEY idx_linkd_events_alert (bk_tenant_id, related_alert_id, received_at_ns, event_id)
) ENGINE=InnoDB;

-- linkd:statement
CREATE TABLE IF NOT EXISTS linkd_alerts (
    bk_tenant_id VARBINARY(64) NOT NULL,
    alert_id VARBINARY(160) NOT NULL,
    version BIGINT UNSIGNED NOT NULL,
    status VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    event_source_id VARBINARY(32) NOT NULL,
    fingerprint VARBINARY(128) NOT NULL,
    severity VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    latest_event_id VARBINARY(160) NOT NULL,
    end_type VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NULL,
    end_at_ns BIGINT NULL,
    active_marker TINYINT UNSIGNED NULL,
    payload JSON NOT NULL,
    PRIMARY KEY (bk_tenant_id, alert_id),
    UNIQUE KEY uq_linkd_alert_active_identity (
        bk_tenant_id, event_source_id, fingerprint, active_marker
    ),
    KEY idx_linkd_alert_ended_event (bk_tenant_id, latest_event_id, end_type),
    KEY idx_linkd_alert_updated (bk_tenant_id, status, end_at_ns, alert_id),
    KEY idx_linkd_alert_severity (bk_tenant_id, severity, alert_id)
) ENGINE=InnoDB;

-- linkd:statement
CREATE TABLE IF NOT EXISTS linkd_alert_logs (
    bk_tenant_id VARBINARY(64) NOT NULL,
    log_id VARBINARY(128) NOT NULL,
    alert_id VARBINARY(160) NOT NULL,
    created_time_ns BIGINT NOT NULL,
    payload JSON NOT NULL,
    PRIMARY KEY (bk_tenant_id, log_id),
    KEY idx_linkd_alert_logs_timeline (bk_tenant_id, alert_id, created_time_ns, log_id)
) ENGINE=InnoDB;
