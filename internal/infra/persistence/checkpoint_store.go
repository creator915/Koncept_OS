package persistence

import "github.com/creator915/Koncept_OS/internal/domain/checkpoint"

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// DefaultPath is the conventional location for the checkpoint file,
// relative to the project root (cwd when kcpos runs).
const CheckpointDefaultPath = "K/checkpoint.json"

// Load reads a checkpoint from path. The file must exist.
func LoadCheckpoint(path string) (*checkpoint.Checkpoint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	c := checkpoint.New()
	if err := json.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if c.Items == nil {
		c.Items = []checkpoint.Item{}
	}
	return c, nil
}

// LoadOrInit returns the checkpoint at path, or a fresh empty one if the
// file does not exist. The empty checkpoint is not written until Save.
func LoadCheckpointOrInit(path string) (*checkpoint.Checkpoint, error) {
	c, err := LoadCheckpoint(path)
	if err == nil {
		return c, nil
	}
	if errors.Is(err, fs.ErrNotExist) || isNotExistWrappedCheckpoint(err) {
		return checkpoint.New(), nil
	}
	return nil, err
}

// Save writes the checkpoint to disk, recomputing the summary first.
func SaveCheckpoint(path string, c *checkpoint.Checkpoint) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	c.RecomputeSummary()
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func isNotExistWrappedCheckpoint(err error) bool {
	for err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return true
		}
		unwrapped := errors.Unwrap(err)
		if unwrapped == err {
			return false
		}
		err = unwrapped
	}
	return false
}
