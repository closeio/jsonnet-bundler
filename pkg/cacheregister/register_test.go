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
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createTempDir creates a temporary directory for testing
func createTempDir(t *testing.T) string {
	dir, err := os.MkdirTemp("", "cacheregister_test")
	require.NoError(t, err)
	return dir
}

// TestRegisterCacheFile tests the RegisterCacheFile function
func TestRegisterCacheFile(t *testing.T) {
	// Create a temporary directory
	tempDir := createTempDir(t)
	defer os.RemoveAll(tempDir)

	// Create subdirectories
	cacheDir := filepath.Join(tempDir, "cache")
	subdirPath := filepath.Join(cacheDir, "subdir")
	require.NoError(t, os.MkdirAll(subdirPath, os.ModePerm))

	// Create a test file
	testFilePath := filepath.Join(subdirPath, "test-file.txt")
	testContent := "Test content"
	require.NoError(t, os.WriteFile(testFilePath, []byte(testContent), 0644))

	// Test registering the file
	testURL := "https://example.com/test-file.txt"
	err := RegisterCacheFile(testFilePath, testURL, true)
	require.NoError(t, err)

	// Check if the index file was created in the subdirectory
	subdirIndexPath := filepath.Join(subdirPath, "index.json")
	_, err = os.Stat(subdirIndexPath)
	require.NoError(t, err)

	// Verify the content of the subdirectory index
	subdirIndexData, err := os.ReadFile(subdirIndexPath)
	require.NoError(t, err)

	var subdirIndex CacheIndex
	require.NoError(t, json.Unmarshal(subdirIndexData, &subdirIndex))
	require.Equal(t, 1, len(subdirIndex.Entries))
	assert.Equal(t, testFilePath, subdirIndex.Entries[0].Path)
	assert.Equal(t, int64(len(testContent)), subdirIndex.Entries[0].Size)
	assert.Equal(t, testURL, subdirIndex.Entries[0].Metadata["url"])

	// Check if the index file was created in the main cache directory
	mainIndexPath := filepath.Join(cacheDir, "index.json")
	_, err = os.Stat(mainIndexPath)
	require.NoError(t, err)

	// Verify the content of the main cache directory index
	mainIndexData, err := os.ReadFile(mainIndexPath)
	require.NoError(t, err)

	var mainIndex CacheIndex
	require.NoError(t, json.Unmarshal(mainIndexData, &mainIndex))
	require.Equal(t, 1, len(mainIndex.Entries))
	assert.Equal(t, testFilePath, mainIndex.Entries[0].Path)
	assert.Equal(t, int64(len(testContent)), mainIndex.Entries[0].Size)
	assert.Equal(t, testURL, mainIndex.Entries[0].Metadata["url"])

	// Test updating an existing entry
	updatedURL := "https://example.com/updated-file.txt"
	err = RegisterCacheFile(testFilePath, updatedURL, true)
	require.NoError(t, err)

	// Verify the entry was updated in the subdirectory index
	subdirIndexData, err = os.ReadFile(subdirIndexPath)
	require.NoError(t, err)

	require.NoError(t, json.Unmarshal(subdirIndexData, &subdirIndex))

	// Find entry with our updated URL
	var foundSubdirEntry *CacheEntry
	for i := range subdirIndex.Entries {
		if subdirIndex.Entries[i].Path == testFilePath &&
			subdirIndex.Entries[i].Metadata["url"] == updatedURL {
			foundSubdirEntry = &subdirIndex.Entries[i]
			break
		}
	}
	require.NotNil(t, foundSubdirEntry, "Could not find the updated entry in subdir index")
	assert.Equal(t, updatedURL, foundSubdirEntry.Metadata["url"])

	// Verify the entry was updated in the main cache directory index
	mainIndexData, err = os.ReadFile(mainIndexPath)
	require.NoError(t, err)

	require.NoError(t, json.Unmarshal(mainIndexData, &mainIndex))

	// Find entry with our updated URL
	var foundMainEntry *CacheEntry
	for i := range mainIndex.Entries {
		if mainIndex.Entries[i].Path == testFilePath &&
			mainIndex.Entries[i].Metadata["url"] == updatedURL {
			foundMainEntry = &mainIndex.Entries[i]
			break
		}
	}
	require.NotNil(t, foundMainEntry, "Could not find the updated entry in main index")
	assert.Equal(t, updatedURL, foundMainEntry.Metadata["url"])
}

