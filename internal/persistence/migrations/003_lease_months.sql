-- 新增独立的总租期，旧记录保持 NULL，不从押付规则推断。
-- DDL 成功但迁移版本未写入时，重试不会重复添加列或约束。
SET @rental_lease_months_ddl = IF(
  (SELECT COUNT(*) FROM information_schema.COLUMNS
   WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'rental_import_record'
     AND COLUMN_NAME = 'lease_months') = 0,
  'ALTER TABLE rental_import_record ADD COLUMN lease_months DECIMAL(6,2) NULL COMMENT ''租期月数'' AFTER monthly_rent, ADD CONSTRAINT chk_record_lease_months CHECK (lease_months IS NULL OR lease_months > 0)',
  'SELECT 1'
);
PREPARE rental_lease_months_stmt FROM @rental_lease_months_ddl;
EXECUTE rental_lease_months_stmt;
DEALLOCATE PREPARE rental_lease_months_stmt;
