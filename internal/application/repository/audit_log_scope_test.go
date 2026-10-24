package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/testutil"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func TestAuditLogRepositoryListFiltersKnowledgeBaseScope(t *testing.T) {
	db := testutil.OpenTestDB(t, &types.AuditLog{})
	rows := []*types.AuditLog{
		{TenantID: 7, Action: types.AuditActionMemberAdded},
		{TenantID: 7, Action: types.AuditActionKBUpdated, ScopeType: "knowledge_base", ScopeID: "kb-a"},
		{TenantID: 7, Action: types.AuditActionKnowledgeCreated, ScopeType: "knowledge_base", ScopeID: "kb-b"},
		{TenantID: 8, Action: types.AuditActionKBUpdated, ScopeType: "knowledge_base", ScopeID: "kb-a"},
	}
	for _, row := range rows {
		if err := db.Create(row).Error; err != nil {
			t.Fatalf("insert audit log: %v", err)
		}
	}

	repo := NewAuditLogRepository(db)
	got, err := repo.List(context.Background(), 7, &interfaces.AuditLogQuery{
		ScopeType: "knowledge_base",
		ScopeID:   "kb-a",
	})
	if err != nil {
		t.Fatalf("list audit logs: %v", err)
	}
	if len(got) != 1 || got[0].TenantID != 7 || got[0].ScopeID != "kb-a" {
		t.Fatalf("scope filter returned unexpected rows: %+v", got)
	}
	unscoped, err := repo.List(context.Background(), 7, &interfaces.AuditLogQuery{UnscopedOnly: true})
	if err != nil {
		t.Fatalf("list unscoped audit logs: %v", err)
	}
	if len(unscoped) != 1 || unscoped[0].Action != types.AuditActionMemberAdded {
		t.Fatalf("unscoped filter returned unexpected rows: %+v", unscoped)
	}
}
