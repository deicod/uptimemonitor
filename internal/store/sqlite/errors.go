package sqlite

import "errors"

// ErrNotFound is returned by repository reads, updates, and deletes when the
// targeted row does not exist (or is soft-deleted). Callers use errors.Is to
// distinguish a missing row from an infrastructure failure — IPC handlers map
// it to a not_found error envelope.
var ErrNotFound = errors.New("sqlite: record not found")
