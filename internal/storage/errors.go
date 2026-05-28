package storage

import "fmt"

type StorageError struct {
	Op   string
	Path string
	Err  error
}

func (e *StorageError) Error() string {
	return fmt.Sprintf("storage %s error on %s: %v", e.Op, e.Path, e.Err)
}

func (e *StorageError) Unwrap() error {
	return e.Err
}

var (
	ErrNotFound   = fmt.Errorf("resource not found")
	ErrPermission = fmt.Errorf("permission denied")
	ErrNoSpace    = fmt.Errorf("no space left on device")
	ErrCorrupt    = fmt.Errorf("data corruption detected")
)
