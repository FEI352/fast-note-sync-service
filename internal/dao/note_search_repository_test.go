package dao

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/haierkeys/fast-note-sync-service/internal/domain"
	"github.com/haierkeys/fast-note-sync-service/internal/model"
	"github.com/haierkeys/fast-note-sync-service/pkg/util"
	"github.com/stretchr/testify/require"
)

func TestLiteralSearchPersistentIndex(t *testing.T) {
	d, repo, _, cleanup := setupFTSTestEnv(t, true)
	defer cleanup()
	// Exercise the repository without the existing optional write queue.
	d.config.EnableWriteQueue = util.Ptr(false)
	d.userConfig.EnableWriteQueue = util.Ptr(false)
	ctx := context.Background()
	r := repo.(*noteRepository)
	create := func(uid, vault int64, path, body string) *domain.Note {
		n, err := r.Create(ctx, &domain.Note{VaultID: vault, Action: domain.NoteActionCreate, Path: path, PathHash: util.EncodeHash32(path), Content: body, ContentHash: util.EncodeHash32(body)}, uid)
		require.NoError(t, err)
		return n
	}
	n := create(1, 10, "知识/Hello-World.md", "prefixMIDword 知识管理 C++.*? éß abc  \x00 tail")
	create(1, 11, "other.md", n.Content)
	create(2, 10, "other.md", n.Content)
	search := func(uid, vault int64, query, mode string) []*domain.Note {
		var all []*domain.Note
		var cursor int64
		for {
			page, next, err := r.SearchCandidates(ctx, uid, vault, query, mode, cursor, 1)
			require.NoError(t, err)
			all = append(all, page...)
			if next == 0 {
				break
			}
			require.Greater(t, next, cursor)
			cursor = next
		}
		return all
	}
	for _, query := range []string{"MIDWORD", "知识管", "C++.*?", "++.", "éß ", "abc  ", "\x00 t", "管理"} {
		found := search(1, 10, query, "content")
		require.Len(t, found, 1, query)
		require.Equal(t, n.ID, found[0].ID)
	}
	require.Len(t, search(1, 10, "hello-world", "path"), 1)
	require.Empty(t, search(1, 10, "hello-world", "content"))
	require.Empty(t, search(1, 10, "absent", "auto"))
	require.Len(t, search(2, 10, "MIDWORD", "auto"), 1)
	// Existing SQL-only notes are progressively backfilled; empty pages continue.
	db := d.ResolveDB(r.GetKey(1))
	legacy := model.Note{VaultID: 10, Path: "legacy.md", Action: "create", Content: "旧数据!middle", ContentHash: util.EncodeHash32("旧数据!middle")}
	require.NoError(t, db.Create(&legacy).Error)
	require.Len(t, search(1, 10, "数据!", "content"), 1)
	// A fresh repository reuses the persistent index.
	r = NewNoteRepository(d).(*noteRepository)
	require.Len(t, search(1, 10, "MIDWORD", "content"), 1)
	n.Path = "renamed/path.md"
	n.Content = "replacement 内容!"
	n.ContentHash = util.EncodeHash32(n.Content)
	_, err := r.Update(ctx, n, 1)
	require.NoError(t, err)
	require.Empty(t, search(1, 10, "MIDWORD", "content"))
	require.Empty(t, search(1, 10, "hello-world", "path"))
	require.Len(t, search(1, 10, "内容!", "auto"), 1)
	n.Action = domain.NoteActionDelete
	require.NoError(t, r.UpdateDelete(ctx, n, 1))
	require.Empty(t, search(1, 10, "内容!", "auto"))
	require.NoError(t, r.UpdateActionMtime(ctx, domain.NoteActionModify, 1, n.ID, 1))
	require.Len(t, search(1, 10, "内容!", "auto"), 1)
	// Simulate an interrupted write: invalidate, SQL new hash, old file remains.
	require.NoError(t, r.invalidateSearch(ctx, db, n.ID))
	require.NoError(t, db.Model(&model.Note{}).Where("id = ?", n.ID).Update("content_hash", util.EncodeHash32("failed new body")).Error)
	require.Len(t, search(1, 10, "内容!", "content"), 1)
	var count int64
	require.NoError(t, db.Model(&noteSearchState{}).Where("note_id = ?", n.ID).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, r.Delete(ctx, n.ID, 10, 1))
	require.Empty(t, search(1, 10, "内容!", "auto"))
	require.NoError(t, db.Model(&noteSearchGram{}).Where("note_id = ?", n.ID).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, r.DeleteByVaultID(ctx, 10, 1))
	require.Empty(t, search(1, 10, "数据!", "auto"))
}

