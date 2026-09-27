# 数据库定时备份（dbbackup）

Multica 服务端内置每日定时备份：对 `DATABASE_URL` 指向的 PostgreSQL 数据库执行 `pg_dump`，产物为 custom 格式（`-Fc`，内部已压缩）的单文件归档，按时间戳命名落盘到配置目录，并按保留天数滚动清理过期备份（RUYI-237）。

## 运行机制

- 服务端进程内的后台 goroutine 按配置周期（默认 24h）触发一轮备份：`pg_dump -Fc -d <DATABASE_URL> -f <临时文件>` 成功后原子改名落盘，随后执行一轮保留清理。
- 进程启动时若目录内最新备份已超过一个周期（或尚无备份），立即补跑一轮，因此服务频繁重启不会漏掉当天备份，也不会重复堆积备份。
- 归档命名：`multica-db-YYYYMMDD-HHMMSS.dump`（本地时间）；写过程中的临时文件后缀 `.dump.partial`，崩溃残留的过期 partial 同样被滚动清理。
- 单轮备份有超时预算（默认 2h），pg_dump 挂死不会阻塞后续周期。
- 备份失败（pg_dump 缺失、连接失败、磁盘写失败等）以 `ERROR` 级日志暴露，不会静默，也不会中断服务或后续周期。

## 配置项（环境变量，均可选）

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `MULTICA_BACKUP_ENABLED` | `true` | 置 `false` 关闭定时备份 |
| `MULTICA_BACKUP_DIR` | `backups`（相对服务端工作目录） | 归档输出目录（自动创建，权限 0700） |
| `MULTICA_BACKUP_INTERVAL` | `24h` | 备份周期（Go duration） |
| `MULTICA_BACKUP_RETENTION_DAYS` | `7` | 保留天数；恰好到期边界不删，仅清理严格更旧的文件 |
| `MULTICA_BACKUP_PG_DUMP` | `pg_dump`（从 PATH 解析） | pg_dump 可执行文件路径 |
| `MULTICA_BACKUP_TIMEOUT` | `2h` | 单轮备份超时预算 |

备份使用的连接串即服务端自身 `DATABASE_URL`（含回退默认值），无需单独配置。运行服务端的机器或容器内需装有 `pg_dump`，且大版本不得低于数据库服务端大版本（PostgreSQL 官方约束：pg_dump 可向后兼容更低版本服务端，反之不行）。

## 恢复操作手册（从备份包恢复）

### 前置条件

- 目标机装有 `pg_restore`（与 `pg_dump` 同包安装），版本 ≥ 生成备份的 PostgreSQL 大版本（`pg_restore --version` 核对）。
- 归档为 custom 格式，必须用 `pg_restore` 恢复，不能用 `psql` 直接执行。

### 恢复前先核对归档内容

```bash
# 列出归档内的对象清单，确认备份完整、定位需要的表
pg_restore -l /path/to/multica-db-20260927-140305.dump | less
```

### 整库恢复到全新数据库（灾难恢复路径）

```bash
# 1. 创建空的恢复目标库
createdb -h <host> -p <port> -U <user> multica_restored

# 2. 整库恢复（归档生成时已带 --no-owner --no-privileges，恢复时同样携带）
pg_restore -h <host> -p <port> -U <user> \
  --dbname multica_restored \
  --no-owner --no-privileges \
  --exit-on-error \
  /path/to/multica-db-20260927-140305.dump

# 3. 恢复后抽查：行数、最新记录时间与业务核对
psql -h <host> -U <user> -d multica_restored \
  -c "SELECT count(*) FROM issues;" \
  -c "SELECT max(created_at) FROM issues;"
```

说明：

- `pg_restore` 非 `-j` 串行恢复最稳妥；大库可加 `-j <N>` 并行恢复（会牺牲部分错误顺序确定性）。
- `--if-exists` 必须与 `--clean` 成对使用，仅适用于对已有同名对象的库重跑；`--clean` 会先 DROP 目标库同名对象，属破坏性操作，禁止未经确认对生产库使用。恢复永远先落临时库验证。

### 只恢复单张表（数据订正路径）

```bash
# 1. 从清单中确认表在归档内的条目（TABLE + TABLE DATA 两条）
pg_restore -l /path/to/multica-db-20260927-140305.dump | grep -i '<table_name>'

# 2. 仅恢复该表（含 schema 与数据），先落到临时表规避覆盖
pg_restore -h <host> -U <user> --dbname multica_restored \
  --table '<table_name>' --no-owner --no-privileges --exit-on-error \
  /path/to/multica-db-20260927-140305.dump
```

### 恢复演练建议

每季度在一次性库上执行一次「整库恢复 + 抽查」演练并记录耗时；备份只有在演练过恢复后才是可恢复的备份。
