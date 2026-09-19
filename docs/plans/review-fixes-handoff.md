# 六项审查修复与全仓 sqlc 迁移交接

日期：2026-09-19。以下是首次暂停时的历史快照，涉及已放弃的 database/sql 方案；用户随后改为原生 pgx。当前实现、验证和阻塞条件以 [原生 pgx 验收记录](pgx-native-acceptance.md) 和 [P68 子计划](P68-runtime-architecture-hardening.md) 为准。

## 1. 当前状态（先读）

- 仓库：`/Users/sudoless/codespace/coding/etherview`。
- 分支：`main`；基线 HEAD：`a805559556025c3f88699a2fb5bc51d137be4d0a`。
- 开始实施时工作区干净；本轮没有提交、推送或创建 PR。
- 写本文档前：206 个已跟踪文件修改，3 个新文件；已跟踪 diff 为 14,109 行新增、14,276 行删除，包含大量 sqlc 生成输出。本文件会再增加一个新文件。
- **当前工作树不能通过 Go 编译，数据库迁移尚未完成，不是可发布结果。** 最后一次 `go test ./internal/... -run '^$'` 退出码为 1，错误见第 5 节。
- worker、首页 API、前端事件处理已有实现及阶段性测试；这些测试是在后续数据库迁移前通过，不能视为当前全树通过。
- 数据库迁移使用过临时 AST/脚本辅助；已知还有自动转换造成的编译错误、NULL 语义和行映射风险。接手者需要审核转换，不应只修到“能编译”。
- `PLAN.md` 的 P68 已改为 `in_progress`；P68-T12～T17 为 `in_progress`，T18 为 `todo`，没有关闭任何新增工作项。P70/P73 外部发布阻塞仍独立存在。

## 2. 已确认的用户范围与约束

用户要求修复全部六项，不只是局部修补：

1. 单个验证任务丢失租约不再退出整个 API 服务。
2. 首页响应不得落后于浏览器已经观察到的最低事件版本。
3. SSE 过期游标导致连接关闭后能够恢复。
4. UserOperation 列表和详情响应新块及重组，撤掉已失效成功数据。
5. **全仓**生产数据库调用统一为类型化 sqlc，取消手工 SQL 常量执行与行扫描的绕行路径。
6. 验证 worker 的真实仓储依赖在接口上明确表达。

用户另行确认：数据库采用全量统一；首页允许小幅 API 扩展。接受的方案保留 `database/sql` 连接池和 pgx 驱动，sqlc 生成 `database/sql` 调用；保留单一浏览器 EventSource。

按 `AGENTS.md` 执行：SQL 起于 `internal/db/queries/`，API 起于 OpenAPI，生成文件必须重新生成；保留链/hash 身份、读写路由、隔离级别、锁顺序、租约围栏和原子发布。没有授权新增 schema 兼容层、回填或弱化测试。既有迁移执行器和合法分区 DDL 是原始 SQL 例外。不要把本次迁移扩展成 pgxpool 替换。

## 3. 已实现部分及代码入口

### 3.1 Worker 与依赖接口（P68-T12）

入口：`internal/verify/worker.go`、`repository.go`、`worker_test.go`；新增 `worker_lease_test.go`。

- 新增 `WorkerRepository`：`ClaimRunnable`、`Renew`、`BindCompiler`、`Fail`、`CompleteV2`、`CompleteProxyV2`。
- `NewWorker` 和 worker 字段改用该接口，删除仓储能力断言和领取任务回退；查询用 `Repository` 缩减为 `Job`、`VerifiedContract`。
- `Run` 遇到单纯 `ErrLeaseLost` 时等待既有 PollInterval 后继续，不返回给监督器。
- 解析/绑定编译器等路径识别租约丢失后直接返回，不再尝试 Fail 已失去所有权的任务。
- 心跳结束使用 `errors.Join(renewalErr, operationErr)` 保留同时发生的编译器清理失败；`fatalCompilerError` 优先于租约丢失处理。
- 新测试覆盖监督器与 API 同级组件存活、后续任务继续、未知/致命错误不被吞掉、清理失败与心跳失败并存，以及解析/绑定丢失租约后不写失败状态。
- 仍需按正式计划补齐所有完成/失败提交分支、真实 API/生产拓扑验证，复审混合错误优先级；不要把未知数据库错误统一当作可恢复错误。

### 3.2 首页事件版本（P68-T13）

入口：`api/openapi.yaml`、`internal/httpapi/httpapi.go`；新增 `home_version_test.go`。

