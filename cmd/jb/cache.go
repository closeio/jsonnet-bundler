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

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/jsonnet-bundler/jsonnet-bundler/pkg/cache"
	"github.com/jsonnet-bundler/jsonnet-bundler/pkg/s3"
	"github.com/pkg/errors"
)

// GlobalCacheEnabled indicates if the global cache is enabled
var GlobalCacheEnabled = true

// Cache information structure
type CacheInfo struct {
	Location    string    `json:"location"`
	Entries     int       `json:"entries"`
	Size        int64     `json:"size"`
	OldestEntry time.Time `json:"oldest_entry,omitempty"`
	NewestEntry time.Time `json:"newest_entry,omitempty"`
}

// cacheStatusCommand shows the status of local and global caches
func cacheStatusCommand(workdir, jsonnetHome string) int {
	globalCacheDir := filepath.Join(os.Getenv("HOME"), ".cache", "jb")

	// Check global cache if enabled
	if GlobalCacheEnabled {
		globalInfo, err := getCacheInfo(globalCacheDir)
		if err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Error checking global cache: %v\n", err)
			return 1
		}

		// Print global cache info
		fmt.Println("Global cache:")
		if globalInfo != nil {
			printCacheInfo(*globalInfo)
		} else {
			fmt.Println("  Not initialized")
		}
	} else {
		fmt.Println("Global cache: Disabled")
	}

	return 0
}

// cacheCleanCommand function removed

// cacheFlushCommand completely empties the cache
func cacheFlushCommand(workdir, jsonnetHome string) int {
	// Only global cache is supported
	if !GlobalCacheEnabled {
		fmt.Println("Global cache is disabled")
		return 1
	}

	// Flush global cache
	globalCacheDir := filepath.Join(os.Getenv("HOME"), ".cache", "jb")
	err := flushCache(globalCacheDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error flushing global cache: %v\n", err)
		return 1
	}
	color.Green("Global cache flushed successfully")

	return 0
}

// cacheAddRemoteCommand adds a remote cache server
func cacheAddRemoteCommand(workdir, jsonnetHome string, url string) int {
	globalCacheDir := filepath.Join(os.Getenv("HOME"), ".cache", "jb")

	// Validate the URL
	if strings.HasPrefix(url, "s3://") {
		// For S3 URLs, verify we can parse it
		_, err := s3.NewClientFromURL(url)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Invalid S3 URL: %v\n", err)
			return 1
		}
	} else if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		fmt.Fprintf(os.Stderr, "Invalid URL: must start with http://, https://, or s3://\n")
		return 1
	}

	// Exit early if global cache is disabled
	if !GlobalCacheEnabled {
		fmt.Fprintf(os.Stderr, "Global cache is disabled. Cannot add remote cache.\n")
		return 1
	}

	// Get the current index without setting the Remote field
	globalLocation := cache.CacheLocation{
		Path: globalCacheDir,
	}

	globalIndex, err := cache.LoadOrCreateIndex(globalLocation)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading global cache index: %v\n", err)
		return 1
	}

	// Check if URL is already in remoteCaches
	globalFound := false
	for _, remote := range globalIndex.RemoteCaches {
		if remote == url {
			globalFound = true
			break
		}
	}

	if !globalFound {
		// Add the URL to the list and save
		globalIndex.RemoteCaches = append(globalIndex.RemoteCaches, url)
		err = cache.SaveIndex(globalLocation, globalIndex)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error saving global cache index: %v\n", err)
			return 1
		}
		fmt.Printf("Added remote cache: %s\n", url)
	} else {
		fmt.Printf("Remote cache already exists: %s\n", url)
	}

	return 0
}