func TestLiteralSearchWarmIndexBoundsBodyReads(t *testing.T) {
	d, repo, _, cleanup := setupFTSTestEnv(t, true)
	defer cleanup()
	d.config.EnableWriteQueue = util.Ptr(false)
	d.userConfig.EnableWriteQueue = util.Ptr(false)
	r := repo.(*noteRepository)
	ctx := context.Background()
	const size = 1000
	db := r.note(3).Note.WithContext(ctx).UnderlyingDB()
	for i := 0; i < size; i++ {
		body := "ordinary baseline body"
		if i == size-1 {
			body = "xyzselective marker"
		}
		m := model.Note{VaultID: 33, Path: fmt.Sprintf("%04d.md", i), Action: "create", Content: body, ContentHash: util.EncodeHash32(body)}
		require.NoError(t, db.Create(&m).Error)
	}
	// Warm/backfill all entries using bounded candidate pages.
	cursor := int64(0)
	for {
		_, next, err := r.SearchCandidates(ctx, 3, 33, "xyzselective", "content", cursor, 128)
		require.NoError(t, err)
		if next == 0 {
			break
		}
		cursor = next
	}
	// Count candidate-body hydration by deleting an unrelated legacy body. A full
	// scan would fail on that note; the indexed selective query must not load it.
	require.NoError(t, db.Model(&model.Note{}).Where("path = ?", "0000.md").Update("content", "").Error)
	start := time.Now()
	found, next, err := r.SearchCandidates(ctx, 3, 33, "xyzselective", "content", 0, 128)
	require.NoError(t, err)
	require.Len(t, found, 1)
	require.Zero(t, next)
	t.Logf("warm selective search: 1000 notes, 1 candidate body, %s", time.Since(start))
}

func TestLiteralSearchConcurrentWriteAndPurge(t *testing.T) {
	d, repo, _, cleanup := setupFTSTestEnv(t, true)
	defer cleanup()
	d.config.EnableWriteQueue = util.Ptr(false)
	d.userConfig.EnableWriteQueue = util.Ptr(false)
	r := repo.(*noteRepository)
	ctx := context.Background()
	n, err := r.Create(ctx, &domain.Note{VaultID: 44, Action: domain.NoteActionCreate, Path: "race.md", PathHash: util.EncodeHash32("race.md"), Content: "alpha marker", ContentHash: util.EncodeHash32("alpha marker")}, 4)
	require.NoError(t, err)
	errs := make(chan error, 2)
	go func() {
		for i := 0; i < 20; i++ {
			_, _, err := r.SearchCandidates(ctx, 4, 44, "marker", "content", 0, 128)
			if err != nil {
				errs <- err
				return
			}
		}
		errs <- nil
	}()
	go func() {
		copyNote := *n
		for i := 0; i < 20; i++ {
			copyNote.Content = fmt.Sprintf("beta marker %d", i)
			copyNote.ContentHash = util.EncodeHash32(copyNote.Content)
			if _, err := r.Update(ctx, &copyNote, 4); err != nil {
				errs <- err
				return
			}
		}
		errs <- nil
	}()
	require.NoError(t, <-errs)
	require.NoError(t, <-errs)
	found, _, err := r.SearchCandidates(ctx, 4, 44, "beta marker 19", "content", 0, 128)
	require.NoError(t, err)
	require.Len(t, found, 1)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, _, err = r.SearchCandidates(canceled, 4, 44, "marker", "content", 0, 128)
	require.ErrorIs(t, err, context.Canceled)
	n.Action = domain.NoteActionDelete
	require.NoError(t, r.UpdateDelete(ctx, n, 4))
	require.NoError(t, r.DeletePhysicalByTime(ctx, time.Now().Add(time.Hour).UnixMilli(), 4))
	db := d.ResolveDB(r.GetKey(4))
	var count int64
	require.NoError(t, db.Model(&noteSearchGram{}).Where("note_id = ?", n.ID).Count(&count).Error)
	require.Zero(t, count)
}
