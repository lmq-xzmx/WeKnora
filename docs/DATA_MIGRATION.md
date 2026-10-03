# WeKnora 数据迁移与传导指南

> 适用版本：WeKnora v0.7+ · Postgres 17 + ParadeDB · Redis 7 · MinIO
> 文档目的：把"换镜像 / 换环境 / 换版本 / 跨容器拷贝数据卷"等数据迁移场景的
> 风险点和正确做法一次性写清，避免下次踩坑。

---

## 一、WeKnora 涉及的数据服务

| 服务 | 用途 | 数据丢失后果 |
|---|---|---|
| **postgres + paradedb** | 主库 + 向量检索（pgvector） | 全部知识库元数据 + 向量索引丢失 |
| **redis** | Asynq 队列 / 配置缓存 / PubSub | 任务队列、解析任务状态丢失（库文件不受影响） |
| **minio** | S3 兼容对象存储 | 所有上传的原版 PDF/图片/音频丢失 |
| milvus / qdrant / opensearch / neo4j / doris / clickhouse / elasticsearch / tidb / mysql | 可选 profile，按需启用 | 各自业务数据 |
| langfuse | 可选 profile，LLM 观测 | 观测/trace 数据 |

**核心数据有三类**：
1. **结构化业务数据**（postgres + paradedb）
2. **文件原版**（minio）
3. **临时状态**（redis，可重建）

---

## 二、迁移的核心风险

### 2.1 版本兼容性（最核心）

| 风险 | 说明 |
|---|---|
| 目标镜像版本 < 源镜像版本 | 数据库文件格式不兼容，启动直接失败 |
| Postgres 主版本升级（13→17） | 须 `pg_upgrade` 或全量 dump/restore |
| ParadeDB / pgvector 扩展升级 | 索引格式可能变，需要 `REINDEX` |
| Redis 大版本升级 | AOF/RDB 格式可能变，需 `redis-check-aof --fix` |
| Milvus 跨小版本 | 元数据结构在 etcd，迁移必须停服 |

### 2.2 参数一致性（最隐蔽）

迁移两端必须**完全一致**的关键配置：

| 参数 | 用途 | 不一致后果 |
|---|---|---|
| `lower_case_table_names` | 表名大小写敏感 | 容器内外迁移时表名找不到 |
| `collation_server` | 字符集排序规则 | 索引扫描结果不一致 |
| `time_zone` | 时区 | 时间字段误差 |
| `sql_mode` | SQL 严格模式 | 严格模式关闭后旧数据可能写入失败 |
| `innodb_strict_mode` | 严格数据校验 | 关闭会让 schema 错误默默接受 |
| `max_allowed_packet` | 单包大小 | 大文档导入失败（默认 4 MB 太小） |
| `shared_buffers` | 共享缓冲池 | 性能 + OOM |
| Redis `appendonly` | AOF 持久化 | 关闭则重启丢队列任务 |
| Redis `maxmemory-policy` | 淘汰策略 | 默认 noeviction 会让写阻塞 |

### 2.3 数据卷直接挂载的限制

- **Postgres**：目标镜像版本 ≥ 源镜像版本即可；否则启动报错"incompatible database"
- **Redis**：使用 MySQL AOF + RDB 的版本字段；混用可能报错"Bad file format reading the append only file"
- **MinIO**：数据目录结构与版本弱绑定；升级通常安全，但回滚需小心
- **向量库**（milvus / qdrant / weaviate）：**不要直接拷贝数据卷**，用官方 export/import 工具

---

## 三、按场景的迁移方案

### 3.1 同 Postgres 版本，跨主机迁移

```bash
# 源端：全量 dump（推荐并行加速）
pg_dump -h <src> -U <user> -d <db> -Fc -j 4 -f weknora.dump

# 目标端：先确保目标 postgres 与源同大版本，然后 restore
pg_restore -h <dst> -U <user> -d <db> -j 4 weknora.dump

# pgvector 索引需要在 restore 后手动重建（dump 通常不含）
psql -h <dst> -U <user> -d <db> \
    -c "REINDEX INDEX <idx_name>;"
```

### 3.2 Postgres 跨大版本升级（如 16→17）

```bash
# 推荐路径（停机窗口短）
1. 源端 pg_dump --schema-only → 手工审阅 schema，删除已废弃语法
2. 目标端用新主版本起一个空容器，restore schema
3. pg_dump data-only（按表分批）→ restore 到新库
4. 验证 REINDEX、ANALYZE、扩展兼容性

# 备选：pg_upgrade（in-place，需停服）
1. 旧版本 stop
2. 新版本容器以旧数据目录挂载
4. pg_upgrade --link 模式（秒级切换）
5. 启动新版本
```

### 3.3 ParadeDB / pgvector 扩展升级

```bash
# ParadeDB 通常要求对应 Postgres 大版本，必须配套升级
# 升级流程：
1. 备份（pg_dump）
2. 升级 postgres 镜像 + paradedb 镜像
3. 启动新版本，自动跑 paradedb 内部升级脚本
4. 验证：SELECT extname, extversion FROM pg_extension;
5. REINDEX pgvector 索引（必要时）
```

### 3.4 Redis 数据迁移

