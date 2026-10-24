package repository

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/testutil"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupKnowledgeTestDB returns a PostgreSQL test db with the knowledges table.
//
// 以前这里内联了一份 SQLite 方言的 DDL，并把连接池压到 1 条连接来绕开
// SQLite 的单写者限制。PostgreSQL 没有这个限制：并发写走的是行级锁，
// FinalizeSubtask 的原子递减在 PG 上照样成立，所以连接池保持默认即可。
func setupKnowledgeTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := testutil.OpenTestDB(t, &types.Knowledge{})

	// 生产迁移里 knowledges 的 JSON 列都是 JSONB（000000/000078/000101），
	// 而 types 的 gorm tag 写的是 json，AutoMigrate 建出来的是 json 列。
	// 测试要跑在和线上一致的列类型上，所以这里改回 JSONB。
	//
	// custom_metadata 更是必须补上 DEFAULT：生产是 NOT NULL DEFAULT '{}'，
	// types 的 tag 只写了 not null，AutoMigrate 建出来的列没有默认值，
	// 裸 SQL 插入不带这一列就会直接报错。
	for _, stmt := range []string{
		`ALTER TABLE knowledges ALTER COLUMN metadata TYPE JSONB USING metadata::jsonb`,
		`ALTER TABLE knowledges ALTER COLUMN custom_metadata TYPE JSONB USING custom_metadata::jsonb`,
		`ALTER TABLE knowledges ALTER COLUMN custom_metadata SET DEFAULT '{}'::JSONB`,
		`ALTER TABLE knowledges ALTER COLUMN last_faq_import_result TYPE JSONB USING last_faq_import_result::jsonb`,
		`ALTER TABLE knowledges ALTER COLUMN profile TYPE JSONB USING profile::jsonb`,
	} {
		require.NoError(t, db.Exec(stmt).Error)
	}
	return db
}

// insertProcessingKnowledge seeds a row in `processing` state ready for a
// SetFinalizing transition.
func insertProcessingKnowledge(t *testing.T, db *gorm.DB) string {
	t.Helper()
	id := uuid.New().String()
	require.NoError(t, db.Exec(`
		INSERT INTO knowledges (id, tenant_id, knowledge_base_id, type, title, source, parse_status, pending_subtasks_count)
		VALUES (?, 1, ?, 'document', 'finalize-test', 'manual', 'processing', 0)
	`, id, uuid.New().String()).Error)
	return id
}

// reloadKnowledgeRow returns the parse_status and pending_subtasks_count of
// a row directly via raw SQL — bypasses any GORM hook noise.
func reloadKnowledgeRow(t *testing.T, db *gorm.DB, id string) (status string, count int) {
	t.Helper()
	row := db.Raw(`SELECT parse_status, pending_subtasks_count FROM knowledges WHERE id = ?`, id).Row()
	require.NoError(t, row.Scan(&status, &count))
	return status, count
}

func reloadKnowledgeErrorMessage(t *testing.T, db *gorm.DB, id string) string {
	t.Helper()
	var msg string
	require.NoError(t, db.Raw(`SELECT COALESCE(error_message, '') FROM knowledges WHERE id = ?`, id).Scan(&msg).Error)
	return msg
}

func TestKnowledgeRepository_UpdateKnowledgeColumnsSanitizesErrorMessage(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db)
	id := insertProcessingKnowledge(t, db)
	invalid := "parse failed " + string([]byte{0xef, 0xbc, 0x2e})

	require.NoError(t, repo.UpdateKnowledgeColumns(context.Background(), id, map[string]interface{}{
		"error_message": invalid,
	}))

	got := reloadKnowledgeErrorMessage(t, db, id)
	if !utf8.ValidString(got) {
		t.Fatalf("persisted error_message is invalid UTF-8: % x", []byte(got))
	}
	assert.Equal(t, "parse failed .", got)
}

func insertKnowledgeWithStatus(t *testing.T, db *gorm.DB, status string, deleted bool) string {
	t.Helper()
	id := uuid.New().String()
	deletedAt := interface{}(nil)
	if deleted {
		deletedAt = "2026-06-16 12:00:00"
	}
	require.NoError(t, db.Exec(`
		INSERT INTO knowledges (id, tenant_id, knowledge_base_id, type, title, source, parse_status, deleted_at)
		VALUES (?, 1, ?, 'document', 'delete-test', 'manual', ?, ?)
	`, id, uuid.New().String(), status, deletedAt).Error)
	return id
}

