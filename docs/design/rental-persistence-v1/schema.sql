-- 租房业务数据持久化 V1；目标 MySQL 8.4+，由 cmd/migrate 记录版本执行。
-- 时间由应用显式写入 UTC；字符列均不可静默截断。

CREATE TABLE rental_extraction_task (
    id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '后端生成的任务 UUID，重试不变',
    file_name LONGTEXT NOT NULL COMMENT '输入 Document 的文件名',
    input_document JSON NOT NULL COMMENT '完整输入 Document',
    extraction_context JSON NOT NULL COMMENT '创建任务时固定的上下文，如 default_currency',
    schema_version SMALLINT UNSIGNED NOT NULL COMMENT '提取输出契约版本',
    extractor_version VARCHAR(128) NOT NULL COMMENT '规则及提示词版本',
    provider MEDIUMTEXT NULL COMMENT '实际调用提供方',
    model TEXT NULL COMMENT '实际调用模型',
    stage VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'EXTRACT',
    status VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'PENDING',
    attempt_count INT UNSIGNED NOT NULL DEFAULT 0,
    structured_result JSON NULL COMMENT '通过验证的完整标准 JSON',
    last_error_code VARCHAR(64) NULL,
    last_error_message TEXT NULL COMMENT '脱敏后的错误',
    next_retry_at DATETIME(3) NULL,
    created_at DATETIME(3) NOT NULL COMMENT '任务接受时间 UTC',
    updated_at DATETIME(3) NOT NULL COMMENT '状态更新时间 UTC',
    completed_at DATETIME(3) NULL,
    PRIMARY KEY (id),
    KEY idx_task_retry (status, next_retry_at, id),
    KEY idx_task_created (created_at, id),
    CONSTRAINT chk_task_stage CHECK (stage IN ('EXTRACT', 'PERSIST')),
    CONSTRAINT chk_task_status CHECK (status IN ('PENDING', 'RUNNING', 'RETRY_WAIT', 'SUCCEEDED', 'FAILED')),
    CONSTRAINT chk_task_result CHECK (stage <> 'PERSIST' OR structured_result IS NOT NULL),
    CONSTRAINT chk_task_success CHECK (status <> 'SUCCEEDED' OR stage = 'PERSIST'),
    CONSTRAINT chk_task_completion CHECK (
        (status IN ('SUCCEEDED', 'FAILED') AND completed_at IS NOT NULL)
        OR (status NOT IN ('SUCCEEDED', 'FAILED') AND completed_at IS NULL)
    ),
    CONSTRAINT chk_task_retry_time CHECK (
        (status = 'RETRY_WAIT' AND next_retry_at IS NOT NULL)
        OR (status <> 'RETRY_WAIT' AND next_retry_at IS NULL)
    )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='AI 业务提取任务';

CREATE TABLE rental_import_record (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    task_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    community_location TEXT NULL,
    area_sqm DECIMAL(10,2) NULL,
    area_min_sqm DECIMAL(10,2) NULL,
    area_max_sqm DECIMAL(10,2) NULL,
    area_qualifier VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NULL,
    area_scope VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NULL,
    orientation VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NULL,
    broker_name VARCHAR(256) NULL,
    currency CHAR(3) CHARACTER SET ascii COLLATE ascii_bin NULL,
    monthly_rent DECIMAL(14,2) NULL,
    deposit_months DECIMAL(6,2) NULL,
    prepay_months DECIMAL(6,2) NULL,
    deposit_amount DECIMAL(14,2) NULL,
    quoted_first_month_total DECIMAL(14,2) NULL COMMENT '原文报价，不是 AI 计算结果',
    quoted_monthly_total DECIMAL(14,2) NULL COMMENT '原文报价，不是 AI 计算结果',
    data_quality VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    created_at DATETIME(3) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_record_task (task_id),
    KEY idx_record_created (created_at, id),
    CONSTRAINT fk_record_task FOREIGN KEY (task_id) REFERENCES rental_extraction_task (id) ON DELETE RESTRICT ON UPDATE RESTRICT,
    CONSTRAINT chk_record_area CHECK (
        (area_qualifier IS NULL AND area_sqm IS NULL AND area_min_sqm IS NULL AND area_max_sqm IS NULL)
        OR (area_qualifier IS NOT NULL AND area_qualifier IN ('EXACT', 'APPROXIMATE') AND area_sqm IS NOT NULL AND area_sqm > 0 AND area_min_sqm IS NULL AND area_max_sqm IS NULL)
        OR (area_qualifier IS NOT NULL AND area_qualifier = 'RANGE' AND area_sqm IS NULL AND area_min_sqm IS NOT NULL AND area_max_sqm IS NOT NULL AND area_min_sqm > 0 AND area_max_sqm >= area_min_sqm)
    ),
    CONSTRAINT chk_record_scope CHECK (area_scope IS NULL OR area_scope IN ('ROOM', 'UNIT', 'GROSS', 'USABLE', 'OTHER')),
    CONSTRAINT chk_record_orientation CHECK (orientation IS NULL OR orientation IN ('N', 'NE', 'E', 'SE', 'S', 'SW', 'W', 'NW', 'NS', 'EW', 'OTHER')),
    CONSTRAINT chk_record_amounts CHECK (
        (monthly_rent IS NULL OR monthly_rent >= 0)
        AND (deposit_amount IS NULL OR deposit_amount >= 0)
        AND (quoted_first_month_total IS NULL OR quoted_first_month_total >= 0)
        AND (quoted_monthly_total IS NULL OR quoted_monthly_total >= 0)
        AND (deposit_months IS NULL OR deposit_months >= 0)
        AND (prepay_months IS NULL OR prepay_months > 0)
    ),
    CONSTRAINT chk_record_currency CHECK (
        currency IS NOT NULL OR (monthly_rent IS NULL AND deposit_amount IS NULL AND quoted_first_month_total IS NULL AND quoted_monthly_total IS NULL)
    ),
    CONSTRAINT chk_record_quality CHECK (data_quality IN ('NORMALIZED', 'NEEDS_REVIEW'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='每次 AI 提取的房源业务快照';

CREATE TABLE rental_charge (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    record_id BIGINT UNSIGNED NOT NULL,
    item_index SMALLINT UNSIGNED NOT NULL,
    charge_type VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    charge_name VARCHAR(256) NOT NULL,
    direction VARCHAR(8) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    pricing_mode VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    currency CHAR(3) CHARACTER SET ascii COLLATE ascii_bin NULL,
    amount DECIMAL(14,2) NULL,
    unit_price DECIMAL(18,6) NULL,
    unit VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NULL,
    ratio DECIMAL(10,6) NULL,
    ratio_base VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NULL,
    billing_period VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NULL,
    payment_timing VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NULL,
    source_field_ids JSON NOT NULL,
    notes TEXT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_charge_order (record_id, item_index),
    KEY idx_charge_type (charge_type, record_id),
    CONSTRAINT fk_charge_record FOREIGN KEY (record_id) REFERENCES rental_import_record (id) ON DELETE RESTRICT ON UPDATE RESTRICT,
    CONSTRAINT chk_charge_type CHECK (charge_type IN ('AGENCY', 'WATER', 'ELECTRICITY', 'CLEANING', 'PROPERTY', 'GAS', 'OTHER')),
    CONSTRAINT chk_charge_direction CHECK (direction IN ('CHARGE', 'CREDIT', 'UNKNOWN')),
    CONSTRAINT chk_charge_unit CHECK (unit IS NULL OR unit IN ('TON', 'M3', 'KWH', 'SQM', 'PERSON', 'HOUSEHOLD', 'OTHER')),
    CONSTRAINT chk_charge_base CHECK (ratio_base IS NULL OR ratio_base IN ('MONTHLY_RENT', 'ANNUAL_RENT', 'OTHER')),
    CONSTRAINT chk_charge_period CHECK (billing_period IS NULL OR billing_period IN ('ONCE', 'DAY', 'WEEK', 'MONTH', 'QUARTER', 'YEAR', 'OTHER')),
    CONSTRAINT chk_charge_timing CHECK (payment_timing IS NULL OR payment_timing IN ('MOVE_IN', 'RECURRING', 'MOVE_OUT', 'OTHER')),
    CONSTRAINT chk_charge_amounts CHECK ((amount IS NULL OR amount >= 0) AND (unit_price IS NULL OR unit_price >= 0) AND (ratio IS NULL OR ratio >= 0)),
    CONSTRAINT chk_charge_sources CHECK (JSON_TYPE(source_field_ids) = 'ARRAY' AND JSON_LENGTH(source_field_ids) > 0),
    CONSTRAINT chk_charge_mode CHECK (
        (pricing_mode = 'FIXED' AND amount IS NOT NULL AND currency IS NOT NULL AND unit_price IS NULL AND unit IS NULL AND ratio IS NULL AND ratio_base IS NULL)
        OR (pricing_mode = 'UNIT_PRICE' AND amount IS NULL AND currency IS NOT NULL AND unit_price IS NOT NULL AND unit IS NOT NULL AND ratio IS NULL AND ratio_base IS NULL)
        OR (pricing_mode = 'RENT_RATIO' AND amount IS NULL AND currency IS NOT NULL AND unit_price IS NULL AND unit IS NULL AND ratio IS NOT NULL AND ratio_base IS NOT NULL)
        OR (pricing_mode = 'INCLUDED' AND amount IS NULL AND unit_price IS NULL AND unit IS NULL AND ratio IS NULL AND ratio_base IS NULL)
        OR (pricing_mode = 'FREE' AND amount IS NOT NULL AND amount = 0 AND currency IS NOT NULL AND unit_price IS NULL AND unit IS NULL AND ratio IS NULL AND ratio_base IS NULL)
        OR (pricing_mode = 'UNKNOWN' AND amount IS NULL AND unit_price IS NULL AND unit IS NULL AND ratio IS NULL AND ratio_base IS NULL)
    )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='按业务类型、计价方式和周期保存收费项';
