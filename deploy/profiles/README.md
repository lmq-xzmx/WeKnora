# WeKnora 场景配置（profiles）

> 一套 WeKnora 代码服务多个客户/场景，**差异全在配置** —— 切场景 = 换一个
> `.env` 文件，不改代码、不切 git 分支。

## 用法

```bash
# 默认（读仓库根 .env）
./scripts/run-local.sh --bg

# 指定场景
./scripts/run-local.sh --profile zkju --bg
```

加载顺序（后者覆盖前者）：
```
.env  →  deploy/profiles/<profile>.env  →  命令行临时 export
```

## 现有场景

| profile | 用途 | 登录方式 |
|---------|------|---------|
| （无） | 默认。仓库根 `.env` 的配置 | 单 IdP |
| `zkju` | 中科矩（企业信息化） | 单 IdP → 457 Casdoor |
| `edu-multiprov` | 多 IdP 演示：中科矩 + 教育同时接入 | 多 IdP → 登录页两个按钮 |

## 配置项

`deploy/profiles/*.env` 是**普通 env 文件**（`KEY=VALUE`），在 `.env` 之后加载，
所以这里的值优先。WeKnora 用 viper 读配置，`AutomaticEnv` + `.`→`_` 替换，
所以 `server.port` 对应环境变量 `SERVER_PORT`，`oidc_auth.enable` 对应
`OIDC_AUTH_ENABLE`。

### 多 IdP（一个部署接多个客户的 Casdoor）

```bash
OIDC_AUTH_PROVIDERS_JSON=[{"id":"zkju","display_name":"中科矩账号","issuer_url":"http://36.212.140.68:3012","client_id":"..."},{"id":"edu","display_name":"教育账号","issuer_url":"http://36.213.66.254:3012","client_id":"..."}]
```

- 每项一个 IdP；**字段缺失时继承 `.env` 里的单 provider 字段**
- 登录页按 `providers` 渲染按钮；点某个按钮 → `/auth/oidc/url?provider=<id>`
- 回调从**服务端签名过的 state** 取回 provider → 用对应 client_id 换 token
- **不配这个变量** → `Providers` 为空 → 登录页仍是一个按钮、文案不变
  （这就是「生产用户无感」的技术保证）

### 各客户在 Casdoor 里的应用登记

每个客户在**自己的** Casdoor 里注册一个应用（redirect_uri 填本机回调）：

```
http://localhost:5015/api/v1/auth/oidc/callback
```

客户各自的 Casdoor **不用改** —— 我们只是用不同的 client_id 去连它们。

## 踩坑记录

### 1. profile 文件里的 JSON 必须用单引号包裹

profile 是被 shell `source` 的 —— `KEY=[{"a":"b"}]` 里的双引号会被 shell
当引号处理剥掉，JSON 变成 `{a:b}` → 解析失败 → **静默回落到单 IdP**
（页面看起来"配置没生效"，但没有任何报错）。

正确写法：`KEY='[{"a":"b"}]'`

### 2. 改了 Go 代码但二进制没重编 → 新功能"配了不生效"

`run-local.sh` 原来用**目录** mtime 判断是否重编译：
`[ "$REPO/internal" -nt "$BIN" ]`。但改的是目录**里的** .go 文件，
目录 mtime 不变 → 旧二进制一直被复用。

2026-11-09 排查这个坑花了很久（env 明明在进程里、格式也对，但 config 读不到）。

已修：改为检查**具体文件**的时间戳
（cmd/server/main.go / internal/config/config.go / internal/handler/auth.go /
internal/application/service/user.go）。

**手动验证新功能时，先确认二进制是新的**：
`ls -l /tmp/WeKnora-local` 看时间戳 vs 你改的文件时间。

## 凭据处理

`OIDC_AUTH_PROVIDERS_JSON` 里含 client_secret 时**不要写进文件**（会进 git）。
用环境变量注入：

```bash
export EDU_MULTI_PROVIDERS_JSON='[...]'
./scripts/run-local.sh --profile edu-multiprov --bg
```

或在 profile 文件里引用：`OIDC_AUTH_PROVIDERS_JSON=${EDU_MULTI_PROVIDERS_JSON}`。
