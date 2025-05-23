// Copyright 2018 jsonnet-bundler authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fatih/color"
	"github.com/pkg/errors"
)

// IndexFilename is the name of the index file in the cache
const IndexFilename = "index.json"

// Default cache settings
const (
	defaultVersion = 1
)

// CacheLocation represents information about where a cache is stored
type CacheLocation struct {
	Path   string `json:"path"`
	Remote string `json:"remote,omitempty"`
}

// CacheEntry represents a single item in the cache
type CacheEntry struct {
	Key          string            `json:"key"`
	Path         string            `json:"path"`
	Size         int64             `json:"size"`
	LastAccessed time.Time         `json:"last_accessed"`
	Created      time.Time         `json:"created"`
	LastVerified time.Time         `json:"last_verified"`
	Hash         string            `json:"hash,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// CacheIndex stores metadata for all cached items
type CacheIndex struct {
	Version      int          `json:"version"`
	LastCleaned  time.Time    `json:"lastCleaned"`
	Entries      []CacheEntry `json:"entries"`
	TotalSize    int64        `json:"totalSize"`
	RemoteCaches []string     `json:"remoteCaches"`
}

// GetCacheDir returns the global cache directory
func GetCacheDir() (string, error) {
	// Check for custom global cache dir in environment variable
	if envDir := os.Getenv("JB_CACHE_DIR"); envDir != "" {
		return envDir, nil
	}

	// Use default home directory cache
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", errors.Wrap(err, "failed to get user home directory")
	}

	return filepath.Join(homeDir, ".cache", "jb"), nil
}

// isQuiet is a local function to avoid import cycles
func isQuiet(quietFlag bool) bool {
	return quietFlag
}

// LoadOrCreateIndex loads the cache index or creates a new one
func LoadOrCreateIndex(location CacheLocation) (*CacheIndex, error) {
	indexPath := filepath.Join(location.Path, IndexFilename)

	// Ensure the cache directory exists
	if err := os.MkdirAll(location.Path, 0755); err != nil {
		return nil, errors.Wrap(err, "failed to create cache directory")
	}

	// Check if index file exists
	index := &CacheIndex{
		Version:      defaultVersion,
		LastCleaned:  time.Now(),
		Entries:      []CacheEntry{},
		TotalSize:    0,
		RemoteCaches: []string{},
	}

	// If a remote was specified, add it to remoteCaches
	if location.Remote != "" {
		index.RemoteCaches = append(index.RemoteCaches, location.Remote)
	}

	// Try to load existing index
	if _, err := os.Stat(indexPath); err == nil {
		data, err := os.ReadFile(indexPath)
		if err != nil {
			return nil, errors.Wrap(err, "failed to read index file")
		}

		if err := json.Unmarshal(data, index); err != nil {
			return nil, errors.Wrap(err, "failed to parse index file")
		}

		// If a remote was specified and not already in list, add it
		if location.Remote != "" {
			found := false
			for _, r := range index.RemoteCaches {
				if r == location.Remote {
					found = true
					break
				}
			}
			if !found {
				index.RemoteCaches = append(index.RemoteCaches, location.Remote)
			}
		}
	}

	return index, nil
}

// SaveIndex saves the cache index to disk
func SaveIndex(location CacheLocation, index *CacheIndex) error {
	// Ensure directory exists
	if err := os.MkdirAll(location.Path, 0755); err != nil {
		return errors.Wrap(err, "failed to create cache directory")
	}

	// Write index file
	indexPath := filepath.Join(location.Path, IndexFilename)
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return errors.Wrap(err, "failed to serialize index")
	}

	if err := os.WriteFile(indexPath, data, 0644); err != nil {
		return errors.Wrap(err, "failed to write index file")
	}

	return nil
}

// AddEntry adds an entry to the cache
func AddEntry(location CacheLocation, entry CacheEntry) error {
	index, err := LoadOrCreateIndex(location)
	if err != nil {
		return err
	}

	// Check if entry already exists by key
	updated := false
	for i, e := range index.Entries {
		if e.Key == entry.Key {
			// Update existing entry
			index.TotalSize = index.TotalSize - e.Size + entry.Size
			index.Entries[i] = entry
			updated = true
			break
		}
	}

	// Also check if there's a duplicate by URL in metadata
	if !updated && entry.Metadata != nil {
		if url, ok := entry.Metadata["url"]; ok && url != "" {
			for i, e := range index.Entries {
				if e.Metadata != nil {
					if existingURL, ok := e.Metadata["url"]; ok && existingURL == url {
						// Found a duplicate by URL, update it instead of adding a new one
						index.TotalSize = index.TotalSize - e.Size + entry.Size
						index.Entries[i] = entry
						updated = true
						break
					}
				}
			}
		}
	}

	// Add new entry if not updated
	if !updated {
		index.Entries = append(index.Entries, entry)
		index.TotalSize += entry.Size
	}

	return SaveIndex(location, index)
}

// GetEntry retrieves an entry from the cache
func GetEntry(location CacheLocation, key string, quiet bool) (CacheEntry, error) {
	index, err := LoadOrCreateIndex(location)
	if err != nil {
		return CacheEntry{}, err
	}

	// Find entry by key
	for i, entry := range index.Entries {
		if entry.Key == key {
			// Update last accessed time
			entry.LastAccessed = time.Now()
			index.Entries[i] = entry

			// Save the updated index
			if err := SaveIndex(location, index); err != nil {
				if !isQuiet(quiet) {
					color.Yellow("Warning: Failed to update access time: %v", err)
				}
			}

			return entry, nil
		}
	}

	return CacheEntry{}, fmt.Errorf("entry not found: %s", key)
}

// RemoveEntry removes an entry from the cache
func RemoveEntry(location CacheLocation, key string, quiet bool) error {
	index, err := LoadOrCreateIndex(location)
	if err != nil {
		return err
	}

	// Find entry to remove
	found := false
	newEntries := []CacheEntry{}
	for _, entry := range index.Entries {
		if entry.Key == key {
			found = true
			index.TotalSize -= entry.Size

			// Remove the file or directory if it exists
			if entry.Path != "" {
				if _, err := os.Stat(entry.Path); err == nil {
					if err := os.RemoveAll(entry.Path); err != nil {
						if !isQuiet(quiet) {
							color.Yellow("Warning: Failed to remove %s: %v", entry.Path, err)
						}
					}
				}
			}
		} else {
			newEntries = append(newEntries, entry)
		}
	}

	if !found {
		return fmt.Errorf("entry not found: %s", key)
	}

	// Update entries and save
	index.Entries = newEntries
	return SaveIndex(location, index)
}

// Flush completely empties the cache
func Flush(location CacheLocation, quiet bool) error {
	index, err := LoadOrCreateIndex(location)
	if err != nil {
		return err
	}

	// Remove all entries
	for _, entry := range index.Entries {
		if entry.Path != "" {
			if _, err := os.Stat(entry.Path); err == nil {
				if err := os.RemoveAll(entry.Path); err != nil {
					if !isQuiet(quiet) {
						color.Yellow("Warning: Failed to remove %s: %v", entry.Path, err)
					}
				}
			}
		}
	}

	// Clear index
	index.Entries = []CacheEntry{}
	index.TotalSize = 0
	index.LastCleaned = time.Now()

	if !isQuiet(quiet) {
		color.Green("Cache flushed successfully")
	}

	// Save empty index
	return SaveIndex(location, index)
}

// GetGlobalRemoteCaches returns the list of remote caches from the global cache.
func GetGlobalRemoteCaches(quiet bool) []string {
	// Get global cache directory
	globalCacheDir, err := GetCacheDir()
	if err != nil {
		if !isQuiet(quiet) {
			color.Yellow("Warning: Failed to get cache directory: %v", err)
		}
		return nil
	}

	// Load global cache index
	globalLocation := CacheLocation{
		Path: globalCacheDir,
	}

	globalIndex, err := LoadOrCreateIndex(globalLocation)
	if err != nil {
		if !isQuiet(quiet) {
			color.Yellow("Warning: Failed to load global cache index: %v", err)
		}
		return nil
	}

	return globalIndex.RemoteCaches
}