// TestFinalizeSubtask_Concurrent_ExactlyOnePromote spawns N goroutines that
// each call FinalizeSubtask after SetFinalizing(N), and asserts:
//   - the counter ends at zero,
//   - parse_status is "completed",
//   - exactly one caller observed promoted=true.
//
// This is the behavior the original "stuck pending_subtasks_count" bug
// violated: clobbered counters meant some callers saw a non-zero value
// after the true count had reached zero, and none of them promoted.
func TestFinalizeSubtask_Concurrent_ExactlyOnePromote(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()

	const n = 20
	id := insertProcessingKnowledge(t, db)

	transitioned, err := repo.SetFinalizing(ctx, id, n)
	require.NoError(t, err)
	require.True(t, transitioned, "SetFinalizing should transition processing -> finalizing")

	var promoteWins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, promoted, ferr := repo.FinalizeSubtask(ctx, id)
			if ferr != nil {
				t.Errorf("FinalizeSubtask: %v", ferr)
				return
			}
			if promoted {
				promoteWins.Add(1)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int32(1), promoteWins.Load(),
		"exactly one caller must observe promoted=true even under concurrent decrements")

	status, count := reloadKnowledgeRow(t, db, id)
	assert.Equal(t, types.ParseStatusCompleted, status)
	assert.Equal(t, 0, count)
}

// TestFinalizeSubtask_PartialDecrement_StaysFinalizing verifies the row
// remains in "finalizing" with the expected residual count when fewer
// callers decrement than were seeded — the promote guard must not fire
// early.
func TestFinalizeSubtask_PartialDecrement_StaysFinalizing(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()

	id := insertProcessingKnowledge(t, db)
	_, err := repo.SetFinalizing(ctx, id, 3)
	require.NoError(t, err)

	for i := 0; i < 2; i++ {
		_, promoted, ferr := repo.FinalizeSubtask(ctx, id)
		require.NoError(t, ferr)
		assert.False(t, promoted, "promote must not fire while count > 0")
	}

	status, count := reloadKnowledgeRow(t, db, id)
	assert.Equal(t, types.ParseStatusFinalizing, status)
	assert.Equal(t, 1, count)
}

// TestFinalizeSubtask_DecrementClampedAtZero verifies the safety-net
// clamp on the decrement: extra calls past the seeded count must not
// underflow pending_subtasks_count below zero. (Reconciliation's
// shortfall-release loop relies on this.)
func TestFinalizeSubtask_DecrementClampedAtZero(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()

	id := insertProcessingKnowledge(t, db)
	_, err := repo.SetFinalizing(ctx, id, 1)
	require.NoError(t, err)

	// First decrement drains the only slot and promotes.
	_, promoted, err := repo.FinalizeSubtask(ctx, id)
	require.NoError(t, err)
	assert.True(t, promoted)

	// Subsequent decrements must be no-ops, not underflow.
	for i := 0; i < 3; i++ {
		_, promoted, err := repo.FinalizeSubtask(ctx, id)
		require.NoError(t, err)
		assert.False(t, promoted)
	}

	status, count := reloadKnowledgeRow(t, db, id)
	assert.Equal(t, types.ParseStatusCompleted, status)
	assert.Equal(t, 0, count, "pending_subtasks_count must be clamped at zero")
}

// TestSetFinalizingAndFinalizeSubtask_ClearStaleErrorMessage is the
// regression test for stale error_message: a row that failed once keeps
// error_message set, and both entering finalizing (a new attempt) and
// promoting to completed (a successful finish) must clear it so the UI
// no longer shows an outdated failure.
func TestSetFinalizingAndFinalizeSubtask_ClearStaleErrorMessage(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()

	id := insertProcessingKnowledge(t, db)
	require.NoError(t, db.Exec(
		`UPDATE knowledges SET error_message = ? WHERE id = ?`,
		"Task interrupted due to application restart",
		id,
	).Error)

	transitioned, err := repo.SetFinalizing(ctx, id, 1)
	require.NoError(t, err)
	require.True(t, transitioned)
	assert.Empty(t, reloadKnowledgeErrorMessage(t, db, id),
		"SetFinalizing must clear error_message from the previous attempt")

	require.NoError(t, db.Exec(
		`UPDATE knowledges SET error_message = ? WHERE id = ?`,
		"stale finalizing failure",
		id,
	).Error)
	_, promoted, err := repo.FinalizeSubtask(ctx, id)
	require.NoError(t, err)
	require.True(t, promoted)
	assert.Empty(t, reloadKnowledgeErrorMessage(t, db, id),
		"promotion to completed must clear error_message")
}

