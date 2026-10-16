-- 用户主动提交的坑人反馈，按去除首尾空白后的中介姓名累计。
CREATE TABLE rental_broker_feedback (
    broker_name VARCHAR(256) COLLATE utf8mb4_0900_bin NOT NULL COMMENT '中介姓名，同名反馈合并累计',
    feedback_count BIGINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '反馈坑人次数',
    created_at DATETIME(3) NOT NULL COMMENT '首次反馈时间 UTC',
    updated_at DATETIME(3) NOT NULL COMMENT '最近反馈时间 UTC',
    PRIMARY KEY (broker_name),
    CONSTRAINT chk_broker_feedback_count CHECK (feedback_count >= 1)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin COMMENT='坑人记录表';
