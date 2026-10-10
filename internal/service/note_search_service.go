package service

import (
	"context"
	"fmt"

	"github.com/haierkeys/fast-note-sync-service/internal/domain"
	"github.com/haierkeys/fast-note-sync-service/internal/dto"
)

// NoteSearchService is optional; callers may fall back for other implementations.
// A nonzero cursor must be followed even when the returned page is empty.
type NoteSearchService interface {
	SearchCandidates(ctx context.Context, uid int64, vault, query, mode string, afterID int64, limit int) ([]*dto.NoteDTO, int64, error)
}

func (s *noteService) SearchCandidates(ctx context.Context, uid int64, vault, query, mode string, afterID int64, limit int) ([]*dto.NoteDTO, int64, error) {
	repo, ok := s.noteRepo.(domain.NoteSearchRepository)
	if !ok {
		return nil, 0, fmt.Errorf("note repository does not support literal search")
	}
	vaultID, err := s.vaultService.MustGetID(ctx, uid, vault)
	if err != nil {
		return nil, 0, err
	}
	notes, next, err := repo.SearchCandidates(ctx, uid, vaultID, query, mode, afterID, limit)
	if err != nil {
		return nil, 0, err
	}
	result := make([]*dto.NoteDTO, 0, len(notes))
	for _, n := range notes {
		result = append(result, s.domainToDTO(n))
	}
	return result, next, nil
}

var _ NoteSearchService = (*noteService)(nil)
