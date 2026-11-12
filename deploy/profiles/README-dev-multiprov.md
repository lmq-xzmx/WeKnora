# 开发机多 Casdoor + 生产数据迁移（2026-11-12）

> 背景：一套开发机 WeKnora（localhost:5015/5016）要同时服务多个生产机
> （36.212.140.68 = 457，36.213.66.254 = 434），按 **Casdoor 账号**区分登录入口，
> 并把各生产机的 WeKnora 数据各迁一套到开发机，以更真实地模拟生产。

## 一、多 Casdoor（登录入口）

### 配置

场景文件（**含 secret，不入 git**）：`deploy/profiles/multiprov.local.env`

```bash
OIDC_AUTH_ENABLE=true
OIDC_AUTH_PROVIDERS_JSON='[
  {"id":"zkju","display_name":"中科矩账号（457 生产机）",
   "issuer_url":"http://36.212.140.68:3012","client_id":"6afaa316e54e775f5152","client_secret":"…"},
  {"id":"edu","display_name":"教育账号（434 生产机）",
   "issuer_url":"http://36.213.66.254:3012","client_id":"6652addd1eecb053980a","client_secret":"…"},
  {"id":"zhaoyun","display_name":"兆云账号（881 生产机）",
   "issuer_url":"http://36.140.179.34:3012","client_id":"70404d353ca3fd4331d7","client_secret":"…"}
]'
```

启动：

```bash
./scripts/run-local.sh --profile multiprov.local --bg
```

### 四个入口

| 路径 | 去向 | 说明 |
|------|------|------|
| `http://localhost:5015/login` | 登录页：**3 个 IdP 按钮 + 1 个「本地账号登录（开发机）」** | 常规入口 |
| `http://localhost:5015/zkju/login` | 36.212.140.68:3012（457 Casdoor） | 路径直达，跳过按钮 |
| `http://localhost:5015/edu/login` | 36.213.66.254:3012（434 Casdoor） | 路径直达，跳过按钮 |
| `http://localhost:5015/zhaoyun/login` | 36.140.179.34:3012（881 Casdoor） | 路径直达，跳过按钮 |
| `http://localhost:5015/dev/login` | **本地账号**（用户名密码，不走 OIDC） | 开发机自己的入口 |

前端改动（`frontend/src/router/index.ts` + `views/auth/Login.vue`）：
新增路由 `/:provider/login`；登录页读到 `route.params.provider` 后
直接调 `handleOIDCLogin(provider)` 跳对应 IdP；`provider=dev|local` 切到本地账号表单。
后端没配该 IdP 时**留在登录页提示**，不会静默跳到别家。

> OIDC 开启时原本会把本地表单 `v-if` 掉，现改为「点『本地账号登录（开发机）』或走
> /dev/login 才显示」，生产实例不受影响（生产仍是纯 OIDC）。

### Casdoor 应用登记（已改）

| Casdoor | 应用 | 改了什么 |
|---------|------|---------|
| 434（36.213.66.254） | 「开发机for：43410253：weknora」`6652addd1eecb053980a` | redirect_uris 原本只有 `localhost:9000/callback`（老端口），已补 `localhost:5015` / `127.0.0.1:5015` / `localhost:5016` |
| 881（36.140.179.34） | 「生产机：weknora知识库」`70404d353ca3fd4331d7` | 追加 `localhost:5015` / `127.0.0.1:5015`（生产用户的 redirect 不变） |

> 434 的生产应用 `yafiqg2udblos57q` 的 redirect_uri 只有公网地址，**不能**用于开发机登录。
> 881 的 Casdoor DB 角色是 **root**（不是 postgres/casdoor）——查应用时别踩。


## 二、生产数据迁移（434 + 457 → 开发机）

### 租户映射

WeKnora 的 OIDC 登录**按 email 找用户**（下表 email 一律用占位符 `acct-*`，真实账号邮箱不入库）（`userService.issueTokensForOIDCUser`），
所以「按账号分数据」= **按 email 归并**，撞 email 的账号共用同一个 dev 用户与租户。

