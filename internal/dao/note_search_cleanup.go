package dao

import (
	"context"
	"gorm.io/gorm"
)

// Cleanup follows physical SQL deletion; the note join already excludes removed
// records if cleanup fails. The same body lock prevents backfill resurrection.
func (r *noteRepository) cleanSearchIDs(ctx context.Context, db *gorm.DB, ids []int64) error {
	if err := r.initSearch(db); err != nil {
		return err
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for start := 0; start < len(ids); start += 128 {
			end := start + 128
			if end > len(ids) {
				end = len(ids)
			}
			for _, table := range []string{"note_search_gram", "note_search_state"} {
				if err := tx.Table(table).Where("note_id IN ?", ids[start:end]).Delete(&noteSearchState{}).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}