// cacheListRemoteCommand lists remote cache servers
func cacheListRemoteCommand(workdir, jsonnetHome string, jsonOutput bool) int {
	globalCacheDir := filepath.Join(os.Getenv("HOME"), ".cache", "jb")

	// Exit early if global cache is disabled
	if !GlobalCacheEnabled {
		if jsonOutput {
			fmt.Println("[]")
		} else {
			fmt.Println("Global cache is disabled")
		}
		return 0
	}

	// Load global cache index
	globalLocation := cache.CacheLocation{
		Path: globalCacheDir,
	}

	globalIndex, err := cache.LoadOrCreateIndex(globalLocation)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading global cache index: %v\n", err)
		return 1
	}

	// Get remote caches
	remotes := globalIndex.RemoteCaches

	// Output
	if jsonOutput {
		output, err := json.MarshalIndent(remotes, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating JSON output: %v\n", err)
			return 1
		}
		fmt.Println(string(output))
	} else {
		if len(remotes) == 0 {
			fmt.Println("No remote caches configured")
		} else {
			fmt.Println("Remote caches:")
			for i, remote := range remotes {
				// Add prefix to show what kind of remote it is
				prefix := "HTTP"
				if strings.HasPrefix(remote, "s3://") {
					prefix = "S3"
				}
				fmt.Printf("  %d. [%s] %s\n", i+1, prefix, remote)
			}
		}
	}

	return 0
}

// cacheRemoveRemoteCommand removes a remote cache server
func cacheRemoveRemoteCommand(workdir, jsonnetHome string, url string) int {
	globalCacheDir := filepath.Join(os.Getenv("HOME"), ".cache", "jb")

	// Exit early if global cache is disabled
	if !GlobalCacheEnabled {
		fmt.Fprintf(os.Stderr, "Global cache is disabled. Cannot remove remote cache.\n")
		return 1
	}

	// Load global cache index
	globalLocation := cache.CacheLocation{
		Path: globalCacheDir,
	}

	globalIndex, err := cache.LoadOrCreateIndex(globalLocation)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading global cache index: %v\n", err)
		return 1
	}

	// Filter out the URL from remoteCaches
	found := false
	filteredRemotes := []string{}
	for _, remote := range globalIndex.RemoteCaches {
		if remote == url {
			found = true
		} else {
			filteredRemotes = append(filteredRemotes, remote)
		}
	}

	if found {
		globalIndex.RemoteCaches = filteredRemotes
		err = cache.SaveIndex(globalLocation, globalIndex)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error saving global cache index: %v\n", err)
			return 1
		}
		fmt.Printf("Removed remote cache: %s\n", url)
	} else {
		fmt.Printf("Remote cache not found: %s\n", url)
	}

	return 0
}

// cacheListCommand lists cache entries
func cacheListCommand(workdir, jsonnetHome string, jsonOutput bool) int {
	// This is a placeholder implementation - we'll list directories in the cache
	var caches []string

	// Add global cache if enabled
	if GlobalCacheEnabled {
		globalCacheDir := filepath.Join(os.Getenv("HOME"), ".cache", "jb")
		caches = append(caches, globalCacheDir)
	}

	if jsonOutput {
		// Output in JSON format
		entries := []map[string]interface{}{}
		for _, cacheDir := range caches {
			cacheEntries, err := listCacheEntries(cacheDir)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error listing cache entries: %v\n", err)
				return 1
			}
			entries = append(entries, cacheEntries...)
		}

		output, err := json.MarshalIndent(entries, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating JSON output: %v\n", err)
			return 1
		}
		fmt.Println(string(output))
	} else {
		// Output in human-readable format
		for _, cacheDir := range caches {
			cacheType := "Global"

			entries, err := listCacheEntries(cacheDir)
			if err != nil {
				if os.IsNotExist(err) {
					fmt.Printf("%s cache: Not initialized\n", cacheType)
					continue
				}
				fmt.Fprintf(os.Stderr, "Error listing %s cache entries: %v\n", cacheType, err)
				return 1
			}

			fmt.Printf("%s cache (%d entries):\n", cacheType, len(entries))
			for i, entry := range entries {
				fmt.Printf("  [%d] %s (%.2f MB)\n", i+1, entry["id"], float64(entry["size"].(int64))/1024/1024)
			}
			fmt.Println()
		}
	}

	return 0
}

