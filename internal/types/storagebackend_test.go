package types

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStorageBackendPathRoundTrip(t *testing.T) {
	path := BuildStorageBackendPath("backend-a", "cos://bucket/region/7/file.pdf")
	id, inner, ok := ParseStorageBackendPath(path)
	require.True(t, ok)
	assert.Equal(t, "backend-a", id)
	assert.Equal(t, "cos://bucket/region/7/file.pdf", inner)
	assert.Equal(t, "cos", ParseProviderScheme(path))
}

func TestSharesStorageBackendWithUsesConcreteInstance(t *testing.T) {
	aID, bID := "cos-a", "cos-b"
	a := &KnowledgeBase{StorageBackendID: &aID, StorageProviderConfig: &StorageProviderConfig{Provider: "cos"}}
	b := &KnowledgeBase{StorageBackendID: &bID, StorageProviderConfig: &StorageProviderConfig{Provider: "cos"}}
	assert.False(t, a.SharesStorageBackendWith(b, "", "cos"))

	b.StorageBackendID = &aID
	assert.True(t, a.SharesStorageBackendWith(b, "", "cos"))
}

func TestNewStorageBackendResponseMasksCredentials(t *testing.T) {
	backend := &StorageBackend{Config: StorageBackendConfig{AccessKeyID: "id", SecretAccessKey: "secret"}}
	response := NewStorageBackendResponse(backend)
	assert.Equal(t, RedactedSecretPlaceholder, response.Config.AccessKeyID)
	assert.Equal(t, RedactedSecretPlaceholder, response.Config.SecretAccessKey)
	assert.Equal(t, "id", backend.Config.AccessKeyID)
}

func TestStorageBackendFromEnvironment(t *testing.T) {
	t.Setenv("STORAGE_TYPE", "s3")
	t.Setenv("S3_ENDPOINT", "https://s3.example.com")
	t.Setenv("S3_REGION", "ap-test-1")
	t.Setenv("S3_ACCESS_KEY", "access")
	t.Setenv("S3_SECRET_KEY", "secret")
	t.Setenv("S3_BUCKET_NAME", "bucket")

	backend := StorageBackendFromEnvironment(42)
	require.NotNil(t, backend)
	assert.Equal(t, uint64(42), backend.TenantID)
	assert.Equal(t, "s3", backend.Provider)
	assert.Equal(t, StorageBackendSourceEnv, backend.Source)
	assert.True(t, backend.LegacyAlias)
	assert.Equal(t, "bucket", backend.Config.BucketName)
}

func TestStorageBackendRejectsTraversingPathPrefix(t *testing.T) {
	backend := &StorageBackend{
		TenantID: 1, Name: "unsafe", Provider: "local",
		Config: StorageBackendConfig{PathPrefix: "../outside"},
	}
	require.Error(t, backend.Validate())
}

// 2026-11-06 生产实例回归：UseSSL=false 必须能原样往返。
// 曾因 `json:"use_ssl,omitempty"` 把 false 丢掉 → 落库缺键 → 前端用
// blankConfig() 的默认 true 覆盖 → 显示「使用 HTTPS」与实际（http）相反。
func TestStorageBackendConfigKeepsFalseBooleans(t *testing.T) {
	raw, err := json.Marshal(StorageBackendConfig{
		Mode: "remote", Endpoint: "minio:9000", BucketName: "weknora-files",
		UseSSL: false, ForcePathStyle: false, UseTempBucket: false,
	})
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	for _, k := range []string{"use_ssl", "force_path_style", "use_temp_bucket"} {
		// 键必须在（不是被 omitempty 丢掉），且值为 false
		v, ok := decoded[k]
		require.True(t, ok, "字段 %s 被 omitempty 丢掉了", k)
		assert.Equal(t, false, v, "字段 %s 的 false 语义丢失", k)
	}

	// 完整往返：反序列化后仍是 false（不是零值缺失导致的歧义）
	var back StorageBackendConfig
	require.NoError(t, json.Unmarshal(raw, &back))
	assert.False(t, back.UseSSL)
	assert.False(t, back.ForcePathStyle)
	assert.False(t, back.UseTempBucket)
}
