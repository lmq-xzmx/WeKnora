package testutil

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestOpenTestDBIsolatesSchemas 验证 fixture 能连上 postgres、AutoMigrate
// 生效，并且两个测试拿到的是互相隔离的 schema。
func TestOpenTestDBIsolatesSchemas(t *testing.T) {
	db := OpenTestDB(t)
	require.NotNil(t, db)

	var currentSchema string
	require.NoError(t, db.Raw("select current_schema()").Scan(&currentSchema).Error)
	require.Equal(t, SchemaName(t.Name()), currentSchema)
}

func TestSchemaNameIsValid(t *testing.T) {
	// 非法字符要被替换掉，且结果不能以数字开头
	require.Equal(t, "t_Test_OpensDB", SchemaName("Test/OpensDB"))
	require.Equal(t, "t_", SchemaName(""))
	require.LessOrEqual(t, len(SchemaName(string(make([]byte, 200)))), 63)
}
