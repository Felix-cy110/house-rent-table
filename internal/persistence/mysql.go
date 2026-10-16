package persistence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Felix-cy110/house-rent-table/internal/extraction"
	"github.com/go-sql-driver/mysql"
)

//go:embed migrations/*.sql
var migrations embed.FS

var migrationFiles = []string{"001_rental.sql", "002_broker_feedback.sql"}

func migrationSource(version int) []byte {
	raw, _ := migrations.ReadFile("migrations/" + migrationFiles[version-1])
	return bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
}

type MySQL struct{ db *sql.DB }

func OpenMySQL(dsn string) (*MySQL, error) {
	config, err := mysql.ParseDSN(dsn)
	if err != nil || config.DBName == "" {
		return nil, errors.New("RENT_MYSQL_DSN 无效或未指定数据库")
	}
	config.ParseTime = true
	config.Loc = time.UTC
	config.MultiStatements = false
	config.Timeout = 3 * time.Second
	config.ReadTimeout = 10 * time.Second
	config.WriteTimeout = 10 * time.Second
	config.MaxAllowedPacket = 0 // Fetch actual server packet limit; do not silently truncate.
	if config.Params == nil {
		config.Params = map[string]string{}
	}
	config.Params["time_zone"] = "'+00:00'"
	config.Params["sql_mode"] = "'STRICT_ALL_TABLES,NO_ENGINE_SUBSTITUTION'"
	delete(config.Params, "charset")
	if err = config.Apply(mysql.Charset("utf8mb4", "utf8mb4_0900_ai_ci")); err != nil {
		return nil, errors.New("MySQL 字符集配置无效")
	}
	connector, err := mysql.NewConnector(config)
	if err != nil {
		return nil, errors.New("MySQL 配置无效")
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(3 * time.Minute)
	return &MySQL{db: db}, nil
}
func (s *MySQL) Close() error { return s.db.Close() }

// Migrate is an explicit operator command. It never runs automatically on HTTP startup.
func (s *MySQL) Migrate(ctx context.Context) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return errors.New("无法连接 MySQL")
	}
	defer conn.Close()
	var lock sql.NullInt64
	if err = conn.QueryRowContext(ctx, "SELECT GET_LOCK(SHA2(CONCAT(DATABASE(), ':rental_migrate'), 256), 10)").Scan(&lock); err != nil || !lock.Valid || lock.Int64 != 1 {
		return errors.New("无法获得迁移锁")
	}
	defer func() {
		release, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(release, "SELECT RELEASE_LOCK(SHA2(CONCAT(DATABASE(), ':rental_migrate'), 256))")
	}()
	_, err = conn.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS rental_schema_migrations (version INT PRIMARY KEY, checksum CHAR(64) CHARACTER SET ascii NOT NULL, applied_at DATETIME(3) NOT NULL) ENGINE=InnoDB")
	if err != nil {
		return errors.New("无法创建迁移记录")
	}
	for index := range migrationFiles {
		version := index + 1
		raw := migrationSource(version)
		checksum := fmt.Sprintf("%x", sha256.Sum256(raw))
		var applied string
		err = conn.QueryRowContext(ctx, "SELECT checksum FROM rental_schema_migrations WHERE version=?", version).Scan(&applied)
		if err == nil {
			if applied != checksum {
				return fmt.Errorf("迁移 %d 校验和不匹配，请勿修改已执行的迁移", version)
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return errors.New("无法读取迁移版本")
		}
		lines := []string{}
		for _, line := range strings.Split(string(raw), "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "--") {
				lines = append(lines, line)
			}
		}
		// These migrations only contain CREATE TABLE statements, without procedural SQL.
		for _, statement := range strings.Split(strings.Join(lines, "\n"), ";") {
			statement = strings.TrimSpace(statement)
			if statement == "" {
				continue
			}
			statement = strings.Replace(statement, "CREATE TABLE ", "CREATE TABLE IF NOT EXISTS ", 1)
			if _, err = conn.ExecContext(ctx, statement); err != nil {
				return errors.New("创建租房业务表失败，请检查 MySQL 版本、权限及现有表结构")
			}
		}
		_, err = conn.ExecContext(ctx, "INSERT INTO rental_schema_migrations (version,checksum,applied_at) VALUES (?,?,?)", version, checksum, time.Now().UTC())
		if err != nil {
			return errors.New("保存迁移版本失败")
		}
	}
	return nil
}