// cacheSetConfigCommand function removed

// Helper functions

// getCacheInfo retrieves information about the cache
func getCacheInfo(cacheDir string) (*CacheInfo, error) {
	// Check if cache directory exists
	info, err := os.Stat(cacheDir)
	if err != nil {
		return nil, err
	}

	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", cacheDir)
	}

	// Walk the cache directory to collect statistics
	var size int64
	var oldestEntry, newestEntry time.Time
	entries := 0

	err = filepath.Walk(cacheDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip the root directory
		if path == cacheDir {
			return nil
		}

		// Count directories as cache entries
		if info.IsDir() && filepath.Dir(path) == cacheDir {
			entries++
			modTime := info.ModTime()

			// Initialize timestamps on first entry
			if oldestEntry.IsZero() || modTime.Before(oldestEntry) {
				oldestEntry = modTime
			}
			if newestEntry.IsZero() || modTime.After(newestEntry) {
				newestEntry = modTime
			}
		}

		// Sum up file sizes
		if !info.IsDir() {
			size += info.Size()
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return &CacheInfo{
		Location:    cacheDir,
		Entries:     entries,
		Size:        size,
		OldestEntry: oldestEntry,
		NewestEntry: newestEntry,
	}, nil
}

// printCacheInfo prints cache information in a human-readable format
func printCacheInfo(info CacheInfo) {
	fmt.Printf("  Location: %s\n", info.Location)
	fmt.Printf("  Entries: %d\n", info.Entries)
	fmt.Printf("  Size: %.2f MB\n", float64(info.Size)/1024/1024)

	if !info.OldestEntry.IsZero() {
		fmt.Printf("  Oldest entry: %s (%.1f days old)\n",
			info.OldestEntry.Format("2006-01-02"),
			time.Since(info.OldestEntry).Hours()/24)
	}

	if !info.NewestEntry.IsZero() {
		fmt.Printf("  Newest entry: %s (%.1f days old)\n",
			info.NewestEntry.Format("2006-01-02"),
			time.Since(info.NewestEntry).Hours()/24)
	}
}

// cleanCache removes expired entries from the cache
func cleanCache(cacheDir string, maxAgeInDays, maxSizeMB int) error {
	// Check if cache directory exists
	_, err := os.Stat(cacheDir)
	if os.IsNotExist(err) {
		// Nothing to clean
		return nil
	}
	if err != nil {
		return err
	}

	// Get current cache information
	info, err := getCacheInfo(cacheDir)
	if err != nil {
		return err
	}

	// If cache is already below limits, do nothing
	maxSizeBytes := int64(maxSizeMB) * 1024 * 1024
	if info.Size <= maxSizeBytes && time.Since(info.OldestEntry).Hours()/24 <= float64(maxAgeInDays) {
		return nil
	}

	// Find entries to clean
	entries, err := filepath.Glob(filepath.Join(cacheDir, "*"))
	if err != nil {
		return err
	}

	// Sort entries by age, oldest first
	type Entry struct {
		Path    string
		ModTime time.Time
	}

	var sortedEntries []Entry
	for _, path := range entries {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if info.IsDir() {
			sortedEntries = append(sortedEntries, Entry{
				Path:    path,
				ModTime: info.ModTime(),
			})
		}
	}

	// Remove entries that exceed age limit
	cutoffTime := time.Now().AddDate(0, 0, -maxAgeInDays)
	for _, entry := range sortedEntries {
		if entry.ModTime.Before(cutoffTime) {
			err := os.RemoveAll(entry.Path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: Failed to remove %s: %v\n", entry.Path, err)
			}
		}
	}

	// Recheck cache size after age-based cleanup
	info, err = getCacheInfo(cacheDir)
	if err != nil {
		return err
	}

	// If still over size limit, remove oldest entries until under limit
	if info.Size > maxSizeBytes {
		// Re-scan entries after previous cleanup
		entries, err = filepath.Glob(filepath.Join(cacheDir, "*"))
		if err != nil {
			return err
		}

		// Rebuild sorted entries
		sortedEntries = []Entry{}
		for _, path := range entries {
			info, err := os.Stat(path)
			if err != nil {
				continue
			}
			if info.IsDir() {
				sortedEntries = append(sortedEntries, Entry{
					Path:    path,
					ModTime: info.ModTime(),
				})
			}
		}

		// Remove oldest entries until under size limit
		for _, entry := range sortedEntries {
			err := os.RemoveAll(entry.Path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: Failed to remove %s: %v\n", entry.Path, err)
				continue
			}

			// Recheck cache size after removal
			info, err = getCacheInfo(cacheDir)
			if err != nil {
				return err
			}

			if info.Size <= maxSizeBytes {
				break
			}
		}
	}

	return nil
}

// flushCache completely empties the cache but preserves the index.json file
func flushCache(cacheDir string) error {
	// Check if cache directory exists
	_, err := os.Stat(cacheDir)
	if os.IsNotExist(err) {
		// Nothing to flush
		return nil
	}
	if err != nil {
		return err
	}

	// Save the index file if it exists
	indexPath := filepath.Join(cacheDir, cache.IndexFilename)
	var indexData []byte
	indexExists := false

	if _, err := os.Stat(indexPath); err == nil {
		// Read the index file
		indexData, err = os.ReadFile(indexPath)
		if err == nil {
			indexExists = true
		}
	}

	// Get all entries
	entries, err := filepath.Glob(filepath.Join(cacheDir, "*"))
	if err != nil {
		return err
	}

	// Remove all entries except the index file
	for _, entry := range entries {
		// Skip the index file
		if filepath.Base(entry) == cache.IndexFilename {
			continue
		}

		err := os.RemoveAll(entry)
		if err != nil {
			return errors.Wrap(err, fmt.Sprintf("failed to remove %s", entry))
		}
	}

	// If an index file existed, load it and update it
	if indexExists {
		// Parse the index
		var index cache.CacheIndex
		if err := json.Unmarshal(indexData, &index); err == nil {
			// Update the index to show empty entries but keep the remote caches
			index.Entries = []cache.CacheEntry{}
			index.TotalSize = 0
			index.LastCleaned = time.Now()

			// Save the updated index
			location := cache.CacheLocation{Path: cacheDir}
			if err := cache.SaveIndex(location, &index); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: Failed to update index file: %v\n", err)
			}
		}
	}

	return nil
}

// listCacheEntries returns a list of cache entries
func listCacheEntries(cacheDir string) ([]map[string]interface{}, error) {
	// Check if cache directory exists
	_, err := os.Stat(cacheDir)
	if err != nil {
		return nil, err
	}

	// List all files in the cache directory
	entries, err := filepath.Glob(filepath.Join(cacheDir, "*"))
	if err != nil {
		return nil, err
	}

	var result []map[string]interface{}
	for _, entry := range entries {
		info, err := os.Stat(entry)
		if err != nil {
			continue
		}

		baseName := filepath.Base(entry)

		// Skip directories (like repos) and index.json
		if info.IsDir() || baseName == cache.IndexFilename {
			continue
		}

		// Only process .tar.gz files
		if strings.HasSuffix(baseName, ".tar.gz") {
			// Extract the hash from the filename (remove .tar.gz extension)
			id := strings.TrimSuffix(baseName, ".tar.gz")

			result = append(result, map[string]interface{}{
				"id":          id,
				"path":        entry,
				"size":        info.Size(),
				"last_access": info.ModTime(),
			})
		}
	}

	return result, nil
}
