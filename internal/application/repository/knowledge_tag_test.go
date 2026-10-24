package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/testutil"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupKnowledgeTagTestDB opens a PostgreSQL test database with the
// knowledge / tag / relation tables.
//
// 以前这里内联了一份 SQLite 方言的 DDL（DATETIME 列、没有自增 seq_id）。
// PostgreSQL 上 AutoMigrate 能直接按 types 的 gorm tag 建表，所以改用 AutoMigrate。
func setupKnowledgeTagTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := testutil.OpenTestDB(t,
		&types.Knowledge{}, &types.KnowledgeTag{}, &types.KnowledgeTagRelation{},
		&types.Chunk{},
	)
	// 生产里 knowledges.custom_metadata 是 NOT NULL DEFAULT '{}'，types 的 gorm
	// tag 只写了 not null，AutoMigrate 建出来的列没有默认值，裸 SQL 插入不带
	// 这一列就会违反非空约束。
	require.NoError(t, db.Exec(
		`ALTER TABLE knowledges ALTER COLUMN custom_metadata SET DEFAULT '{}'::jsonb`).Error)
	return db
}

func seedKnowledgeTagFixture(t *testing.T, db *gorm.DB) (kbID, knowledgeID, tagA, tagB string) {
	t.Helper()
	kbID = uuid.New().String()
	knowledgeID = uuid.New().String()
	tagA = uuid.New().String()
	tagB = uuid.New().String()
	require.NoError(t, db.Exec(`
		INSERT INTO knowledges (id, tenant_id, knowledge_base_id, type, title, parse_status)
		VALUES (?, 1, ?, 'file', 'doc', 'completed')
	`, knowledgeID, kbID).Error)
	require.NoError(t, db.Exec(`
		INSERT INTO knowledge_tags (id, seq_id, tenant_id, knowledge_base_id, name)
		VALUES (?, 1, 1, ?, 'alpha'), (?, 2, 1, ?, 'beta')
	`, tagA, kbID, tagB, kbID).Error)
	return kbID, knowledgeID, tagA, tagB
}

func TestSetKnowledgeTags_DedupesAndReplaces(t *testing.T) {
	db := setupKnowledgeTagTestDB(t)
	repo := &knowledgeRepository{db: db}
	_, knowledgeID, tagA, tagB := seedKnowledgeTagFixture(t, db)
	ctx := context.Background()

	err := repo.SetKnowledgeTags(ctx, knowledgeID, []string{tagA, tagA, tagB})
	require.NoError(t, err)

	var count int64
	require.NoError(t, db.Model(&types.KnowledgeTagRelation{}).
		Where("knowledge_id = ?", knowledgeID).Count(&count).Error)
	assert.Equal(t, int64(2), count)

	err = repo.SetKnowledgeTags(ctx, knowledgeID, []string{tagB})
	require.NoError(t, err)
	require.NoError(t, db.Model(&types.KnowledgeTagRelation{}).
		Where("knowledge_id = ?", knowledgeID).Count(&count).Error)
	assert.Equal(t, int64(1), count)

	err = repo.SetKnowledgeTags(ctx, knowledgeID, nil)
	require.NoError(t, err)
	require.NoError(t, db.Model(&types.KnowledgeTagRelation{}).
		Where("knowledge_id = ?", knowledgeID).Count(&count).Error)
	assert.Equal(t, int64(0), count)
}

func TestAddKnowledgeTagRelations_IsIncrementalIdempotentAndScoped(t *testing.T) {
	db := setupKnowledgeTagTestDB(t)
	repo := &knowledgeRepository{db: db}
	kbID, knowledgeID, tagA, tagB := seedKnowledgeTagFixture(t, db)
	ctx := context.Background()

	require.NoError(t, repo.SetKnowledgeTags(ctx, knowledgeID, []string{tagA}))
	require.NoError(t, repo.AddKnowledgeTagRelations(ctx, 1, kbID, knowledgeID, []string{tagA, tagB, tagB}))
	require.NoError(t, repo.AddKnowledgeTagRelations(ctx, 1, kbID, knowledgeID, []string{tagB}))

	var relations []types.KnowledgeTagRelation
	require.NoError(t, db.Where("knowledge_id = ?", knowledgeID).Find(&relations).Error)
	assert.Len(t, relations, 2)

	otherKBTag := uuid.New().String()
	require.NoError(t, db.Exec(`
		INSERT INTO knowledge_tags (id, seq_id, tenant_id, knowledge_base_id, name)
		VALUES (?, 3, 1, ?, 'other')
	`, otherKBTag, uuid.New().String()).Error)
	err := repo.AddKnowledgeTagRelations(ctx, 1, kbID, knowledgeID, []string{otherKBTag})
	require.Error(t, err)
}

func TestAddKnowledgeTagRelations_RejectsCancelledKnowledge(t *testing.T) {
	db := setupKnowledgeTagTestDB(t)
	repo := &knowledgeRepository{db: db}
	kbID, knowledgeID, _, tagB := seedKnowledgeTagFixture(t, db)
	require.NoError(t, db.Model(&types.Knowledge{}).
		Where("id = ?", knowledgeID).
		Update("parse_status", types.ParseStatusCancelled).Error)

	err := repo.AddKnowledgeTagRelations(context.Background(), 1, kbID, knowledgeID, []string{tagB})
	require.Error(t, err)

	var count int64
	require.NoError(t, db.Model(&types.KnowledgeTagRelation{}).
		Where("knowledge_id = ?", knowledgeID).
		Count(&count).Error)
	assert.Zero(t, count)
}

