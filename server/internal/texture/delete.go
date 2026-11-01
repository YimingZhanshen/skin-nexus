package texture

import (
	"errors"
	"io/fs"
	"os"
)

// Remove deletes one content-addressed file. A missing file is not an error:
// the row may already have been removed by an earlier attempt, and the database
// deletion that this follows must not be undone by the filesystem.
func (s *Store) Remove(hash string) error {
	p, err := s.Path(hash)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
