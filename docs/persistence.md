# 后台业务提取与 MySQL 保存

启用后，用户点击“发送并分析”，后端校验 Document 并将输入快照写入本地 bbolt 队列。原有报告调用保持输入与响应内容；独立后台 Codex 会话根据 JSON Schema 提取业务字段，校验后写入 MySQL。

只上传 Excel 不产生任务；每次有效分析请求产生一个新任务。同一文件再次分析保存新快照，后台重试沿用任务 ID。报告失败或取消不撤销已经落盘的任务。每个任务额外调用一次 AI，提取失败最多尝试 3 次，因此会增加模型用量。

## 配置与启动

目标数据库为 MySQL 8.4+，需要先创建数据库和应用账号。迁移账号需要 CREATE、ALTER/建表相关权限；运行账号需要 SELECT、INSERT、UPDATE。应用不会自动创建数据库，也不会在 HTTP 启动时执行 DDL。

```sql
CREATE DATABASE house_rent CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
```

在仓库根目录复制示例并编辑本机配置，将 `.env` 中的账号与密码替换为实际值：

```powershell
Copy-Item .env.example .env
# 编辑 .env 后执行：
go run ./cmd/migrate
go run ./cmd/server
```

服务和迁移命令在读取配置前自动加载当前工作目录的 `.env`。文件不存在时继续使用系统环境变量；环境变量优先于 `.env`，包括显式设置的空字符串。`.env` 和 `.env.*` 已被 Git 忽略，仅提交不含真实密码的 `.env.example`。请在仓库根目录运行命令，或在服务启动目录放置 `.env`。未配置或显式清空 `RENT_MYSQL_DSN` 时关闭后台保存，原有功能可独立使用。

连接串可使用单引号包裹，原样保留密码里的 `$`、`#` 等字符；支持 Windows UTF-8 BOM。配置解析失败时只输出通用错误，不输出配置原文。数据库连接串不传给 Codex 子进程，也不会进入前端构建变量。

`RENT_DEFAULT_CURRENCY` 默认 CNY，仅作为未明确币种时的后端固定上下文；设置为空则不提供默认币种。每个任务保留创建时的上下文，重试不受之后环境变量变化影响。未给出单位/周期时不会自动补 MONTH，也不会自动把房间面积当物业计费面积。

可用 `RENT_PERSISTENCE_DIR` 或 `-persistence-dir` 指定队列目录。默认位于系统用户配置目录的 `house-rent-table/persistence/tasks.db`；Windows 是 `%APPDATA%\house-rent-table\persistence\tasks.db`。同一目录只允许一个服务进程打开。队列与 MySQL 包含填写原文，清空页面不删除历史。

MySQL 连接自动使用 utf8mb4、UTC 和严格 SQL 模式。所有时间按 UTC 保存。大输入可超过默认数据包限制，建议服务器 `max_allowed_packet` 至少 64 MiB；写入失败不会截断输入或标准结果，任务留在本地补写。连接串解析错误会阻止启动；数据库暂不可用或尚未迁移会明确日志告警，HTTP 主服务仍可启动。

## 业务结构

- `rental_extraction_task`：输入快照、版本、模型、提取结果及任务状态。
- `rental_import_record`：地点、面积/范围、朝向、中介姓名、月租、押付规则和用户报价。
- `rental_charge`：收费类型、金额/单价/比例、单位、周期、付款时间和来源。
- `rental_schema_migrations`：迁移版本及校验和。

完整字段说明见 [V1 设计](design/rental-persistence-v1/README.md)。可执行契约位于 `internal/extraction/schema.json`，Go 类型位于 `internal/extraction/types.go`，DDL 位于 `internal/persistence/migrations/001_rental.sql`。设计目录保留可评审副本，测试核对 Schema 一致性。

金额以十进制字符串传输并直接写入 DECIMAL，不经二进制浮点数。零、未知、已含、免费分别保存；面积支持精确值、约数和范围。首月/后续月费保留用户报价，后台提取不重新核算或覆盖报价。V1 不执行年租折算月租等算术派生，原文口径无法直接映射时留空并记录问题。

验证包括唯一 JSON 对象、禁止重复/额外/缺失键、类型与精度、面积及收费字段组合、引用 ID 和路径、非空输入覆盖、非空主表字段证据等。格式或组合不合格会重试；原文不确定则保存为 null/UNKNOWN 并标 NEEDS_REVIEW。NORMALIZED 不代表房源信息齐全或经过人工核实；自然语言提取仍可能有语义错误，需保留原文复核。

