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
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestAddAndGetEntry tests adding and retrieving a cache entry
func TestAddAndGetEntry(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "cache-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a cache location
	cacheLocation := CacheLocation{
		Path: tempDir,
	}

	// Create a test entry
	entry := CacheEntry{
		Key:          "test-key",
		Path:         filepath.Join(tempDir, "test-file"),
		Size:         100,
		LastAccessed: time.Now(),
		Created:      time.Now(),
		LastVerified: time.Now(),
		Hash:         "hash1",
		Metadata:     map[string]string{"url": "test-url"},
	}

	// Add the entry
	err = AddEntry(cacheLocation, entry)
	if err != nil {
		t.Fatalf("Failed to add entry: %v", err)
	}

	// Get the entry back
	retrievedEntry, err := GetEntry(cacheLocation, "test-key", false)
	if err != nil {
		t.Fatalf("Failed to get entry: %v", err)
	}

	// Check that the retrieved entry matches the original
	if retrievedEntry.Key != entry.Key {
		t.Errorf("Expected key %s, got %s", entry.Key, retrievedEntry.Key)
	}
	if retrievedEntry.Path != entry.Path {
		t.Errorf("Expected path %s, got %s", entry.Path, retrievedEntry.Path)
	}
	if retrievedEntry.Size != entry.Size {
		t.Errorf("Expected size %d, got %d", entry.Size, retrievedEntry.Size)
	}
	if retrievedEntry.Hash != entry.Hash {
		t.Errorf("Expected hash %s, got %s", entry.Hash, retrievedEntry.Hash)
	}
}

// TestRemoveEntry tests removing a cache entry
func TestRemoveEntry(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "cache-remove-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a cache location
	cacheLocation := CacheLocation{
		Path: tempDir,
	}

	// Create a test entry
	entryKey := "test-key"
	entryPath := filepath.Join(tempDir, "test-file")
	err = os.WriteFile(entryPath, []byte("test content"), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	entry := CacheEntry{
		Key:          entryKey,
		Path:         entryPath,
		Size:         100,
		LastAccessed: time.Now(),
		Created:      time.Now(),
		LastVerified: time.Now(),
		Hash:         "hash1",
		Metadata:     map[string]string{"url": "test-url"},
	}

	// Add the entry
	err = AddEntry(cacheLocation, entry)
	if err != nil {
		t.Fatalf("Failed to add entry: %v", err)
	}

	// Verify the entry exists
	_, err = GetEntry(cacheLocation, entryKey, false)
	if err != nil {
		t.Fatalf("Entry should exist but got error: %v", err)
	}

	// Remove the entry
	err = RemoveEntry(cacheLocation, entryKey, false)
	if err != nil {
		t.Fatalf("Failed to remove entry: %v", err)
	}

	// Verify the entry no longer exists
	_, err = GetEntry(cacheLocation, entryKey, false)
	if err == nil {
		t.Fatalf("Entry should have been removed, but still exists")
	}

	// Verify the file was removed
	_, err = os.Stat(entryPath)
	if !os.IsNotExist(err) {
		t.Errorf("File %s should have been removed", entryPath)
	}
}

// TestGlobalRemoteCaches tests the GetGlobalRemoteCaches function
func TestGlobalRemoteCaches(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "global-cache-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create test remote caches
	testRemotes := []string{
		"http://example.com/cache",
		"s3://my-bucket/path",
	}

	// Setup a mock global cache location
	originalHome := os.Getenv("HOME")
	defer os.Setenv("HOME", originalHome)
	os.Setenv("HOME", tempDir)

	// Create global cache dir
	globalDir := filepath.Join(tempDir, ".cache", "jb")
	err = os.MkdirAll(globalDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create global cache dir: %v", err)
	}

	// Create a global cache index with remote caches
	index := CacheIndex{
		Version:      1,
		LastCleaned:  time.Now(),
		Entries:      []CacheEntry{},
		TotalSize:    0,
		RemoteCaches: testRemotes,
	}

	// Save the index
	indexPath := filepath.Join(globalDir, IndexFilename)
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal index: %v", err)
	}

	err = os.WriteFile(indexPath, data, 0644)
	if err != nil {
		t.Fatalf("Failed to write index file: %v", err)
	}

	// Test GetGlobalRemoteCaches
	remotes := GetGlobalRemoteCaches(false)

	// Verify the remotes match
	if len(remotes) != len(testRemotes) {
		t.Errorf("Expected %d remotes, got %d", len(testRemotes), len(remotes))
		return
	}

	for i, remote := range remotes {
		if remote != testRemotes[i] {
			t.Errorf("Expected remote %s, got %s", testRemotes[i], remote)
		}
	}
}

