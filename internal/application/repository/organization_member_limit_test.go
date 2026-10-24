package repository

import (
	"context"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/testutil"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// The member limit is enforced with the insert, not by a separate count the
// caller did earlier, so concurrent joins cannot exceed it.
func TestAddTenantMemberEnforcesLimitWithTheInsert(t *testing.T) {
	// 用 AutoMigrate 建表而不是内联 DDL：内联的 SQLite 方言（TEXT/INTEGER/DATETIME）
	// 在 PostgreSQL 上不成立，而成员表的列定义本来就以 types 为准。
	//
	// 还要建 organizations 表：加成员时实现会先 SELECT ... FOR UPDATE 锁住
	// 组织行，PG 下锁不到行会直接报错，所以必须先插入一条组织记录。
	// 组织行的 owner 外键指向 users，用户又外键指向 tenants，所以这两张表
	// 和对应记录也必须一起建好 —— SQLite 不强制外键，PG 会。
	db := testutil.OpenTestDB(t,
		&types.Tenant{}, &types.User{}, &types.Organization{}, &types.OrganizationTenantMember{})
	require.NoError(t, db.Create(&types.Tenant{ID: 1, Name: "t1"}).Error)
	require.NoError(t, db.Create(&types.User{
		ID: "owner", Username: "owner", Email: "owner@example.com", TenantID: 1,
	}).Error)
	require.NoError(t, db.Create(&types.Organization{
		ID: "org", Name: "org", OwnerID: "owner", OwnerTenantID: 1, InviteCode: "code-org",
	}).Error)
	repo := &organizationRepository{db: db}
	add := func(tenantID uint64, limit int) error {
		return repo.AddTenantMember(context.Background(), &types.OrganizationTenantMember{
			ID: fmt.Sprintf("m-%d", tenantID), OrganizationID: "org", TenantID: tenantID, Role: types.OrgRoleViewer,
			// 必须指向真实用户：AutoMigrate 依据 gorm 关联生成了到 users 的外键，
			// 而 PG 会真的校验外键（SQLite 默认不校验）。
			RepresentativeUserID: "owner",
		}, limit)
	}

	require.NoError(t, add(1, 2))
	require.NoError(t, add(2, 2))
	require.ErrorIs(t, add(3, 2), ErrOrgMemberLimitReached)
	require.ErrorIs(t, add(2, 0), ErrOrgMemberAlreadyExists)
	require.NoError(t, add(3, 0), "no limit")

	var count int64
	require.NoError(t, db.Model(&types.OrganizationTenantMember{}).Count(&count).Error)
	require.Equal(t, int64(3), count)
}