| 来源 | 源租户 | → dev 租户 | 依据 |
|------|--------|-----------|------|
| 434 | 10000 acct-434-admin | **10100** | 与 434 的 admin 同 email → 同一账号 |
| 434 | 10001 / 10002 / 10003 | 10101 / 10102 / 10103 | 新建 |
| 457 | 10000 acct-434-admin | **10100** | 与 434 的 admin 同 email → 合并进 10100 |
| 457 | 10001 acct-457-super | **10001** | dev 已有同 email（zhongkejusuper）→ 合并 |
| 457 | 10002 acct-dev-owner | **10000** | dev 已有同 email（xzmx2025）→ 合并 |
| 457 | 10003 / 10004 / 10005 / 10006 / 10007 | 10203 / 10204 / 10205 / 10206 / 10207 | 新建 |

迁入的 KB 名称统一加后缀「（434生产）」/「（457生产）」，避免与 dev 原生同名混淆。

### 迁移了什么

- 表：tenants / users / storage_backends / knowledge_bases / knowledges / chunks /
  embeddings / sessions / messages / wiki_folders / wiki_pages / wiki_page_revisions /
  resources / resource_bindings / knowledge_processing_spans / custom_agents / tenant_members
- 文件：
  - minio：434 的 `weknora-files` → dev 的 `weknora-files/<目标租户>/`（356 个对象）
  - local：434 的 `/data/files/10000` → dev 的 `.local-data/files/10100/`（356 个文件）
  - 457 的 minio 对象在生产上已大量缺失（`weknora-files` 只剩 2 个），检索不受影响（正文在 chunks）

### 迁移步骤（可复现）

1. 远端 `pg_dump --data-only -Fp` 导出目标表 → scp 到本地
2. dev 建 staging schema（`mig434` / `mig457`），`CREATE TABLE ... (LIKE public.x INCLUDING ALL)`
   —— 457 的 chunks 比 dev 多一列 `source_locators jsonb`，需先 `ALTER TABLE ... ADD COLUMN`
3. dump 里的 `COPY public.x` 改写成 `COPY migNNN.x` 后导入 staging
4. 生成迁移 SQL：按上表做 tenant_id 映射；`chunks.seq_id` / `embeddings.id` /
   `knowledge_processing_spans.id` 用 dev 自己的序列发号（否则撞唯一索引）
5. `resources.physical_path` 里的租户段同步改写；桶名统一成 `weknora-files`
6. `storage_backends` 的 config 换成 dev 的 minio 密钥对 + `bucket_name=weknora-files`
   —— 注意唯一索引是 `(name, tenant_id)`，迁移的 backend 要改名（如「System MINIO（457生产）」）
7. `tenant_members` 单独插（434/457 各一份，user_id 按 email 归并后重算）
8. 迁入的 KB 要 `UPDATE knowledge_bases SET indexing_strategy = … vector_enabled/keyword_enabled = true`
   —— 生产侧这两个开关可能是关的，dev 里打开才能检索

### 已知缺口：向量没迁过来

生产的向量在 **Qdrant**（2026-11-10 已停用），PG 的 `embeddings` 表里几乎没数据，
所以迁入的 chunk **没有向量**，关键词/向量检索都返回空。

补向量用 WeKnora 自带的维护端点：

```bash
POST /api/v1/knowledge-bases/:id/maintenance/plan   {"action":"backfill_embeddings"}
POST /api/v1/knowledge-bases/:id/maintenance/run    {"action":"backfill_embeddings"}
```

（需要带 `manage_kbs` capability 的 key。）

**当前阻塞（已定位，2026-11-12 修正早前误判）**：dev 的 `builtin-bge-m3` 指向
`http://100.96.230.105:3081`（457 的模型网关）。早前以为「请求卡在 nginx 转发层」，
实测推翻：

