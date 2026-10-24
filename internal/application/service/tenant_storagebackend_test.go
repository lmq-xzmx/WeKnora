package service_test

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/testutil"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateTenantCreatesConcreteDefaultStorageBackend(t *testing.T) {
	t.Setenv("STORAGE_TYPE", "local")
	db := testutil.OpenTestDB(t, &types.Tenant{}, &types.StorageBackend{})
	tenantRepo := repository.NewTenantRepository(db)
	storageRepo := repository.NewStorageBackendRepository(db)
	tenantSvc := service.NewTenantService(tenantRepo, storageRepo)

	tenant, err := tenantSvc.CreateTenant(context.Background(), &types.Tenant{Name: "workspace"})
	require.NoError(t, err)
	require.NotNil(t, tenant.DefaultStorageBackendID)

	backend, err := storageRepo.GetByID(context.Background(), tenant.ID, *tenant.DefaultStorageBackendID)
	require.NoError(t, err)
	require.NotNil(t, backend)
	assert.Equal(t, "local", backend.Provider)
	assert.Equal(t, types.StorageBackendSourceEnv, backend.Source)
	assert.True(t, backend.LegacyAlias)
}
