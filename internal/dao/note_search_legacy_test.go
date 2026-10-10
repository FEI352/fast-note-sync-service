package dao

import (
	"context"
	"testing"

	"github.com/haierkeys/fast-note-sync-service/internal/model"
	"github.com/haierkeys/fast-note-sync-service/pkg/util"
	"github.com/stretchr/testify/require"
)

func TestBleveRebuildRetainsLegacySQLContent(t *testing.T) {
	d, repository, _, cleanup := setupFTSTestEnv(t, false)
	defer cleanup()
	r := repository.(*noteRepository)
	const uid int64 = 1
	const vaultID int64 = 7
	db := r.note(uid).Note.WithContext(context.Background()).UnderlyingDB()
	n := &model.Note{VaultID: vaultID, Path: "legacy.md", Content: "uniqueterm", ContentHash: util.EncodeHash32("uniqueterm")}
	require.NoError(t, db.Create(n).Error)
	// Both rebuild implementations must index legacy bodies without a file.
	require.NoError(t, r.RebuildVaultIndex(context.Background(), uid, vaultID))
	ids, err := r.searchFTS(uid, vaultID, "uniqueterm", false, "mtime", "desc", 100, 0)
	require.NoError(t, err)
	require.Equal(t, []int64{n.ID}, ids)
	fts := NewNoteFTSRepository(d)
	require.NoError(t, fts.(*noteFTSRepository).rebuildVault(context.Background(), uid, vaultID))
	ids, err = fts.Search(context.Background(), "uniqueterm", vaultID, uid, 100, 0)
	require.NoError(t, err)
	require.Equal(t, []int64{n.ID}, ids)
}