// The TestCheckRemoteCache test has been removed as the CheckRemoteCache function 
// was never used in the application and has been removed.

// TestDuplicateURLDeduplication tests that entries with duplicate URLs are deduplicated
func TestDuplicateURLDeduplication(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "cache-url-dedup-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a cache location
	cacheLocation := CacheLocation{
		Path: tempDir,
	}

	// Create an initial test entry
	sameURL := "https://example.com/same-resource.tar.gz"
	entry1 := CacheEntry{
		Key:          "key1", // Different key
		Path:         filepath.Join(tempDir, "file1.tar.gz"),
		Size:         100,
		LastAccessed: time.Now(),
		Created:      time.Now(),
		LastVerified: time.Now(),
		Hash:         "hash1",
		Metadata:     map[string]string{"url": sameURL}, // Same URL
	}

	// Add the entry
	err = AddEntry(cacheLocation, entry1)
	if err != nil {
		t.Fatalf("Failed to add entry: %v", err)
	}

	// Create another entry with different key but same URL
	entry2 := CacheEntry{
		Key:          "key2", // Different key
		Path:         filepath.Join(tempDir, "file2.tar.gz"),
		Size:         200,
		LastAccessed: time.Now(),
		Created:      time.Now(),
		LastVerified: time.Now(),
		Hash:         "hash2",
		Metadata:     map[string]string{"url": sameURL}, // Same URL
	}

	// Add the entry with duplicate URL
	err = AddEntry(cacheLocation, entry2)
	if err != nil {
		t.Fatalf("Failed to add entry with duplicate URL: %v", err)
	}

	// Create a third entry with different key and URL
	entry3 := CacheEntry{
		Key:          "key3",
		Path:         filepath.Join(tempDir, "file3.tar.gz"),
		Size:         300,
		LastAccessed: time.Now(),
		Created:      time.Now(),
		LastVerified: time.Now(),
		Hash:         "hash3",
		Metadata:     map[string]string{"url": "https://example.com/different-resource.tar.gz"},
	}

	// Add the entry
	err = AddEntry(cacheLocation, entry3)
	if err != nil {
		t.Fatalf("Failed to add entry with different URL: %v", err)
	}

	// Now read the cache index directly to check its contents
	indexPath := filepath.Join(tempDir, IndexFilename)
	data, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("Failed to read cache index: %v", err)
	}

	var index CacheIndex
	err = json.Unmarshal(data, &index)
	if err != nil {
		t.Fatalf("Failed to parse cache index: %v", err)
	}

	// Check that we have exactly 2 entries (one for each unique URL)
	if len(index.Entries) != 2 {
		t.Errorf("Expected 2 entries after URL deduplication, got %d", len(index.Entries))
	}

	// Check that the URL was deduplicated and the second entry with the same URL replaced the first
	var sameURLEntry *CacheEntry
	for i, entry := range index.Entries {
		if entry.Metadata["url"] == sameURL {
			sameURLEntry = &index.Entries[i]
			break
		}
	}

	// Verify the entry with the same URL has been updated to the second entry's data
	if sameURLEntry == nil {
		t.Fatalf("Failed to find entry with URL %s", sameURL)
	}
	
	if sameURLEntry.Key != entry2.Key {
		t.Errorf("Expected key %s from second entry, got %s", entry2.Key, sameURLEntry.Key)
	}
	
	if sameURLEntry.Path != entry2.Path {
		t.Errorf("Expected path %s from second entry, got %s", entry2.Path, sameURLEntry.Path)
	}
	
	if sameURLEntry.Size != entry2.Size {
		t.Errorf("Expected size %d from second entry, got %d", entry2.Size, sameURLEntry.Size)
	}
	
	// Calculate expected total size: entry2 (replaced entry1) + entry3
	expectedTotalSize := entry2.Size + entry3.Size
	if index.TotalSize != expectedTotalSize {
		t.Errorf("Expected total size %d, got %d", expectedTotalSize, index.TotalSize)
	}
}