- `GET /home` 新增可选字符串 `min_event_id`。
- `HomeSnapshotResponse` 新增必需字符串 `event_id`，普通响应和完整快照 SSE 共用编码器。
- `RuntimeEventID` 限制规范十进制格式、长度；服务端通过 `ParseUint(..., 63)` 限制到 `9223372036854775807`，允许 0，拒绝前导零、负数、空值、多值、溢出。
- HTTP 订阅现有 HomeFeed，最多等待两秒，跳过小于最低版本的 publication。超时返回 503 `home_snapshot_unavailable`，请求取消时退出，子 context 清理订阅。
- OpenAPI Go 模型和 TS 客户端已重新生成。`web/e2e/server/main.go` 的首页 fixture 增加 event_id。
- 新测试覆盖旧快照不能提前返回、八个并发等待者、无效/未来版本、超时和取消清理。
- 仍需真实浏览器、跨副本、旧请求晚返回及完整契约检查；E2E fixture 的最低版本行为也应审核，不能只增加字段就声称验收完成。

### 3.3 前端事件策略与恢复（P68-T14）

入口：新增 `web/src/api/chainEvents.ts`；修改 `eventInvalidation.tsx`、`homeStream.ts`、`hooks.ts`、`contracts/proxy.ts` 及相关测试。

- React Query meta 注册 `chainEvents` 和 `clearOnReorg`；旧查询根字符串白名单已删除。
- 普通链查询响应 head/reorg；首页和 status 还响应 status；UserOperation 列表与详情启用重组清空。
- WeakMap 按 QueryClient 保存十进制字符串事件水位，BigInt 比较避免 JS 数字精度损失。
- EventSource 在 CONNECTING 时沿用浏览器重连；CLOSED 时关闭旧实例，按 1 秒起、最多 30 秒退避重建；连接打开和终止恢复时刷新链查询。
- 卸载清理连接和计时器，过期实例的回调被忽略。水位在收到合法事件后递增。
- 首页把水位作为 min_event_id，传入 AbortSignal；响应低于当前水位转为暂不可用错误。仅针对该 503 重试，最多三次，退避 250ms～2s。
- `resetQueries` 清掉重组后的 UserOperation 数据；查询失效先取消旧请求。实际 refetch 不阻塞后续事件的取消调度。
- 新测试覆盖 CLOSED 重连、CONNECTING 不重复创建、退避和清理、跨 QueryClient 水位隔离与 orphan 数据撤回。
- 仍需补真实页面的新块刷新/重组撤回和真实 EventSource 400 恢复 E2E；已有单元测试不是完整的浏览器验收。

## 4. 数据库迁移的实际进度（P68-T15～T17）

### 4.1 已改动的基础设施

- `sqlc.yaml` 已切换 `sql_package: database/sql`，**`emit_exported_queries` 目前仍为 true**，因为尚有旧调用及测试引用。
- 为 SQL Scanner 类型指定 UUID/timestamptz 的 pgx v5 `pgtype` 覆盖，生成代码的导入别名为 `pgvalue`。这是值类型，不是原生 pgx 查询桥接。别名不能直接写 pgtype：sqlc 在 stdlib 模式中曾误补旧 `github.com/jackc/pgtype` 导入。
- nullable JSONB 使用 `[]byte`；nullable text/bool/int8/float8 使用指针。NUMERIC 按 stdlib 生成 string/sql.NullString；领域中保留精确解析，无金额浮点转换。
- 增加 `github.com/lib/pq v1.12.3`（sqlc stdlib 数组编解码使用）。未引入第二个生产数据库连接驱动；应用仍由 pgx stdlib 打开连接。最终仍需依赖/license gate。
- `internal/db/runtime.go` 已改为普通 sqlc Queries 和 `*sql.Tx` 回调；查询路径不再通过 `sql.Conn.Raw` 取得原生 pgx。
- `internal/query/proxy.go` 的重复事务桥接已委托公共入口；全树大量 `pgx.ErrNoRows` 已改为 `sql.ErrNoRows`。
- 账务、身份、ENS、指标等原有 typed pgx 调用已适配新生成类型，并有一次早期模块测试通过，但后续改动已超过该验证范围。

### 4.2 迁移覆盖情况

停止前的脚本批次记录：约 367 处直接 Exec/QueryRow 调用、72 处多行查询已转换。这是批次计数，不是最终精确剩余统计；请重新扫描当前树。

