package escrow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Store is the persistence interface for deals.
type Store interface {
	Save(deal *Deal) error
	Load(id string) (*Deal, error)
	List() ([]*Deal, error)
}

// FileStore implements Store using JSON files in a directory.
type FileStore struct {
	dir string
}

// NewFileStore creates a FileStore, creating the directory if needed.
func NewFileStore(dir string) (*FileStore, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create store directory: %w", err)
	}
	return &FileStore{dir: dir}, nil
}

func (s *FileStore) path(id string) string {
	return filepath.Join(s.dir, id+".json")
}

// Save writes a deal to disk as JSON.
func (s *FileStore) Save(deal *Deal) error {
	data, err := json.MarshalIndent(deal, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal deal: %w", err)
	}
	if err := os.WriteFile(s.path(deal.ID), data, 0600); err != nil {
		return fmt.Errorf("failed to write deal file: %w", err)
	}
	return nil
}

// Load reads a deal from disk by ID.
func (s *FileStore) Load(id string) (*Deal, error) {
	data, err := os.ReadFile(s.path(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("deal %s not found", id)
		}
		return nil, fmt.Errorf("failed to read deal file: %w", err)
	}
	var deal Deal
	if err := json.Unmarshal(data, &deal); err != nil {
		return nil, fmt.Errorf("failed to unmarshal deal: %w", err)
	}
	return &deal, nil
}

// List returns all deals in the store.
func (s *FileStore) List() ([]*Deal, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read store directory: %w", err)
	}

	var deals []*Deal
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := entry.Name()[:len(entry.Name())-5] // strip .json
		deal, err := s.Load(id)
		if err != nil {
			return nil, err
		}
		deals = append(deals, deal)
	}
	return deals, nil
}