// TestUpdateKnowledge_DoesNotClobberPendingCounter is the regression test
// for the original bug: a full-row Save with a stale in-memory counter
// must not write that stale value back, otherwise it overwrites atomic
// decrements made by other goroutines.
//
// Sequence:
//  1. SetFinalizing(N=5) -> counter=5
//  2. Caller A loads the row (sees counter=5)
//  3. FinalizeSubtask runs concurrently and decrements to counter=4
//  4. Caller A modifies an unrelated field (Title) and calls UpdateKnowledge
//  5. Counter must still be 4 (not 5).
func TestUpdateKnowledge_DoesNotClobberPendingCounter(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()

	id := insertProcessingKnowledge(t, db)
	_, err := repo.SetFinalizing(ctx, id, 5)
	require.NoError(t, err)

	// Step 2: caller A snapshots the row with counter=5 in memory.
	loaded, err := repo.GetKnowledgeByID(ctx, 1, id)
	require.NoError(t, err)
	require.Equal(t, 5, loaded.PendingSubtasksCount)

	// Step 3: an enrichment subtask decrements concurrently.
	_, _, err = repo.FinalizeSubtask(ctx, id)
	require.NoError(t, err)

	// Step 4: caller A persists an unrelated change. The in-memory copy
	// of PendingSubtasksCount is the STALE 5 — Save must NOT write it.
	loaded.Title = "renamed-after-stale-load"
	require.NoError(t, repo.UpdateKnowledge(ctx, loaded))

	// Step 5: the live counter is still 4, not clobbered back to 5.
	status, count := reloadKnowledgeRow(t, db, id)
	assert.Equal(t, types.ParseStatusFinalizing, status)
	assert.Equal(t, 4, count,
		"UpdateKnowledge must omit pending_subtasks_count so a stale in-memory value cannot clobber atomic decrements")

	// And the unrelated field WAS persisted.
	reloaded, err := repo.GetKnowledgeByID(ctx, 1, id)
	require.NoError(t, err)
	assert.Equal(t, "renamed-after-stale-load", reloaded.Title)
}

// TestUpdateKnowledge_PendingCounterOmittedOnReset verifies the inverse
// case the reparse paths rely on: even setting PendingSubtasksCount=0
// in memory and calling UpdateKnowledge does NOT persist that value.
// Reparse must use UpdateKnowledgeColumn explicitly.
func TestUpdateKnowledge_PendingCounterOmittedOnReset(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()

	id := insertProcessingKnowledge(t, db)
	_, err := repo.SetFinalizing(ctx, id, 7)
	require.NoError(t, err)

	loaded, err := repo.GetKnowledgeByID(ctx, 1, id)
	require.NoError(t, err)

	// Caller tries to reset the counter via Save — this must be a no-op
	// for that column. The dedicated UpdateKnowledgeColumn is the only
	// path that actually writes pending_subtasks_count.
	loaded.PendingSubtasksCount = 0
	require.NoError(t, repo.UpdateKnowledge(ctx, loaded))

	_, count := reloadKnowledgeRow(t, db, id)
	assert.Equal(t, 7, count, "UpdateKnowledge with PendingSubtasksCount=0 must NOT persist the reset")

	// The explicit column write IS the supported path.
	require.NoError(t, repo.UpdateKnowledgeColumn(ctx, id, "pending_subtasks_count", 0))
	_, count = reloadKnowledgeRow(t, db, id)
	assert.Equal(t, 0, count)
}

