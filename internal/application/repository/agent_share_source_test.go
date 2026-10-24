package repository

import (
	"context"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/testutil"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestGetShareByAgentIDAndSourceForTenantDisambiguatesSource(t *testing.T) {
	// 用 AutoMigrate 建表而不是内联 DDL：内联 DDL 用的是 SQLite 方言
	//（TEXT/INTEGER/DATETIME），在 PostgreSQL 上不成立。
	db := testutil.OpenTestDB(t,
		&types.Tenant{}, &types.User{},
		&types.Organization{}, &types.OrganizationTenantMember{},
		&types.CustomAgent{}, &types.AgentShare{})
	require.NoError(t, db.Create(&types.Tenant{ID: 1, Name: "t1"}).Error)
	require.NoError(t, db.Create(&types.User{
		ID: "owner", Username: "owner", Email: "owner@example.com", TenantID: 1,
	}).Error)
	require.NoError(t, db.Create(&types.Organization{
		ID: "org", Name: "org", OwnerID: "owner", OwnerTenantID: 1, InviteCode: "code-org",
	}).Error)
	for _, tenantID := range []uint64{7, 42, 84} {
		require.NoError(t, db.Create(&types.OrganizationTenantMember{
			ID: fmt.Sprintf("m-%d", tenantID), OrganizationID: "org", TenantID: tenantID,
			Role: types.OrgRoleViewer, RepresentativeUserID: "owner",
		}).Error)
	}
	for _, tenantID := range []uint64{42, 84} {
		require.NoError(t, db.Create(&types.CustomAgent{
			ID: "builtin-smart-reasoning", TenantID: tenantID, Name: "builtin-smart-reasoning",
		}).Error)
	}
	for _, tenantID := range []uint64{42, 84} {
		require.NoError(t, db.Create(&types.AgentShare{
			ID: fmt.Sprintf("share-%d", tenantID), AgentID: "builtin-smart-reasoning",
			OrganizationID: "org", SharedByUserID: "owner", SourceTenantID: tenantID,
			Permission: types.OrgRoleViewer,
		}).Error)
	}

	repo := &agentShareRepository{db: db}
	share, err := repo.GetShareByAgentIDAndSourceForTenant(
		context.Background(), 7, "builtin-smart-reasoning", 84,
	)
	require.NoError(t, err)
	require.Equal(t, "share-84", share.ID)
	require.Equal(t, uint64(84), share.SourceTenantID)

	_, err = repo.GetShareByAgentIDAndSourceForTenant(
		context.Background(), 8, "builtin-smart-reasoning", 84,
	)
	require.ErrorIs(t, err, ErrAgentShareNotFound)

	// A share lapses once its source tenant leaves the organization, even if
	// the row itself was left behind.
	require.NoError(t, db.Exec(
		`DELETE FROM organization_tenant_members WHERE organization_id = 'org' AND tenant_id = 84`).Error)
	_, err = repo.GetShareByAgentIDAndSourceForTenant(
		context.Background(), 7, "builtin-smart-reasoning", 84,
	)
	require.ErrorIs(t, err, ErrAgentShareNotFound)
	_, err = repo.GetShareByAgentIDForTenant(context.Background(), 7, "builtin-smart-reasoning", 7)
	require.NoError(t, err, "the share of the remaining member still resolves")
	shares, err := repo.ListSharedAgentsForTenant(context.Background(), 7)
	require.NoError(t, err)
	require.Len(t, shares, 1)
	require.Equal(t, uint64(42), shares[0].SourceTenantID)
}
