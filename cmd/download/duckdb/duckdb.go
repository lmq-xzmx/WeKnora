package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"
)

// duckdbExtensions is the list of DuckDB extensions required by 小炬人's
// data analysis tool. `spatial` is used for layer metadata (st_read_meta)
// so we can enumerate sheet names from Excel files, while `excel` provides
// the dedicated read_xlsx reader with proper type inference.
var duckdbExtensions = []string{"spatial", "excel"}

// maxAttempts: 直连 extensions.duckdb.org 在国内只有 12-24KB/s 且会断，
// 下载失败是常态（不是配置错误），所以要重试。
const maxAttempts = 3

// alreadyInstalled 报告扩展是否已经装好。
// 镜像构建时可以通过 COPY 把预下载好的扩展带进来，这时就不要再联网下载。
func alreadyInstalled(ctx context.Context, db *sql.DB, ext string) bool {
	rows, err := db.QueryContext(ctx,
		"SELECT installed FROM duckdb_extensions() WHERE extension_name = ?", ext)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var installed bool
		if err := rows.Scan(&installed); err == nil && installed {
			return true
		}
	}
	return false
}

// tryInstall 在一个全新的连接上装扩展。
// 下载失败会把连接搞坏（报 "Failed to read connection"），所以每次重试都重开连接。
func tryInstall(ctx context.Context, ext string) error {
	db, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		return err
	}
	defer db.Close()

	if _, err := db.ExecContext(ctx, fmt.Sprintf("INSTALL %s;", ext)); err != nil {
		return fmt.Errorf("install %s: %w", ext, err)
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf("LOAD %s;", ext)); err != nil {
		return fmt.Errorf("load %s: %w", ext, err)
	}
	return nil
}

func downloadExtensions() {
	ctx := context.Background()

	// 可选：自定义扩展仓库（内网/国内镜像）
	if repo := os.Getenv("DUCKDB_EXTENSION_REPO"); repo != "" {
		db, err := sql.Open("duckdb", ":memory:")
		if err == nil {
			if _, err := db.ExecContext(ctx, fmt.Sprintf("SET custom_extension_repository='%s'", repo)); err != nil {
				fmt.Fprintf(os.Stderr, "设置扩展仓库 %s 失败: %v\n", repo, err)
			}
			db.Close()
		}
	}

	for _, ext := range duckdbExtensions {
		// 已经装好（构建时 COPY 进来的）就跳过，不联网
		db, err := sql.Open("duckdb", ":memory:")
		if err == nil {
			installed := alreadyInstalled(ctx, db, ext)
			db.Close()
			if installed {
				fmt.Printf("扩展 %s 已存在，跳过下载\n", ext)
				continue
			}
		}

		var lastErr error
		ok := false
		for i := 1; i <= maxAttempts; i++ {
			if lastErr = tryInstall(ctx, ext); lastErr == nil {
				ok = true
				break
			}
			fmt.Fprintf(os.Stderr, "扩展 %s 第 %d/%d 次尝试失败: %v\n", ext, i, maxAttempts, lastErr)
			if i < maxAttempts {
				time.Sleep(10 * time.Second)
			}
		}
		if !ok {
			panic(fmt.Errorf("failed to install %s extension after %d attempts: %w", ext, maxAttempts, lastErr))
		}
		fmt.Printf("扩展 %s 安装完成\n", ext)
	}
}

func main() {
	downloadExtensions()
}
