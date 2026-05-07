// Package transcript persists the chat conversation between user and agent.
// This is the REPL/one-shot conversation log — distinct from KonceptOS work
// sessions in internal/session. Stored under .kcpos/transcripts/ relative to
// the user's cwd.
package transcript

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/creator915/Koncept_OS/internal/llm"
)

type Transcript struct {
	ID       string
	Dir      string
	Messages []llm.Message
}

func dirIn(cwd string) string {
	return filepath.Join(cwd, ".kcpos", "transcripts")
}

func New(cwd string) *Transcript {
	return &Transcript{
		ID:  time.Now().Format("20060102-150405"),
		Dir: dirIn(cwd),
	}
}

func Load(cwd, id string) (*Transcript, error) {
	dir := dirIn(cwd)
	if id == "latest" {
		latest, err := LatestID(dir)
		if err != nil {
			return nil, err
		}
		id = latest
	}
	path := filepath.Join(dir, id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load transcript %s: %w", id, err)
	}
	var parsed struct {
		ID       string         `json:"id"`
		Messages []llm.Message `json:"messages"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("parse transcript %s: %w", id, err)
	}
	return &Transcript{ID: parsed.ID, Dir: dir, Messages: parsed.Messages}, nil
}

func LatestID(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("read transcript dir: %w", err)
	}
	var ids []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".json") {
			ids = append(ids, strings.TrimSuffix(name, ".json"))
		}
	}
	if len(ids) == 0 {
		return "", fmt.Errorf("no transcripts found in %s", dir)
	}
	sort.Strings(ids)
	return ids[len(ids)-1], nil
}

func (t *Transcript) Save() error {
	if err := os.MkdirAll(t.Dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(t.Dir, t.ID+".json")
	data, err := json.MarshalIndent(struct {
		ID       string         `json:"id"`
		Messages []llm.Message `json:"messages"`
	}{t.ID, t.Messages}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	return nil
}

func (t *Transcript) Path() string {
	return filepath.Join(t.Dir, t.ID+".json")
}