- 457 本机 `POST 127.0.0.1:3081/embeddings`（无 key）→ **1ms 返回 401**（转发层正常）
- 457 的 bge 服务 `journalctl -u bge-m3` 里能看到 dev 机的 `POST /embeddings 200 OK`
  → **embedding 服务一直是好的**

真正的失败在 WeKnora 侧：`batch index [0:200] failed: context deadline exceeded`
（`kb_maintenance.go` 的 `backfillBatchTimeout = 10 分钟`）。
**默认批量 200 条太大** → 一批算不完 10 分钟 → 整批作废。

### 最终解法：在开发机本地算向量（正文不出本机）

`ollama pull bge-m3`（1.2GB）→ 本机 ollama 跑 **bge-m3，1024 维，与生产 457 池同模型同维度**。

1. `models` 表新增 `builtin-bge-m3-local`（tenant_id=10100，source=local，
   `base_url=http://localhost:11434`，`dimension=1024`）
2. 迁入的 6 个库 `embedding_model_id` → `builtin-bge-m3-local`
3. **`.env` 的 `OLLAMA_BASE_URL` 要从 `http://host.docker.internal:11434` 改成
   `http://127.0.0.1:11434`** —— 前者是给容器部署用的默认值，开发机上解析不了
   （表现为 `lookup host.docker.internal: no such host`）。改完重启 WeKnora。
4. 回填：

```bash
bash scripts/backfill-434-embeddings.sh     # batch_size=16 + 进度轮询，临时 key 跑完自动删
```

实测：1197 chunks / 75 次 embedding 调用 / 0 错误；向量 1024 维全部落库；
查「表现手法有哪几种」命中 69 条，Top1《表现手法-相关简介.md》score 0.965。


## 四、praxis 已切到 434 生产的答题方法库（2026-11-12）

| 项 | 值 |
|----|---|
| 读侧库 `PRAXIS_KB_ID` | `4606a393-9165-4acc-9961-ca387c2d6da1`（praxis-答题方法库（434生产），21 篇）|
| 写侧库 `PRAXIS_KB_ID_WRITE` | `b930762e-b135-4b73-973f-119082382c21`（praxis-学员经验库（434生产））|
| API Key | `tenant_api_keys.name = 'praxis-dev-434'`，tenant_id=10100，白名单=上面两个库，能力 retrieve/ingest/chat/message_history/manage_kbs |

明文只落在 `apps/praxis/backend/.env`（不入 git）。改完 `.env` 要重启后端：
`python3 -m uvicorn app.main:app --app-dir <repo>/apps/praxis/backend --host 127.0.0.1 --port 5018`

验证：`GET http://127.0.0.1:5018/api/v1/methods` → 21 条，题型分组与 434 生产一致。

> ⚠️ dev 的 WeKnora 里**同时存在**两套 praxis 答题方法库：原生的 `6e6788ed…`（dev 自建，0 篇）
> 与迁入的 `4606a393…`（434 生产，21 篇）。别切错。


## 五、去重：dev 原生库以生产为准（2026-11-12）

dev 里曾有 dev 自建的 `praxis-答题方法库`（`6e6788ed…`，20 篇）与迁入的
`4606a393…`（21 篇）并存。按「以生产为准」已软删 dev 原生那份：

| 已软删 | 保留（生产版） |
|--------|----------------|
| `6e6788ed` praxis-答题方法库（20 篇） | `4606a393` praxis-答题方法库（434生产，21 篇） |
| `68efe6a1` __chat_history__（0 篇） | `696e9e40` / `3d22225a` / `c7f803ca`（生产迁入） |

- 备份表：`bak_deleted_kb_20261112` / `bak_deleted_20261112`（可恢复）
- 引用了被删库的 API Key 白名单已同步改指生产版（`geo-k-integration`）
- **未删**：`geo-k-v1-test` / `geo-monitor` / `小小知识库2` —— 生产没有同名版，
  是 dev 自有测试库，不在「同名以生产为准」范围内（另有几个重复上传的空库，可另行清理）