// TestRegisterNonExistentFile tests RegisterCacheFile with a file that doesn't exist
func TestRegisterNonExistentFile(t *testing.T) {
	// Create a temporary directory
	tempDir := createTempDir(t)
	defer os.RemoveAll(tempDir)

	// Try to register a non-existent file
	nonExistentFilePath := filepath.Join(tempDir, "non-existent-file.txt")
	testURL := "https://example.com/non-existent-file.txt"
	err := RegisterCacheFile(nonExistentFilePath, testURL, true)
	require.Error(t, err) // Should return an error

	// Verify no index file was created
	indexPath := filepath.Join(tempDir, "index.json")
	_, err = os.Stat(indexPath)
	assert.True(t, os.IsNotExist(err))
}

// TestRegisterMultipleFiles tests registering multiple files to the same cache directory
func TestRegisterMultipleFiles(t *testing.T) {
	// Create a temporary directory
	tempDir := createTempDir(t)
	defer os.RemoveAll(tempDir)

	// Create test files
	numFiles := 3
	files := make([]string, numFiles)
	urls := make([]string, numFiles)
	sizes := make([]int, numFiles)

	for i := 0; i < numFiles; i++ {
		fileNum := i + 1                    // Use 1-based numbering for more readable filenames
		fileNumStr := strconv.Itoa(fileNum) // Convert int to string properly
		files[i] = filepath.Join(tempDir, filepath.FromSlash(filepath.Clean(filepath.Join(".", "test-file-"+fileNumStr+".txt"))))
		urls[i] = "https://example.com/test-file-" + fileNumStr + ".txt"
		content := "Test content " + fileNumStr
		sizes[i] = len(content)
		require.NoError(t, os.WriteFile(files[i], []byte(content), 0644))
	}

	// Register all files
	for i := 0; i < numFiles; i++ {
		err := RegisterCacheFile(files[i], urls[i], true)
		require.NoError(t, err)
	}

	// Check the index file
	indexPath := filepath.Join(tempDir, "index.json")
	indexData, err := os.ReadFile(indexPath)
	require.NoError(t, err)

	var index CacheIndex
	require.NoError(t, json.Unmarshal(indexData, &index))
	require.Equal(t, numFiles, len(index.Entries))

	// Verify total size
	expectedTotalSize := int64(0)
	for i := 0; i < numFiles; i++ {
		expectedTotalSize += int64(sizes[i])
	}
	assert.Equal(t, expectedTotalSize, index.TotalSize)
}

// TestAddToIndex tests the addToIndex function directly
func TestAddToIndex(t *testing.T) {
	// Create a temporary directory
	tempDir := createTempDir(t)
	defer os.RemoveAll(tempDir)

	// Create a test entry
	testEntry := CacheEntry{
		Key:          "test-key",
		Path:         filepath.Join(tempDir, "test-file.txt"),
		Size:         100,
		LastAccessed: time.Now(),
		Created:      time.Now(),
		LastVerified: time.Now(),
		Hash:         "test-hash",
		Metadata: map[string]string{
			"test-key": "test-value",
		},
	}

	// Add to index
	err := addToIndex(tempDir, testEntry, true)
	require.NoError(t, err)

	// Check if the index file was created
	indexPath := filepath.Join(tempDir, "index.json")
	_, err = os.Stat(indexPath)
	require.NoError(t, err)

	// Verify the content of the index
	indexData, err := os.ReadFile(indexPath)
	require.NoError(t, err)

	var index CacheIndex
	require.NoError(t, json.Unmarshal(indexData, &index))
	require.Equal(t, 1, len(index.Entries))
	assert.Equal(t, testEntry.Key, index.Entries[0].Key)
	assert.Equal(t, testEntry.Path, index.Entries[0].Path)
	assert.Equal(t, testEntry.Size, index.Entries[0].Size)
	assert.Equal(t, testEntry.Hash, index.Entries[0].Hash)
	assert.Equal(t, "test-value", index.Entries[0].Metadata["test-key"])

	// Test updating an existing entry
	updatedEntry := testEntry
	updatedEntry.Size = 200 // Change the size
	updatedEntry.Metadata["test-key"] = "updated-value"

	err = addToIndex(tempDir, updatedEntry, true)
	require.NoError(t, err)

	// Verify the entry was updated
	indexData, err = os.ReadFile(indexPath)
	require.NoError(t, err)

	require.NoError(t, json.Unmarshal(indexData, &index))
	require.Equal(t, 1, len(index.Entries)) // Still just one entry
	assert.Equal(t, int64(200), index.Entries[0].Size)
	assert.Equal(t, "updated-value", index.Entries[0].Metadata["test-key"])
	assert.Equal(t, int64(200), index.TotalSize) // Total size should be updated
}

