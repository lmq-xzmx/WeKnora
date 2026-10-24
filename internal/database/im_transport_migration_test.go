// Package database implements database initialization and migrations.
package database

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/testutil"
)

func TestDingtalkStreamMigrationPreservesOtherChannels(t *testing.T) {
	db := testutil.OpenTestDB(t)

	require.NoError(t, db.Exec("CREATE TABLE im_channels (id TEXT, platform TEXT, mode TEXT, updated_at TIMESTAMPTZ)").Error)
	require.NoError(t, db.Exec("INSERT INTO im_channels VALUES "+
		"('d', 'dingtalk', 'webhook', NULL), "+
		"('s', 'slack', 'webhook', NULL), "+
		"('w', 'dingtalk', 'websocket', NULL)").Error)

	script, err := os.ReadFile(filepath.Join(repoRoot(t), "migrations", "versioned", "000096_dingtalk_stream_only.up.sql"))
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(script)).Error)

	for id, expected := range map[string]string{"d": "websocket", "s": "webhook", "w": "websocket"} {
		var mode string
		require.NoError(t, db.Raw("SELECT mode FROM im_channels WHERE id = ?", id).Scan(&mode).Error)
		require.Equal(t, expected, mode)
	}
}