```bash
# 方案 A：AOF 复制（推荐）
1. 源端 BGSAVE → dump.rdb
2. 目标端挂载相同 AOF 文件（同大版本 Redis）

# 方案 B：redis-cli 跨主机复制
redis-cli -h <src> -p 6379 --rdb /tmp/dump.rdb
# 目标端停服 → 替换 dump.rdb → 启动

# 方案 C：跨大版本升级
1. 旧版 redis-cli --rdb dump.rdb
2. 新版 redis-server 启动时加载 dump.rdb
3. 旧 AOF 文件先备份，新版 redis-check-aof --fix <aof-file>
```

### 3.5 MinIO 数据迁移

```bash
# 方案 A：mc mirror（S3 协议层）
mc alias set src http://<src>:9000 <ak> <sk>
mc alias set dst http://<dst>:9000 <ak> <sk>
mc mirror --preserve --remove src/<bucket> dst/<bucket>

# 方案 B：直接拷贝数据卷（仅同版本可用）
docker run --rm \
    -v src_data:/from:ro \
    -v dst_data:/to \
    alpine cp -a /from/. /to/

# 注意：跨版本时检查 .minio.sys/config 兼容性
```

### 3.6 向量库迁移（Milvus / Qdrant / Weaviate）

**不要**直接拷贝数据卷。必须用官方工具：

```bash
# Milvus
1. milvus-backup create --database default --minio_endpoint ...
2. milvus-backup restore ...

# Qdrant
1. QDRANT__SERVICE__API_KEY=xxx qdrant snapshot create --collection <name>
2. scp snapshot 到目标
3. qdrant snapshot recover --collection <name> <snapshot-file>

# Weaviate
1. weaviate-backup
2. 目标 restore
```

### 3.7 Neo4j 迁移

```bash
# 必须停服或用官方备份工具
neo4j-admin dump --database=neo4j --to=/backup/neo4j.dump
# 目标
neo4j-admin load --from=/backup/neo4j.dump --database=neo4j --force
```

---

## 四、参数一致性检查清单（迁移前必做）

### Postgres

```sql
-- 源端导出
SHOW server_version;
SHOW data_directory;
SELECT name, setting FROM pg_settings
WHERE name IN (
    'lower_case_table_names',
    'collation_server',
    'lc_collate', 'lc_ctype',
    'timezone',
    'sql_mode',
    'innodb_strict_mode',
    'max_allowed_packet',
    'shared_buffers',
    'wal_level',
    'max_replication_slots',
);

-- 列出已装扩展（pgvector / paradedb 套件）
SELECT extname, extversion FROM pg_extension ORDER BY extname;
```

### Redis

```bash
redis-cli INFO server | grep redis_version
redis-cli CONFIG GET appendonly
redis-cli CONFIG GET maxmemory-policy
redis-cli CONFIG GET maxmemory
```

### MinIO

```bash
# mc admin info <alias> 看版本
mc admin info src
```

---

## 五、WeKnora 项目内的可优化点

### 5.1 docker-compose.dev.yml 已浮动 tag 修复（已完成）

修复前：
```yaml
image: searxng/searxng:latest
image: minio/minio:latest
image: neo4j:latest
image: dexidp/dex:latest
```

修复后：
```yaml
image: searxng/searxng:2025.06.13-1e0b49f6
image: minio/minio:RELEASE.2025-09-07T16-13-09Z
image: neo4j:2025.10.1
image: dexidp/dex:v2.43.1
```

### 5.2 未来 TODO

- **生产化阶段**：把 dev compose 与 prod compose 镜像版本统一（避免开发/生产漂移）
- **Postgres 卷迁移 SOP**：写一份 `scripts/migrate-postgres-volume.sh` 一键化操作
- **CI 集成 SBOM**：`syft scan <image> -o cyclonedx-json` 输出 SBOM，配合 `grype` 做漏洞扫描
- **备份策略**：postgres 每日全量 + 7 天保留；MinIO `mc mirror` 异地同步

---

## 六、紧急情况（容器内 Postgres 启动失败）

```bash
# 错误：FATAL: database files are incompatible with server
# 错误：The data directory was initialized by PostgreSQL version X, which is not compatible with this version Y

# 临时解决：升级源数据目录版本（需要 Y > X）
docker run --rm \
    -v postgres_data_dev:/data \
    postgres:17-alpine \
    pg_upgradecluster 17 main

# 或直接导出重建：
docker exec -e PGPASSWORD=xxx <src-postgres> pg_dumpall -U postgres > full.sql
docker exec -i <dst-postgres> psql -U postgres < full.sql
```

---

## 七、CI/CD 中的迁移检查

建议在部署流水线里加入：

```bash
# 镜像升级前
1. 比较新旧镜像版本
2. 数据库兼容性检查（pg_upgrade --check）
3. 验证扩展版本兼容（paradedb, pgvector, pg_lakehouse）
4. 执行 dry-run 迁移（沙箱环境）
5. 比对 source / target 的 schema 快照
6. 通过后才允许升级
```

---

## 八、参考

- Postgres pg_upgrade：https://www.postgresql.org/docs/current/pgupgrade.html
- pg_dump / pg_restore 最佳实践：https://www.postgresql.org/docs/current/app-pgdump.html
- Redis RDB / AOF：https://redis.io/docs/management/persistence/
- MinIO Client (mc)：https://min.io/docs/minio/linux/reference/minio-mc.html
- Milvus Backup：https://milvus.io/docs/milvus_backup.md
- Qdrant Snapshots：https://qdrant.tech/documentation/concepts/snapshots/
- ParadeDB：https://docs.paradedb.com/