- 多个原 `:many` 单行查询改为 `:one`。
- 多个原 `:exec` 改为 **`:execresult`**，暂保留既有 `sql.Result` / RowsAffected 检查；与原计划倾向 `:execrows` 有差异，需要最终收敛或明确记录原因，不能漏掉零行围栏。
- 已把约 140 个 SQL 相关 any 值、15 个辅助函数改成显式可空类型。
- 41 个相关查询增加 `sqlc.narg`，并把剩余 `$N` 参数一并命名。`sqlc.narg` 与未替换的 `$N` 混合会留下参数编号空洞，曾导致生成失败，现已整体命名修复。
- **命名参数会改变生成调用中的参数顺序。** 尚未迁移的手工常量调用可能仍按旧位置传参；应逐一核对，不应运行生产服务。
- 已转换的代码有较多临时 IIFE、`queryValueN`、显式字段赋值和遗留 `//nolint:errcheck`，尚未做最终可读性整理、gofmt、文件长度检查。
- 单行共用 scanner、动态 SQL 选择、mempool prepared statement、核心批处理入口以及若干 UserOperation 行映射仍未完成。
- 多行查询已经有一批改为生成的 `:many`，会先物化切片；**有界 keyset 扫描尚未落实/验证**。必须重新审查大结果集，不能声称保留了原内存界限。
- 尚未关闭 SQL 导出，尚未增强 source-check，尚未新增数据库 ADR 或同步完整架构/开发文档。
- 没有新增 schema migration，没有运行迁移或部署。

### 4.3 接手时必须保留的发现

1. **sqlc 对旧 SQL 的推断并不都正确。** 旧代码只取生成 SQL 字符串并手动 Scan，因此错误的生成参数/结果类型以前没有被使用。例如：
   - `EnrichInlineLoadSignatureABIBindingStatement1` 的 `octet_length(...) <= $3/$4` 被误推断为 string/json.RawMessage；已在 SQL 添加 `::integer`，仍需完成调用迁移。
   - `QuerySearchText` 的 `$2 = ANY(exact_terms)` 被推断为 []string；已在对应 SQL 中补 `$2::text`，仍需迁移调用。
2. **nullable 列上的 `::text` 会丢失 sqlc 输出的 NULL 信息。** 已查阅本机 sqlc v1.31.1 的 output_columns.go，TypeCast 分支会把这类结果当非 NULL。最后一批改动移除了 `finality.safe_number/finalized_number` 的 text cast 并保留别名，使生成字段变为 sql.NullString；Go 映射尚未随之修完。还必须排查其他可空 cast/subquery，不可用 `Valid:true` 掩盖 NULL。
3. **两个生成 Row 不能仅凭列顺序做 Go struct 强转。** 字段名、bool/interface{}、nullable 类型可能不同。现有 `dbgen.ListBlockTransactionsRow(storedRow)` 等有编译错误；应写明确类型化字段映射，或在 SQL 中统一投影别名与类型，不能恢复通用 Scan 包装。
4. **脚本错误已经发生过。** Go AST 位置是字节偏移，Python 字符串切片是 Unicode 码点；含 `·` 的 cursor_search.go 曾被破坏，已从基线恢复该文件后重新迁移相关部分，脚本也改为 bytes。当前仍需检查全 diff，不能把脚本输出视为已审核。
5. 删除 Rows.Err/Close 时，脚本可能删掉整个末尾 `return rows.Err()`，产生 missing return；最后编译已发现 repository_derived.go 一处。应保留原错误处理语义并补正确最终返回。
6. `reader.go` 的一次通用替换把尚未迁移的 Blocks 分支 `*sql.Rows` 当成切片索引；最后编译已明确报错。必须依据各函数当前类型分别修，禁止再全局字符串替换同名变量。
7. 收窄整数时必须保留越界失败，不可为编译通过而直接把 uint64/int 强转为 int32/int64。部分临时转换已用 ParseInt 和界限检查，仍需整体审核。
8. UUID/timestamptz 的 Valid/InfinityModifier 校验、JSONB nil 与 JSON null、RowsAffected、歧义提交、session advisory lock 与坏连接丢弃必须单独验证。

## 5. 最后一次编译失败（当前树）

命令：`go test ./internal/... -run '^$'`。下面是日志中的具体诊断；编译器达到每包错误上限后还会隐藏后续错误，所以不是完整清单。