func TestGetKnowledgeTags_ReturnsTagDetails(t *testing.T) {
	db := setupKnowledgeTagTestDB(t)
	repo := &knowledgeRepository{db: db}
	_, knowledgeID, tagA, tagB := seedKnowledgeTagFixture(t, db)
	ctx := context.Background()

	require.NoError(t, repo.SetKnowledgeTags(ctx, knowledgeID, []string{tagA, tagB}))

	tagMap, err := repo.GetKnowledgeTags(ctx, []string{knowledgeID})
	require.NoError(t, err)
	require.Len(t, tagMap[knowledgeID], 2)

	names := map[string]bool{}
	for _, tag := range tagMap[knowledgeID] {
		names[tag.Name] = true
	}
	assert.True(t, names["alpha"])
	assert.True(t, names["beta"])
}

func TestDeleteKnowledgeTagRelations(t *testing.T) {
	db := setupKnowledgeTagTestDB(t)
	repo := &knowledgeRepository{db: db}
	_, knowledgeID, tagA, _ := seedKnowledgeTagFixture(t, db)
	ctx := context.Background()

	require.NoError(t, repo.SetKnowledgeTags(ctx, knowledgeID, []string{tagA}))
	require.NoError(t, repo.DeleteKnowledgeTagRelations(ctx, knowledgeID))

	var count int64
	require.NoError(t, db.Model(&types.KnowledgeTagRelation{}).
		Where("knowledge_id = ?", knowledgeID).Count(&count).Error)
	assert.Equal(t, int64(0), count)
}

func TestApplyKnowledgeListFilter_TagIDsOrSemantics(t *testing.T) {
	db := setupKnowledgeTagTestDB(t)
	repo := &knowledgeRepository{db: db}
	ctx := context.Background()

	kbID := uuid.New().String()
	docA := uuid.New().String()
	docB := uuid.New().String()
	docC := uuid.New().String()
	tagA := uuid.New().String()
	tagB := uuid.New().String()

	for _, row := range []struct{ id, title string }{
		{docA, "a"}, {docB, "b"}, {docC, "c"},
	} {
		require.NoError(t, db.Exec(`
			INSERT INTO knowledges (id, tenant_id, knowledge_base_id, type, title, parse_status)
			VALUES (?, 1, ?, 'file', ?, 'completed')
		`, row.id, kbID, row.title).Error)
	}
	require.NoError(t, db.Exec(`
		INSERT INTO knowledge_tags (id, seq_id, tenant_id, knowledge_base_id, name)
		VALUES (?, 1, 1, ?, 't-a'), (?, 2, 1, ?, 't-b')
	`, tagA, kbID, tagB, kbID).Error)
	require.NoError(t, repo.SetKnowledgeTags(ctx, docA, []string{tagA}))
	require.NoError(t, repo.SetKnowledgeTags(ctx, docB, []string{tagB}))
	require.NoError(t, repo.SetKnowledgeTags(ctx, docC, []string{tagA, tagB}))

	query := db.WithContext(ctx).Model(&types.Knowledge{}).
		Where("tenant_id = ? AND knowledge_base_id = ?", uint64(1), kbID)
	query = applyKnowledgeListFilter(query, types.KnowledgeListFilter{
		TagIDs: []string{tagA, tagB},
	})

	var ids []string
	require.NoError(t, query.Pluck("id", &ids).Error)
	assert.ElementsMatch(t, []string{docA, docB, docC}, ids)
}

func TestBatchCountReferences_ScopedToKnowledgeBase(t *testing.T) {
	db := setupKnowledgeTagTestDB(t)
	knowledgeRepo := &knowledgeRepository{db: db}
	tagRepo := &knowledgeTagRepository{db: db}
	ctx := context.Background()

	kb1 := uuid.New().String()
	kb2 := uuid.New().String()
	doc1 := uuid.New().String()
	doc2 := uuid.New().String()
	tag1 := uuid.New().String()
	tag2 := uuid.New().String()

	require.NoError(t, db.Exec(`
		INSERT INTO knowledges (id, tenant_id, knowledge_base_id, type, title, parse_status)
		VALUES (?, 1, ?, 'file', 'kb1-doc', 'completed'), (?, 1, ?, 'file', 'kb2-doc', 'completed')
	`, doc1, kb1, doc2, kb2).Error)
	require.NoError(t, db.Exec(`
		INSERT INTO knowledge_tags (id, seq_id, tenant_id, knowledge_base_id, name)
		VALUES (?, 1, 1, ?, 'tag-kb1'), (?, 2, 1, ?, 'tag-kb2')
	`, tag1, kb1, tag2, kb2).Error)
	require.NoError(t, knowledgeRepo.SetKnowledgeTags(ctx, doc1, []string{tag1}))
	// Stale relation: doc in kb2 still linked to tag1 (simulates pre-fix move bug)
	require.NoError(t, knowledgeRepo.SetKnowledgeTags(ctx, doc2, []string{tag1}))

	counts, err := tagRepo.BatchCountReferences(ctx, 1, kb1, []string{tag1})
	require.NoError(t, err)
	assert.Equal(t, int64(1), counts[tag1].KnowledgeCount)
}
