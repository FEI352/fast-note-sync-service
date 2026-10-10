package domain

import "context"

// NoteSearchRepository is an optional literal, case-insensitive search capability.
// Results and cursors use ascending note IDs. A zero cursor means exhaustion.
type NoteSearchRepository interface {
	SearchCandidates(ctx context.Context, uid, vaultID int64, query, mode string, afterID int64, limit int) ([]*Note, int64, error)
}