// TestRegisterCacheFileNoCacheParent tests registering a file in a directory
// that doesn't have a parent 'cache' directory
func TestRegisterCacheFileNoCacheParent(t *testing.T) {
	// Create a temporary directory
	tempDir := createTempDir(t)
	defer os.RemoveAll(tempDir)

	// Create a non-cache subdirectory
	subdirPath := filepath.Join(tempDir, "not-cache-dir")
	require.NoError(t, os.MkdirAll(subdirPath, os.ModePerm))

	// Create a test file
	testFilePath := filepath.Join(subdirPath, "test-file.txt")
	testContent := "Test content"
	require.NoError(t, os.WriteFile(testFilePath, []byte(testContent), 0644))

	// Test registering the file
	testURL := "https://example.com/test-file.txt"
	err := RegisterCacheFile(testFilePath, testURL, true)
	require.NoError(t, err)

	// Check if the index file was created only in the subdirectory
	subdirIndexPath := filepath.Join(subdirPath, "index.json")
	_, err = os.Stat(subdirIndexPath)
	require.NoError(t, err)

	// Verify no index file was created in the parent directory
	parentIndexPath := filepath.Join(tempDir, "index.json")
	_, err = os.Stat(parentIndexPath)
	assert.True(t, os.IsNotExist(err))
}

// TestNoOutput ensures that when isQuiet=true, there's no output
func TestNoOutput(t *testing.T) {
	// This test is more for coverage than actual testing since it's hard
	// to capture color.Output in a test. Just verify the functions don't
	// panic when isQuiet is true.

	// Create a temporary directory
	tempDir := createTempDir(t)
	defer os.RemoveAll(tempDir)

	// Create a test file
	testFilePath := filepath.Join(tempDir, "test-file.txt")
	require.NoError(t, os.WriteFile(testFilePath, []byte("Test content"), 0644))

	// Test registering the file with isQuiet=true
	err := RegisterCacheFile(testFilePath, "https://example.com/test-file.txt", true)
	require.NoError(t, err)

	// Test with a non-existent file with isQuiet=true
	_ = RegisterCacheFile(filepath.Join(tempDir, "non-existent-file.txt"), "https://example.com/non-existent-file.txt", true)
}

// TestURLDeduplication tests that entries with the same URL are deduplicated
func TestURLDeduplication(t *testing.T) {
	// Create a temporary directory
	tempDir := createTempDir(t)
	defer os.RemoveAll(tempDir)

	// Create cache subdirectory
	cacheDir := filepath.Join(tempDir, "cache")
	require.NoError(t, os.MkdirAll(cacheDir, os.ModePerm))

	// Create two files with different names
	file1 := filepath.Join(cacheDir, "file1.tar.gz")
	file2 := filepath.Join(cacheDir, "file2.tar.gz")
	content1 := "content for file 1"
	content2 := "content for file 2 is different and longer"
	require.NoError(t, os.WriteFile(file1, []byte(content1), 0644))
	require.NoError(t, os.WriteFile(file2, []byte(content2), 0644))

	// Use the same URL for both files
	sameURL := "https://example.com/archive.tar.gz"

	// Register the first file
	err := RegisterCacheFile(file1, sameURL, true)
	require.NoError(t, err)

	// Register the second file with the same URL
	err = RegisterCacheFile(file2, sameURL, true)
	require.NoError(t, err)

	// Read the index to verify deduplication
	indexPath := filepath.Join(cacheDir, "index.json")
	indexData, err := os.ReadFile(indexPath)
	require.NoError(t, err)

	var index CacheIndex
	require.NoError(t, json.Unmarshal(indexData, &index))

	// Should only have one entry despite registering two files with the same URL
	assert.Equal(t, 1, len(index.Entries), "Should have only one entry after deduplication")

	// The entry should have the most recent file's data
	entry := index.Entries[0]
	assert.Equal(t, file2, entry.Path, "Entry should have the path of the second file")
	assert.Equal(t, int64(len(content2)), entry.Size, "Entry should have the size of the second file")
	assert.Equal(t, sameURL, entry.Metadata["url"], "Entry should maintain the URL")

	// The total size should match the second file's size
	assert.Equal(t, int64(len(content2)), index.TotalSize, "Total size should match the second file's size")
}