func nullable(v string) any {
	if v == "" {
		return nil
	}
	return v
}
func (s *MySQL) SaveTask(ctx context.Context, t Task) error {
	if err := s.CheckMigration(ctx); err != nil {
		return err
	}
	input, err := json.Marshal(t.Input.Document)
	if err != nil {
		return err
	}
	settings, err := json.Marshal(t.Input.Context)
	if err != nil {
		return err
	}
	var result any
	if len(t.Result) > 0 {
		result = string(t.Result)
	}
	var retry any
	if t.Status == "RETRY_WAIT" {
		retry = t.NextRun
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO rental_extraction_task
 (id,file_name,input_document,extraction_context,schema_version,extractor_version,provider,model,stage,status,attempt_count,structured_result,last_error_code,last_error_message,next_retry_at,created_at,updated_at,completed_at)
 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
 ON DUPLICATE KEY UPDATE
 provider=IF(status='SUCCEEDED',provider,VALUES(provider)),model=IF(status='SUCCEEDED',model,VALUES(model)),
 stage=IF(status='SUCCEEDED',stage,VALUES(stage)),attempt_count=IF(status='SUCCEEDED',attempt_count,VALUES(attempt_count)),
 structured_result=IF(status='SUCCEEDED',structured_result,VALUES(structured_result)),
 last_error_code=IF(status='SUCCEEDED',last_error_code,VALUES(last_error_code)),last_error_message=IF(status='SUCCEEDED',last_error_message,VALUES(last_error_message)),
 next_retry_at=IF(status='SUCCEEDED',next_retry_at,VALUES(next_retry_at)),updated_at=IF(status='SUCCEEDED',updated_at,VALUES(updated_at)),
 completed_at=IF(status='SUCCEEDED',completed_at,VALUES(completed_at)),status=IF(status='SUCCEEDED',status,VALUES(status))`,
		t.ID, t.Input.Document.FileName, string(input), string(settings), extraction.Version, t.RulesVersion, nullable(t.Provider), nullable(t.Model), t.Stage, t.Status, t.Attempts, result, nullable(t.ErrorCode), nullable(t.ErrorMessage), retry, t.CreatedAt, t.UpdatedAt, t.CompletedAt)
	return err
}

const rentalColumns = "community_location,area_sqm,area_min_sqm,area_max_sqm,area_qualifier,area_scope,orientation,broker_name,currency,monthly_rent,deposit_months,prepay_months,deposit_amount,quoted_first_month_total,quoted_monthly_total"
const chargeColumns = "charge_type,charge_name,direction,pricing_mode,currency,amount,unit_price,unit,ratio,ratio_base,billing_period,payment_timing,source_field_ids,notes"

// SQL identifiers originate only from constants above, never model output.
func businessValues(v any, columns string) ([]any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var obj map[string]any
	if err = json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	values := []any{}
	for _, key := range strings.Split(columns, ",") {
		value, ok := obj[key]
		if !ok {
			return nil, errors.New("missing business column")
		}
		if key == "source_field_ids" {
			encoded, e := json.Marshal(value)
			if e != nil {
				return nil, e
			}
			value = string(encoded)
		}
		values = append(values, value)
	}
	return values, nil
}
func placeholders(n int) string { return strings.TrimSuffix(strings.Repeat("?,", n), ",") }
func (s *MySQL) Complete(ctx context.Context, t Task, result *extraction.ExtractionResult) error {
	// Re-validate the checkpoint at the persistence boundary; never trust a caller's DTO.
	verified, err := extraction.Decode(t.Result, t.Input)
	if err != nil {
		return err
	}
	result = verified
	t.Stage = "PERSIST"
	t.Status = "RUNNING"
	t.CompletedAt = nil
	t.UpdatedAt = time.Now().UTC()
	if err = s.SaveTask(ctx, t); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status string
	if err = tx.QueryRowContext(ctx, "SELECT status FROM rental_extraction_task WHERE id=? FOR UPDATE", t.ID).Scan(&status); err != nil {
		return err
	}
	if status == "SUCCEEDED" {
		return tx.Commit()
	}
	values, err := businessValues(result.Rental, rentalColumns)
	if err != nil {
		return err
	}
	values = append([]any{t.ID}, values...)
	values = append(values, result.Quality(), time.Now().UTC())
	inserted, err := tx.ExecContext(ctx, "INSERT INTO rental_import_record (task_id,"+rentalColumns+",data_quality,created_at) VALUES ("+placeholders(len(values))+")", values...)
	if err != nil {
		return err
	}
	recordID, err := inserted.LastInsertId()
	if err != nil {
		return err
	}
	for i, charge := range result.Charges {
		values, err = businessValues(charge, chargeColumns)
		if err != nil {
			return err
		}
		values = append([]any{recordID, i}, values...)
		if _, err = tx.ExecContext(ctx, "INSERT INTO rental_charge (record_id,item_index,"+chargeColumns+") VALUES ("+placeholders(len(values))+")", values...); err != nil {
			return err
		}
	}
	now := time.Now().UTC()
	_, err = tx.ExecContext(ctx, "UPDATE rental_extraction_task SET status='SUCCEEDED',stage='PERSIST',updated_at=?,completed_at=?,next_retry_at=NULL,last_error_code=NULL,last_error_message=NULL WHERE id=?", now, now, t.ID)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *MySQL) GetSummary(ctx context.Context, id string) (Summary, error) {
	var summary Summary
	var code, msg sql.NullString
	err := s.db.QueryRowContext(ctx, "SELECT id,stage,status,last_error_code,last_error_message FROM rental_extraction_task WHERE id=?", id).Scan(&summary.ID, &summary.Stage, &summary.Status, &code, &msg)
	if errors.Is(err, sql.ErrNoRows) {
		return Summary{}, ErrNotFound
	}
	summary.ErrorCode, summary.Message = code.String, msg.String
	return summary, err
}

func (s *MySQL) CheckMigration(ctx context.Context) error {
	for index := range migrationFiles {
		version := index + 1
		sum := sha256.Sum256(migrationSource(version))
		var checksum string
		err := s.db.QueryRowContext(ctx, "SELECT checksum FROM rental_schema_migrations WHERE version=?", version).Scan(&checksum)
		if err != nil || checksum != fmt.Sprintf("%x", sum) {
			return fmt.Errorf("MySQL 尚未执行匹配的迁移 %d，请运行 go run ./cmd/migrate；后台任务将保留在本地等待补写", version)
		}
	}
	return nil
}
