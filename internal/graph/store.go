package graph

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// DefaultPath is the conventional location for the hypergraph file,
// relative to the project root (cwd when kcpos runs).
const DefaultPath = "K/graph.json"

// Load reads a graph from path. The file must exist.
func Load(path string) (*Graph, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	g := NewGraph()
	if err := json.Unmarshal(data, g); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if g.Attributes == nil {
		g.Attributes = map[string]*Attribute{}
	}
	if g.Objects == nil {
		g.Objects = map[string]*Object{}
	}
	return g, nil
}

// LoadOrInit returns the graph at path, creating an empty one if the file
// does not exist. The empty graph is not written until Save is called.
func LoadOrInit(path string) (*Graph, error) {
	g, err := Load(path)
	if err == nil {
		return g, nil
	}
	if errors.Is(err, fs.ErrNotExist) || isNotExistWrapped(err) {
		return NewGraph(), nil
	}
	return nil, err
}

// Save writes the graph to path with parent directories created as needed.
func Save(path string, g *Graph) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// isNotExistWrapped checks for fs.ErrNotExist underneath a fmt.Errorf wrap.
func isNotExistWrapped(err error) bool {
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
