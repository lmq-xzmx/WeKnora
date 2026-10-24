package repository

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/testutil"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestKnowledgeSourceSchemaAllowsObjectStorageURLs(t *testing.T) {
	db := testutil.OpenTestDB(t, &types.Knowledge{})

	// SQLite 的 PRAGMA table_info 在 PG 上不存在，改查
	// information_schema.columns —— PG 侧的类型名是 character varying，
	// 长度则单独存在 character_maximum_length 里。
	var dataType string
	var maxLength *int
	if err := db.Raw(
		`SELECT data_type, character_maximum_length FROM information_schema.columns
		 WHERE table_schema = current_schema() AND table_name = 'knowledges' AND column_name = 'source'`,
	).Row().Scan(&dataType, &maxLength); err != nil {
		t.Fatalf("inspect knowledge schema: %v", err)
	}
	sourceType := strings.ToLower(dataType)
	if maxLength != nil {
		sourceType = fmt.Sprintf("%s(%d)", sourceType, *maxLength)
	}
	if sourceType != "character varying(2048)" {
		t.Fatalf("knowledge source column type = %q, want character varying(2048)", sourceType)
	}

	longURL := "https://example-bucket.cos.ap-beijing.myqcloud.com/test/" +
		strings.Repeat("encoded-path-segment-", 20) + ".docx"
	knowledge := &types.Knowledge{
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		Type:            types.KnowledgeTypeManual,
		Title:           "long source",
		Source:          longURL,
		ParseStatus:     types.ParseStatusPending,
		EnableStatus:    "enabled",
	}
	if err := db.Create(knowledge).Error; err != nil {
		t.Fatalf("create knowledge with long source: %v", err)
	}
}