## 故障与恢复

本地队列提交同步落盘后才确认任务已接收；AI 和 MySQL 都在请求生命周期之外运行。最多容纳 1000 项，同时受 512 MiB 逻辑预算限制（每项预留 16 MiB 提取结果空间），队列文件上限 1 GiB。先到达任一限制就拒绝新任务。成功入库或失败记录已写入 MySQL 后，移除本地任务并复用空闲页。

提取结果通过校验后先落本地，再写 MySQL。数据库失败按指数退避补写，最长间隔 256 秒，不重复调用 AI；服务重启继续恢复。模型最多自动尝试 3 次，最终失败只留任务日志，不插入业务快照。原始模型/数据库错误不直接展示，以免带出认证信息或用户数据。

房源、全部收费项及任务成功状态在同一个事务提交；并发/未知提交结果的重放通过唯一 task_id 和事务锁去重。新请求不按文件名、小区或文件内容去重。失败任务由用户重新分析产生新任务，不在本版本提供修改历史数据的操作。

若入队失败，页面明确提示后台任务未创建，主报告仍继续。本地存储故障时无法同时承诺无损记录和主流程永远成功。机器故障发生在模型已返回但结果尚未落盘的极短窗口，恢复后也可能再次调用模型；不宣称外部 AI 调用恰好一次。

后台使用本应用的登录配置，独立 App Server、工作目录、事件流和超时。提取禁用联网、主分析 skill 和计算工具；每次提取尝试读取当时选定的提供方，数据库补写不会接触 AI。任务中不保存登录凭据。退出服务取消运行中的会话，保留未完成队列；升级提取规则后，尚未提取的旧版本任务明确失败，避免静默改变规则。

## 状态查询

- `GET /api/persistence`：返回 `{ "enabled": true/false }`。
- `POST /api/analyze`：原响应保持不变，通过 `X-Rental-Persistence: queued|failed` 和 `X-Rental-Task-Id` 提供后台收据。
- `GET /api/persistence/tasks/{id}`：返回 taskId、stage、status 及安全错误说明，不返回输入、模型结果或凭据。

阶段为 EXTRACT/PERSIST；状态为 PENDING/RUNNING/RETRY_WAIT/SUCCEEDED/FAILED。页面在收到报告响应的收据后轮询当前任务，最多十分钟；轮询结束不取消任务。若提前取消主请求而未收到响应头，页面无法获得该次任务 ID，但已落盘任务仍继续，可以在数据库按时间查询。暂不提供历史列表或批量删除界面。

## 验证

```powershell
go test ./...
go vet ./...
npm --prefix web run test:e2e
```

真实数据库测试仅针对可丢弃的独立测试库，会创建临时触发器以验证事务回滚：

```powershell
$env:RENT_TEST_MYSQL_DSN = '测试账号:密码@tcp(127.0.0.1:3306)/rental_test'
go test ./internal/persistence -run TestMySQLTransactions -count=1
```

使用已安装 Codex 和本地模拟 Responses 服务验证 outputSchema，不访问真实供应商、不消耗模型额度：

```powershell
$env:RENT_CODEX_SMOKE = '1'
go test ./internal/codex -run TestInstalledCodexExtractionSchema -count=1
```

本轮已通过全部 Go 测试、go vet、前端构建、21 项 Playwright 测试和模板检查。隔离 MySQL 9.1 测试通过迁移重放、并发幂等、事务回滚、长元数据和精确小数验证；真实 Codex CLI 对接本地模拟 Responses 服务的测试也已通过。

持久化实现的远端 CI 已通过 MySQL 8.4 集成测试和 Go race 检查；本机 CGO 关闭且未安装可用 C 编译器，未在本机执行 race 检查。

补充 `.env` 加载后，全量 Go 测试、go vet、前端构建和 3 项持久化页面测试通过；新增用例覆盖环境变量优先、空值覆盖、Windows BOM、带特殊字符的单引号值、解析错误脱敏，以及数据库凭据不传给 Codex 子进程。本机通过 `.env` 验证连接并完成了项目库的 V1 迁移。

自定义供应商是否真正遵守结构化输出，仍需在对应提供方环境验证；服务端始终执行独立校验，不依赖供应商声明。本轮未调用真实模型供应商。