```text
internal/query/home_snapshot.go:174:71: cannot convert storedRow (variable of struct type dbgen.QueryListTransactionsFirstRow) to type dbgen.ListBlockTransactionsRow
internal/query/models.go:81:36: cannot use scanner.FinalitySafeNumber (variable of struct type sql.NullString) as string value in struct literal
internal/query/models.go:82:41: cannot use scanner.FinalityFinalizedNumber (variable of struct type sql.NullString) as string value in struct literal
internal/query/reader.go:139:36: cannot use queryRow.FinalitySafeNumber (variable of struct type sql.NullString) as string value in struct literal
internal/query/reader.go:140:41: cannot use queryRow.FinalityFinalizedNumber (variable of struct type sql.NullString) as string value in struct literal
internal/query/reader.go:320:64: cannot index rows (variable of type *sql.Rows)
internal/query/reader.go:371:60: cannot convert rows[0] (variable of struct type dbgen.QueryBlockByHashRow) to type dbgen.QueryListBlocksFirstRow
internal/query/reader.go:420:66: cannot convert rows[0] (variable of struct type dbgen.QueryTransactionByHashRow) to type dbgen.ListBlockTransactionsRow
internal/query/transactions.go:295:36: cannot use scanner.SafeNumber (variable of struct type sql.NullString) as string value in struct literal
internal/query/transactions.go:296:41: cannot use scanner.FinalizedNumber (variable of struct type sql.NullString) as string value in struct literal
internal/query/transactions.go:296:41: too many errors
internal/maintenance/postgres.go:131:45: cannot use storedRow.FinalityFinalizedNumber (variable of struct type sql.NullString) as string value in struct literal
internal/maintenance/postgres.go:282:35: cannot use queryRow.FinalityFinalizedNumber (variable of struct type sql.NullString) as string value in struct literal
internal/catalog/stats_trace.go:54:34: cannot use dbgen.CatalogBlockStatsRow(storedRow) (value of struct type dbgen.CatalogBlockStatsRow) as rowScanner value in argument to scanBlockStat: dbgen.CatalogBlockStatsRow does not implement rowScanner (missing method Scan)
internal/catalog/token.go:46:45: cannot use tx.QueryRowContext(ctx, dbgen.CatalogTokenContract, snapshot.ChainID, address, snapshot.BlockNumber) (value of type *sql.Row) as dbgen.CatalogTokenContractsRow value in argument to catalog.scanTokenContract
internal/catalog/transaction_failure.go:67:38: cannot use tx.QueryRowContext(ctx, dbgen.CatalogTransactionFailureRoot, chainID, identity.BlockNumber, blockHash, transactionHash) (value of type *sql.Row) as dbgen.CatalogTransactionTraceRow value in argument to catalog.scanTraceFrame
internal/catalog/transaction_resources.go:58:65: cannot convert storedRow (variable of struct type dbgen.CatalogTransactionTokenEventsRow) to type dbgen.CatalogTokenEventsRow
internal/verify/repository_derived.go:137:1: missing return
```

优先处理：

1. `query` 与 `maintenance` 的 sql.NullString 映射；`query/models.go`、`reader.go`、`transactions.go` 不应再把 sql.NullString 包进 String 字段。
2. `query` 的 Blocks/Block/Transaction 单行与多行读取区别；跨 Row 类型使用显式映射。
3. `catalog` scanner 参数已部分改为 typed Row，而调用点仍传 *sql.Row；需要完成两侧迁移。
4. `repository_derived.go` 的末尾返回。
5. 继续暴露剩余编译错误：UserOperation 的批量 scan targets、typed slice 辅助函数、动态查询分支及其他残余原始调用。

## 6. 验证记录与边界

### 已运行并通过（阶段性，非当前全树）

- worker 初始定向：`go test ./internal/verify -run 'Test.*Worker' -count=1`。
- worker/home：`go test ./internal/verify ./internal/httpapi -run 'Test(Worker|Heartbeat|Home)' -count=1`。
- 新回归 race：`go test -race ./internal/verify ./internal/httpapi -run 'Test(Worker|Heartbeat|Home)' -count=1`。
- Web：在 `web/` 中执行 `./node_modules/.bin/vitest run src/api/eventInvalidation.test.tsx src/api/homeStream.test.tsx src/pages/UserOperationPages.test.tsx src/App.test.tsx`，4 个文件、26 项通过。
- 早期 stdlib typed 调用迁移后：`go test ./internal/{adapters,adminstore,analytics,auth,billing,ens,events,observability,query,userauth,db,store}` 通过，日志 `/tmp/etherview-native-tests.log`。
- API Go 与 TS 生成已执行；sqlc generate 最后一轮成功。单独生成成功不代表 generate-check 已通过。
- 前端曾执行 `npm --prefix web run format`；数据库改动后的 Go 格式与门槛尚未整理。

