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

package cacheregister

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/fatih/color"
	"github.com/pkg/errors"
)

// CacheEntry represents an entry in the cache index
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

// CacheIndex is the top-level structure for the cache index
type CacheIndex struct {
	Entries    []CacheEntry `json:"entries"`
	TotalSize  int64        `json:"total_size"`
	LastUpdate time.Time    `json:"last_update"`
}

// RegisterCacheFile adds a file to the cache index
func RegisterCacheFile(filePath, url string, isQuiet bool) error {
	// Check if file exists
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		if !isQuiet {
			color.Red("Error: %v", err)
		}
		return errors.Wrap(err, "failed to stat file")
	}

	// Get the directory where the file is located
	fileDir := filepath.Dir(filePath)

	// Add entry to the index in the file's directory
	entry := CacheEntry{
		Key:          filepath.Base(filePath),
		Path:         filePath,
		Size:         fileInfo.Size(),
		LastAccessed: time.Now(),
		Created:      time.Now(),
		LastVerified: time.Now(),
		Hash:         hashFile(filePath),
		Metadata: map[string]string{
			"url": url,
		},
	}

	// Add to index in file's directory
	err = addToIndex(fileDir, entry, isQuiet)
	if err != nil {
		if !isQuiet {
			color.Red("Error adding to directory index: %v", err)
		}
		return err
	}

	// If this directory is a subdirectory of a cache directory, also add to parent cache index
	cacheParentDir := findCacheParent(fileDir)
	if cacheParentDir != "" && cacheParentDir != fileDir {
		err = addToIndex(cacheParentDir, entry, isQuiet)
		if err != nil {
			if !isQuiet {
				color.Yellow("Warning: Could not add to parent cache index: %v", err)
			}
			// Don't return error for parent cache index - it's not critical
		}
	}

	if !isQuiet {
		color.Green("Added %s to cache index", filePath)
	}

	return nil
}

// findCacheParent finds the nearest parent directory named "cache"
func findCacheParent(dir string) string {
	// Start with the given directory
	current := dir

	// Walk up the directory tree
	for {
		// If the current directory is named "cache", return it
		if filepath.Base(current) == "cache" {
			return current
		}

		// Go up one level
		parent := filepath.Dir(current)

		// If we're at the root or we didn't move up, stop
		if parent == current {
			return ""
		}

		// Continue with the parent
		current = parent
	}
}

// addToIndex adds an entry to the index file
func addToIndex(dir string, entry CacheEntry, isQuiet bool) error {
	// Make sure the directory exists
	if err := os.MkdirAll(dir, 0755); err != nil {
		return errors.Wrap(err, "failed to create directory")
	}

	// Path to the index file
	indexPath := filepath.Join(dir, "index.json")

	// Load existing index or create a new one
	var index CacheIndex
	if _, err := os.Stat(indexPath); err == nil {
		// Load existing index
		data, err := os.ReadFile(indexPath)
		if err != nil {
			return errors.Wrap(err, "failed to read index file")
		}

		if err := json.Unmarshal(data, &index); err != nil {
			return errors.Wrap(err, "failed to parse index file")
		}
	} else {
		// Create a new index
		index = CacheIndex{
			Entries:    []CacheEntry{},
			TotalSize:  0,
			LastUpdate: time.Now(),
		}
	}

	// Check if entry already exists by path
	entryExists := false
	for i, e := range index.Entries {
		if e.Path == entry.Path {
			// Update existing entry
			// Subtract old size from total
			index.TotalSize -= e.Size
			// Update the entry
			index.Entries[i] = entry
			entryExists = true
			break
		}
	}

	// Also check if there's a duplicate by URL in metadata
	if !entryExists && entry.Metadata != nil {
		if url, ok := entry.Metadata["url"]; ok && url != "" {
			for i, e := range index.Entries {
				if e.Metadata != nil {
					if existingURL, ok := e.Metadata["url"]; ok && existingURL == url {
						// Found a duplicate by URL, update it instead of adding a new one
						index.TotalSize -= e.Size
						// Update the entry
						index.Entries[i] = entry
						entryExists = true
						break
					}
				}
			}
		}
	}

	// Add new entry if it doesn't exist
	if !entryExists {
		index.Entries = append(index.Entries, entry)
	}

	// Update total size
	index.TotalSize += entry.Size
	index.LastUpdate = time.Now()

	// Write index to file
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return errors.Wrap(err, "failed to marshal index")
	}

	if err := os.WriteFile(indexPath, data, 0644); err != nil {
		return errors.Wrap(err, "failed to write index file")
	}

	return nil
}

// hashFile computes a SHA-256 hash of a file's contents
func hashFile(filePath string) string {
	// For now, return an empty hash
	// In a real implementation, this would read the file and compute its hash
	return ""
}
