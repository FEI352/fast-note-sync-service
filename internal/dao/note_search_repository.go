package dao

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/haierkeys/fast-note-sync-service/internal/domain"
	"github.com/haierkeys/fast-note-sync-service/internal/model"
	"github.com/haierkeys/fast-note-sync-service/pkg/util"
	"gorm.io/gorm"
)

// Postings contain unique lower-case rune trigrams, including spaces and punctuation.
// They only select a safe superset; the loaded body remains the source of truth.
type noteSearchGram struct {
	NoteID int64  `gorm:"primaryKey;autoIncrement:false;index:idx_note_search_gram,priority:3"`
	Field  string `gorm:"primaryKey;size:8;index:idx_note_search_gram,priority:2"`
	Gram   string `gorm:"primaryKey;size:24;index:idx_note_search_gram,priority:1"`
}

func (noteSearchGram) TableName() string { return "note_search_gram" }

type noteSearchState struct {
	NoteID      int64 `gorm:"primaryKey;autoIncrement:false"`
	VaultID     int64
	Path        string `gorm:"type:text"`
	ContentHash string `gorm:"size:128"`
}

func (noteSearchState) TableName() string { return "note_search_state" }

func (r *noteRepository) initSearch(db *gorm.DB) error {
	r.searchMigrationMu.Lock()
	defer r.searchMigrationMu.Unlock()
	db = db.Session(&gorm.Session{NewDB: true})
	key := db.Statement.ConnPool
	if _, ok := r.searchReady.Load(key); ok {
		return nil
	}
	if err := db.AutoMigrate(&noteSearchGram{}, &noteSearchState{}); err != nil {
		return err
	}
	r.searchReady.Store(key, true)
	return nil
}

func literalTrigrams(text string) []string {
	runes := []rune(strings.ToLower(text))
	seen := make(map[string]struct{})
	grams := make([]string, 0)
	for i := 0; i+3 <= len(runes); i++ {
		gram := hex.EncodeToString([]byte(string(runes[i : i+3])))
		if _, ok := seen[gram]; !ok {
			seen[gram] = struct{}{}
			grams = append(grams, gram)
		}
	}
	return grams
}

// Removing readiness before changing a body makes partial/failed writes searchable
// by the fallback. All postings and readiness are then published atomically.
func (r *noteRepository) invalidateSearch(ctx context.Context, db *gorm.DB, id int64) error {
	if err := r.initSearch(db); err != nil {
		return err
	}
	return db.WithContext(ctx).Where("note_id = ?", id).Delete(&noteSearchState{}).Error
}
func (r *noteRepository) indexLiteralNote(ctx context.Context, db *gorm.DB, m *model.Note, content string) error {
	if err := r.initSearch(db); err != nil {
		return err
	}
	// A failed file write can leave the new SQL hash with the old body. Keep
	// such records dirty until a successful writer repairs them.
	if m.ContentHash != "" && util.EncodeHash32(content) != m.ContentHash {
		return nil
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("note_id = ?", m.ID).Delete(&noteSearchGram{}).Error; err != nil {
			return err
		}
		postings := make([]noteSearchGram, 0)
		for _, field := range []struct{ name, text string }{{"path", m.Path}, {"content", content}} {
			for _, gram := range literalTrigrams(field.text) {
				postings = append(postings, noteSearchGram{m.ID, field.name, gram})
			}
		}
		if len(postings) > 0 {
			if err := tx.CreateInBatches(postings, 128).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("note_id = ?", m.ID).Delete(&noteSearchState{}).Error; err != nil {
			return err
		}
		return tx.Create(&noteSearchState{m.ID, m.VaultID, m.Path, m.ContentHash}).Error
	})
}

// SearchCandidates scans at most limit candidates (maximum 128), not limit
// matches. Empty result pages may have a nonzero cursor and must be continued.
// Dirty/missing entries are included even when postings do not match. This also
// makes an interrupted migration or an index write failure completeness-safe.
func (r *noteRepository) SearchCandidates(ctx context.Context, uid, vaultID int64, query, mode string, afterID int64, limit int) ([]*domain.Note, int64, error) {
	searchLock := &r.dao.literalSearchLocks[uint64(uid)%64]
	searchLock.Lock()
	defer searchLock.Unlock()
	if mode != "path" && mode != "content" && mode != "auto" {
		return nil, 0, fmt.Errorf("invalid literal search mode %q", mode)
	}
	if limit <= 0 || limit > 128 {
		limit = 128
	}
	r.note(uid) // initialize the tenant note table
	db := r.dao.ResolveDB(r.GetKey(uid)).WithContext(ctx)
	if err := r.initSearch(db); err != nil {
		return nil, 0, err
	}
	q := db.Model(&model.Note{}).Where("vault_id = ? AND action <> ? AND id > ?", vaultID, "delete", afterID)
	grams := literalTrigrams(query)
	if len(grams) > 0 {
		// A single trigram is sufficient for a safe superset and avoids SQL parameter
		// limits for arbitrarily long queries. Final substring verification removes
		// false positives; postings lookup uses the gram/field/note_id B-tree index.
		posting := db.Session(&gorm.Session{NewDB: true}).Model(&noteSearchGram{}).Select("note_id").Where("gram = ?", grams[0])
		if mode != "auto" {
			posting = posting.Where("field = ?", mode)
		}
		ready := db.Session(&gorm.Session{NewDB: true}).Model(&noteSearchState{}).Select("note_id").Where("note_search_state.note_id = note.id AND note_search_state.vault_id = note.vault_id AND note_search_state.path = note.path AND note_search_state.content_hash = note.content_hash")
		q = q.Where("(id IN (?) OR NOT EXISTS (?))", posting, ready)
	}
	var rows []*model.Note
	if err := q.Order("id ASC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	result := make([]*domain.Note, 0, len(rows))
	needle := strings.ToLower(query)
	for _, m := range rows {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		n, err := r.toDomain(m, uid)
		if err != nil {
			return nil, 0, err
		}
		var state noteSearchState
		err = db.Session(&gorm.Session{NewDB: true}).Where("note_id = ?", m.ID).First(&state).Error
		if err != nil && err != gorm.ErrRecordNotFound {
			return nil, 0, err
		}
		if err == gorm.ErrRecordNotFound || state.VaultID != m.VaultID || state.Path != m.Path || state.ContentHash != m.ContentHash {
			// A concurrent writer can win or lose this backfill. Fingerprint comparison
			// on the next search guarantees that stale readiness cannot hide that write.
			if err := r.indexLiteralNote(ctx, db, m, n.Content); err != nil {
				return nil, 0, err
			}
		}
		pathMatch := strings.Contains(strings.ToLower(n.Path), needle)
		contentMatch := strings.Contains(strings.ToLower(n.Content), needle)
		if (mode == "path" && pathMatch) || (mode == "content" && contentMatch) || (mode == "auto" && (pathMatch || contentMatch)) {
			result = append(result, n)
		}
	}
	if len(rows) < limit {
		return result, 0, nil
	}
	return result, rows[len(rows)-1].ID, nil
}

var _ domain.NoteSearchRepository = (*noteRepository)(nil)