### 当前失败 / 尚未完成

- 最新全 internal 编译检查失败，见上节和 `/tmp/etherview-db-compile.log`。
- 未在最终树运行 `make check`、generate-check/source-check/docs-check/plan-check、全 Go/Web/race。
- 未运行本轮 PostgreSQL integration/integration-race、真实浏览器 E2E、schema/runtime/Hardhat/Foundry/Preview Metadata 验收或性能基准。
- 前一轮只读审查中的临时 Chrome 诊断仅证明原有 SSE 400 行为，不能作为新恢复逻辑的浏览器验收。
- 尚未创建/更新需要的 ADR；P68 新工作项均不能标记 done。

## 7. 临时辅助文件（仅供参考，不可直接重跑）

这些文件在同一主机 `/tmp`，未加入仓库，可能被系统清理。它们不属于产品实现，也不是维护中的生成器。

只读库存脚本：

- `/tmp/etherview-query-inventory.go`：原始 SQL 调用清单。
- `/tmp/etherview-migration-info.go`：Go 类型信息、生成方法参数/结果与原始调用。
- `/tmp/etherview-row-info.go`：Rows 的 Next/Scan/Close/Err 和辅助函数引用。
- `/tmp/etherview-scanner-info.go`：共用 scanner 的参数与目标类型。

曾执行的修改脚本：

- `/tmp/etherview-numeric-migrate.go`
- `/tmp/etherview-convert-direct.py`
- `/tmp/etherview-fix-integer.go`
- `/tmp/etherview-any-migrate.go`
- `/tmp/etherview-name-params.py`
- `/tmp/etherview-convert-rows.py`

辅助数据：`/tmp/etherview-migration-info.json`、`etherview-row-info.json`、`etherview-scanner-info.json`、`etherview-param-positions.json`、`etherview-direct-skips.json`、`etherview-row-skips.json`、`etherview-mappers.json`。

**JSON 中的位置和类型快照已落后于当前源码。修改脚本不幂等，不能直接对当前树重跑。** 若确需继续自动化，先修正脚本、重新生成同一版本的只读快照，再审核应用范围。参数位置 JSON 记录命名前的 SQL 参数身份，有助于核对位置变化；不要无条件覆盖它。

状态快照：`/tmp/etherview-handoff-status.txt`、`/tmp/etherview-handoff-stat.txt`。旧日期或带 pr86、ens、solcjs 等名称的其他 /tmp 日志属于之前任务，不是本轮证据。

## 8. 建议接手顺序

1. 阅读 AGENTS、P68 和本文档，检查 git status；保留现有未提交修改。当前用户命令是停止，只有收到继续指令后才实施。
2. 先恢复全包编译，人工审核上述自动迁移风险；不要在未审查类型/参数顺序前直接跑全量验收或扩大改动。
3. 完成所有 QueryRow helper、QueryContext helper、动态查询和写批处理入口的类型化迁移。持续搜索 `dbgen` 常量执行、`QueryRowContext/QueryContext/ExecContext/PrepareContext`、`rowScanner`、`*sql.Rows`。
4. 对大扫描落实相同快照中的有界 keyset 分批；保留完整结果、行数/字节预算及外部调用前关闭快照。
5. 复核命名参数、nullable 输出、RowsAffected 与整数校验，再移除 SQL 导出、原生查询桥接及废弃扫描器；更新现有测试，不能删掉安全断言换取通过。
6. 整理临时 IIFE/字段赋值与文件大小；生成产物只通过官方生成命令更新。
7. 新增数据库访问 ADR，同步 ADR-0004 的首页版本/恢复契约及必要的验证运行时说明、架构/开发/测试文档；增强 source-check。
8. 补齐前端真实 SSE 恢复和重组 E2E、首页跨副本/旧请求回归，以及任务提交租约丢失分支。
9. 按已批准计划运行完整验证：`make check`、`make test-integration`、`make test-integration-race`、`make test-e2e`、schema/runtime/Hardhat/Foundry/Preview Metadata，以及核心读写基准。使用当前 Makefile 和 docs/testing.md，不复活历史目标。
10. 只有对应实现、回归和适用门槛通过后，更新 P68 各项证据与状态。外部门槛不可用时明确记录 blocker，不能拿早期测试当最终证据。