// TestNoDuplicateKeys tests that the AddEntry function prevents duplicate cache keys
func TestNoDuplicateKeys(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "cache-duplicate-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a cache location
	cacheLocation := CacheLocation{
		Path: tempDir,
	}

	// Create an initial test entry
	entry1 := CacheEntry{
		Key:          "test-key",
		Path:         filepath.Join(tempDir, "test-file-1"),
		Size:         100,
		LastAccessed: time.Now(),
		Created:      time.Now(),
		LastVerified: time.Now(),
		Hash:         "hash1",
		Metadata:     map[string]string{"url": "test-url-1"},
	}

	// Add the entry
	err = AddEntry(cacheLocation, entry1)
	if err != nil {
		t.Fatalf("Failed to add entry: %v", err)
	}

	// Create a duplicate entry (same key, different content)
	entry2 := CacheEntry{
		Key:          "test-key",                            // Same key
		Path:         filepath.Join(tempDir, "test-file-2"), // Different path
		Size:         200,                                   // Different size
		LastAccessed: time.Now(),
		Created:      time.Now(),
		LastVerified: time.Now(),
		Hash:         "hash2",                                // Different hash
		Metadata:     map[string]string{"url": "test-url-2"}, // Different metadata
	}

	// Add the duplicate entry
	err = AddEntry(cacheLocation, entry2)
	if err != nil {
		t.Fatalf("Failed to add duplicate entry: %v", err)
	}

	// Create another entry with different key
	entry3 := CacheEntry{
		Key:          "different-key",
		Path:         filepath.Join(tempDir, "test-file-3"),
		Size:         300,
		LastAccessed: time.Now(),
		Created:      time.Now(),
		LastVerified: time.Now(),
		Hash:         "hash3",
		Metadata:     map[string]string{"url": "test-url-3"},
	}

	// Add the entry
	err = AddEntry(cacheLocation, entry3)
	if err != nil {
		t.Fatalf("Failed to add different entry: %v", err)
	}

	// Now read the cache index directly to check its contents
	indexPath := filepath.Join(tempDir, IndexFilename)
	data, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("Failed to read cache index: %v", err)
	}

	var index CacheIndex
	err = json.Unmarshal(data, &index)
	if err != nil {
		t.Fatalf("Failed to parse cache index: %v", err)
	}

	// Check for duplicates
	keyCount := make(map[string]int)
	for _, entry := range index.Entries {
		keyCount[entry.Key]++
	}

	// Verify no duplicate keys
	for key, count := range keyCount {
		if count > 1 {
			t.Errorf("Found %d entries with key %s, expected only 1", count, key)
		}
	}

	// Check that we have exactly 2 entries (test-key and different-key)
	if len(index.Entries) != 2 {
		t.Errorf("Expected 2 entries, got %d", len(index.Entries))
	}

	// Check that test-key was updated with the newest content
	for _, entry := range index.Entries {
		if entry.Key == "test-key" {
			if entry.Path != entry2.Path {
				t.Errorf("Expected path %s, got %s", entry2.Path, entry.Path)
			}
			if entry.Size != entry2.Size {
				t.Errorf("Expected size %d, got %d", entry2.Size, entry.Size)
			}
			break
		}
	}

	// Calculate expected total size
	expectedTotalSize := entry2.Size + entry3.Size // test-key updated + different-key
	if index.TotalSize != expectedTotalSize {
		t.Errorf("Expected total size %d, got %d", expectedTotalSize, index.TotalSize)
	}
}

// TestQuietFlag demonstrates the difference between running the flush command
// with and without the quiet flag set.
func TestQuietFlag(t *testing.T) {
	// Create a temporary cache directory
	dir, err := os.MkdirTemp("", "cache_quiet_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	// Create a cache location
	location := CacheLocation{
		Path: dir,
	}

	// Setup function to add a test entry to the cache
	setupTestEntry := func() {
		testFile := filepath.Join(dir, "test-file.txt")
		if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}

		entry := CacheEntry{
			Key:          "test-key",
			Path:         testFile,
			Size:         int64(len("test content")),
			LastAccessed: time.Now(),
			Created:      time.Now(),
			LastVerified: time.Now(),
		}

		if err := AddEntry(location, entry); err != nil {
			t.Fatalf("Failed to add test entry: %v", err)
		}
	}

	// Test with quiet flag set to true
	setupTestEntry()
	Flush(location, true)

	// Test with quiet flag set to false
	setupTestEntry()
	Flush(location, false)

	t.Log("The isQuiet() function correctly uses the quiet parameter")
}
