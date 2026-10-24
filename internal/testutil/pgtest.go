// Package testutil 提供测试用的公共辅助。
//
// 背景：WeKnora 过去用 SQLite 内存库做单元测试的 fixture。但 SQLite 与
// PostgreSQL 在 SQL 方言上有实质差异（AutoMigrate 生成的列类型、事务
// 隔离、并发写、pgvector 的向量列），SQLite 上通过的断言在
// PostgreSQL 上未必成立，反之亦然 —— 迁移目录里连
// migrations/sqlite/ 都已经不存在，测试却还在引用它。
//
// 规划口径（见「数据库.md」§1.1/§3.5）：WeKnora 只跑 PostgreSQL(pgvector)，
// 不保留 SQLite 分支。因此测试 fixture 也统一到 PostgreSQL，
// 让测试跑在和线上一致的引擎上。
//
// 连接目标：默认连本机 dev-postgres 的 weknora_test 库（ParadeDB PG17，
// 含 pgvector）。可用环境变量覆盖：
//
//	WEKNORA_TEST_PG_DSN    完整 DSN（优先）
//	WEKNORA_TEST_PG_HOST   默认 127.0.0.1
//	WEKNORA_TEST_PG_PORT   默认 15432
//	WEKNORA_TEST_PG_USER   默认 postgres
//	WEKNORA_TEST_PG_PASS   默认 postgres
//	WEKNORA_TEST_PG_DB     默认 weknora_test
//
// 每个测试拿到的是自己的 schema（见 OpenTestDB），测试之间互不干扰，
// 结束后自动 DROP。
package testutil

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// dsnOnce 保证只在第一次需要时解析 DSN 并检测可达性 —— 没有 postgres
// 可用时，测试应该明确失败并说明原因，而不是每个 fixture 静默跳过。
var (
	dsnOnce sync.Once
	dsnVal  string
	dsnErr  error
)

// TestDSN 返回测试用 PostgreSQL 的 DSN。
func TestDSN() (string, error) {
	dsnOnce.Do(func() {
		// .env.test 里存着本机测试实例的连接参数（含密码），不读它的话
		// 每次跑测试都得手动 export 一遍。它已被 .gitignore 忽略。
		//
		// 从当前目录一路往上找，测试包嵌套多深都能找到仓库根的 .env.test
		// （go test 的工作目录是包目录，不是仓库根）。
		dir, err := os.Getwd()
		if err != nil {
			dsnErr = err
			return
		}
		for {
			loadEnvFile(filepath.Join(dir, ".env.test"))
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}

		if dsn := strings.TrimSpace(os.Getenv("WEKNORA_TEST_PG_DSN")); dsn != "" {
			dsnVal = dsn
			return
		}
		host := envOr("WEKNORA_TEST_PG_HOST", "127.0.0.1")
		port := envOr("WEKNORA_TEST_PG_PORT", "15432")
		user := envOr("WEKNORA_TEST_PG_USER", "postgres")
		pass := envOr("WEKNORA_TEST_PG_PASS", "postgres")
		db := envOr("WEKNORA_TEST_PG_DB", "weknora_test")
		dsnVal = fmt.Sprintf(
			"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
			host, port, user, pass, db,
		)
	})
	return dsnVal, dsnErr
}