func TestUpdateActiveDeletingKnowledgeColumns_GuardsStateAndSoftDelete(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()

	activeDeletingID := insertKnowledgeWithStatus(t, db, types.ParseStatusDeleting, false)
	activeCompletedID := insertKnowledgeWithStatus(t, db, types.ParseStatusCompleted, false)
	deletedDeletingID := insertKnowledgeWithStatus(t, db, types.ParseStatusDeleting, true)

	require.NoError(
		t,
		db.Exec(
			"UPDATE knowledges SET knowledge_base_id = ? WHERE id IN ?",
			"delete-kb",
			[]string{activeDeletingID, activeCompletedID, deletedDeletingID},
		).Error,
	)
	for _, scope := range []struct {
		tenant uint64
		kb     string
	}{{2, "delete-kb"}, {1, "other-kb"}, {0, "delete-kb"}, {1, ""}} {
		updated, err := repo.UpdateActiveDeletingKnowledgeColumns(
			ctx,
			scope.tenant,
			scope.kb,
			activeDeletingID,
			map[string]interface{}{"parse_status": types.ParseStatusFailed},
		)
		require.NoError(t, err)
		require.False(t, updated)
	}

	updated, err := repo.UpdateActiveDeletingKnowledgeColumns(
		ctx,
		1,
		"delete-kb",
		activeDeletingID,
		map[string]interface{}{
			"parse_status":  types.ParseStatusFailed,
			"error_message": "delete task exhausted retries",
		},
	)
	require.NoError(t, err)
	assert.True(t, updated)

	updated, err = repo.UpdateActiveDeletingKnowledgeColumns(
		ctx,
		1,
		"delete-kb",
		activeCompletedID,
		map[string]interface{}{
			"parse_status": types.ParseStatusFailed,
		},
	)
	require.NoError(t, err)
	assert.False(t, updated)

	updated, err = repo.UpdateActiveDeletingKnowledgeColumns(
		ctx,
		1,
		"delete-kb",
		deletedDeletingID,
		map[string]interface{}{
			"parse_status": types.ParseStatusFailed,
		},
	)
	require.NoError(t, err)
	assert.False(t, updated)

	status, _ := reloadKnowledgeRow(t, db, activeDeletingID)
	assert.Equal(t, types.ParseStatusFailed, status)
	status, _ = reloadKnowledgeRow(t, db, activeCompletedID)
	assert.Equal(t, types.ParseStatusCompleted, status)
	status, _ = reloadKnowledgeRow(t, db, deletedDeletingID)
	assert.Equal(t, types.ParseStatusDeleting, status)
}

func TestCompleteProcessingWithoutSubtasks(t *testing.T) {
	for _, tc := range []struct {
		status  string
		deleted bool
		want    bool
	}{
		{types.ParseStatusProcessing, false, true},
		{types.ParseStatusCancelled, false, false},
		{types.ParseStatusDeleting, false, false},
		{types.ParseStatusCompleted, false, false},
		{types.ParseStatusFinalizing, false, false},
		{types.ParseStatusProcessing, true, false},
	} {
		t.Run(tc.status+"/deleted="+fmt.Sprint(tc.deleted), func(t *testing.T) {
			db := setupKnowledgeTestDB(t)
			repo := NewKnowledgeRepository(db)
			id := insertKnowledgeWithStatus(t, db, tc.status, tc.deleted)
			require.NoError(t, db.Exec(
				`UPDATE knowledges SET summary_status = 'pending', error_message = 'old error' WHERE id = ?`, id,
			).Error)
			completed, err := repo.CompleteProcessingWithoutSubtasks(context.Background(), id)
			require.NoError(t, err)
			require.Equal(t, tc.want, completed)
			status, count := reloadKnowledgeRow(t, db, id)
			var summary string
			require.NoError(t, db.Raw(`SELECT summary_status FROM knowledges WHERE id = ?`, id).Scan(&summary).Error)
			if tc.want {
				require.Equal(t, types.ParseStatusCompleted, status)
				require.Zero(t, count)
				require.Equal(t, types.SummaryStatusNone, summary)
				require.Empty(t, reloadKnowledgeErrorMessage(t, db, id))
				completed, err = repo.CompleteProcessingWithoutSubtasks(context.Background(), id)
				require.NoError(t, err)
				require.False(t, completed, "duplicate delivery must not complete twice")
			} else {
				require.Equal(t, tc.status, status)
				require.Equal(t, types.SummaryStatusPending, summary)
				require.Equal(t, "old error", reloadKnowledgeErrorMessage(t, db, id))
			}
		})
	}
}