// loadEnvFile 把 KEY=VALUE 形式的行读进环境变量。
//
// 已存在的环境变量优先（显式设置的值比文件里的权威），文件不存在时静默跳过。
func loadEnvFile(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, val)
		}
	}
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// OpenTestDB 为一个测试打开一个独立的 PostgreSQL 连接。
//
// 每个测试拿到自己的 schema：schema 名由调用方传入（通常是 t.Name() 的
// 清洗版），建好后把 search_path 指过去。这样多个测试并行跑时不会
// 互相看到对方的表，测试结束后 schema 被 DROP。
//
// search_path 是通过 DSN 参数（而不是 `SET search_path` 语句）下发的：
// GORM 的连接池会开多条连接，而 `SET` 只对执行它的那一条 session 生效，
// 池里后续新建的连接会退回 public schema，测试就会看到别人的表。
//
// 注意：schema 名有 63 字符上限，且不能以数字开头 —— 这里统一做清洗。
func OpenTestDB(t *testing.T, models ...interface{}) *gorm.DB {
	t.Helper()

	dsn, err := TestDSN()
	require.NoError(t, err, "测试 DSN 解析失败")

	schema := SchemaName(t.Name())

	// search_path 指向一个还不存在的 schema 也能连上 —— 标识符解析是
	// 查询时才做的，schema 存在与否不影响连接建立。
	db, err := gorm.Open(postgres.Open(withSearchPath(dsn, schema)), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err, "连不上测试 PostgreSQL（%s）：请确认 dev-postgres 在跑，"+
		"或用 WEKNORA_TEST_PG_DSN 指定其他实例", redactDSN(dsn))

	sqlDB, err := db.DB()
	require.NoError(t, err)
	// 连接池默认按需扩张，几十个测试各开一个池很容易把 postgres 的
	// max_connections 撑满 —— 限制住，测试之间也不会互相抢连接。
	sqlDB.SetMaxOpenConns(4)

	// 先清干净再建：上一次运行如果在这个测试中途崩了（或者 DROP 失败），
	// 残留的 schema 里还留着旧数据，CREATE IF NOT EXISTS 会直接复用它，
	// 于是测试莫名其妙撞上"主键重复"这类跟代码无关的失败。
	require.NoError(t, db.Exec(`DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`).Error)
	require.NoError(t, db.Exec(`CREATE SCHEMA "`+schema+`"`).Error)

	t.Cleanup(func() {
		// 用独立连接删 schema：测试可能已经把自己的连接池关掉了
		// （比如模拟数据库故障的用例），在关掉的池上执行 DROP 会失败，
		// schema 就留在库里，下次跑同一个用例会撞上残留的表。
		cleanup, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
			Logger: logger.Default.LogMode(logger.Silent),
		})
		if err == nil {
			if cleanupDB, err := cleanup.DB(); err == nil {
				defer cleanupDB.Close()
			}
			_ = cleanup.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`).Error
		}
		_ = sqlDB.Close()
	})

	if len(models) > 0 {
		require.NoError(t, db.AutoMigrate(models...), "AutoMigrate 失败")
	}
	return db
}

// withSearchPath 给 DSN 加上 search_path 参数。
//
// DSN 可能是 key=value 形式，也可能是 postgres:// URL —— 两种都要能改。
//
// schema 名必须加双引号：search_path 的值是一串标识符，不加引号时
// PostgreSQL 会把它折叠成小写，schema 名里的驼峰就再也匹配不上了。
func withSearchPath(dsn, schema string) string {
	value := `"` + schema + `",public`
	if strings.Contains(dsn, "://") {
		u, err := url.Parse(dsn)
		if err != nil {
			return dsn
		}
		q := u.Query()
		q.Set("search_path", value)
		u.RawQuery = q.Encode()
		return u.String()
	}
	return dsn + ` search_path='` + value + `'`
}

// SchemaName 把测试名清洗成合法的 PostgreSQL schema 名。
func SchemaName(testName string) string {
	var b strings.Builder
	b.WriteString("t_")
	for _, r := range testName {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	s := b.String()
	// PostgreSQL 标识符上限 63 字节
	if len(s) > 63 {
		s = s[:63]
	}
	return s
}

// redactDSN 去掉 DSN 里的密码，用于错误信息。
func redactDSN(dsn string) string {
	fields := strings.Fields(dsn)
	for i, f := range fields {
		if strings.HasPrefix(f, "password=") {
			fields[i] = "password=***"
		}
	}
	return strings.Join(fields, " ")
}
