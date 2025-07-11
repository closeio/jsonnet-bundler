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
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jsonnet-bundler/jsonnet-bundler/pkg/cache"
)

func TestRemoteCacheCommands(t *testing.T) {
	// Create a temporary directory for tests
	tmpDir, err := os.MkdirTemp("", "jb-test-cache-")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Mock the home directory to create a controlled global cache location
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	// Global cache directory should be under the mock HOME/.cache/jb
	globalCacheDir := filepath.Join(tmpDir, ".cache", "jb")
	err = os.MkdirAll(globalCacheDir, os.ModePerm)
	require.NoError(t, err)

	// Create a global cache location
	globalLocation := cache.CacheLocation{
		Path: globalCacheDir,
	}

	// Create test index with some remote caches
	testRemotes := []string{
		"s3://cache1.example.com",
		"s3://cache2.example.com",
		"s3://cache3.example.com",
	}

	// Create and save index with test remotes for global cache
	testIndex := &cache.CacheIndex{
		Version:      1,
		RemoteCaches: testRemotes,
	}
	err = cache.SaveIndex(globalLocation, testIndex)
	require.NoError(t, err)

	// Verify test setup worked
	verifyIndex, err := cache.LoadOrCreateIndex(globalLocation)
	require.NoError(t, err)
	require.Equal(t, testRemotes, verifyIndex.RemoteCaches, "Test setup failed: cache not initialized correctly")

	// Enable the global cache for testing
	GlobalCacheEnabled = true

	// Test list-remote command
	t.Run("ListRemote", func(t *testing.T) {
		// Run server list command with JSON output
		result := cacheServerListCommand(tmpDir, "vendor", true)
		assert.Equal(t, 0, result, "Command should succeed")

		// Verify that global cache still contains our test remotes
		index, err := cache.LoadOrCreateIndex(globalLocation)
		require.NoError(t, err)
		assert.Equal(t, testRemotes, index.RemoteCaches, "Remotes should match original list")
	})

	// Test remove-remote command
	t.Run("RemoveRemote", func(t *testing.T) {
		// Run server remove command to remove the second cache
		result := cacheServerRemoveCommand(tmpDir, "vendor", "s3://cache2.example.com")
		assert.Equal(t, 0, result, "Command should succeed")

		// Verify that the remote was removed from global cache
		index, err := cache.LoadOrCreateIndex(globalLocation)
		require.NoError(t, err)

		// We should now have only cache1 and cache3
		expectedRemaining := []string{
			"s3://cache1.example.com",
			"s3://cache3.example.com",
		}
		assert.Equal(t, expectedRemaining, index.RemoteCaches, "Remote cache2 should be removed")
	})

	// Test removing non-existent remote
	t.Run("RemoveNonExistentRemote", func(t *testing.T) {
		// Run server remove command with a non-existent URL
		result := cacheServerRemoveCommand(tmpDir, "vendor", "s3://nonexistent.example.com")
		assert.Equal(t, 0, result, "Command should succeed even on non-existent remote")

		// Verify that the remotes are unchanged
		index, err := cache.LoadOrCreateIndex(globalLocation)
		require.NoError(t, err)

		expectedRemaining := []string{
			"s3://cache1.example.com",
			"s3://cache3.example.com",
		}
		assert.Equal(t, expectedRemaining, index.RemoteCaches, "Remotes should be unchanged")
	})

	// Test add-remote followed by list-remote
	t.Run("AddRemoteAndList", func(t *testing.T) {
		// Add a new remote
		result := cacheServerAddCommand(tmpDir, "vendor", "s3://cache4.example.com")
		assert.Equal(t, 0, result, "Add command should succeed")

		// Verify that remote was added to global cache
		index, err := cache.LoadOrCreateIndex(globalLocation)
		require.NoError(t, err)

		expectedRemotes := []string{
			"s3://cache1.example.com",
			"s3://cache3.example.com",
			"s3://cache4.example.com",
		}
		assert.Equal(t, expectedRemotes, index.RemoteCaches, "New remote should be added")

		// Run server list command with JSON output
		capturedOutput := captureTempStdout(t, func() {
			result = cacheServerListCommand(tmpDir, "vendor", true)
		})
		assert.Equal(t, 0, result, "List command should succeed")

		// Parse the JSON output
		var outputList []string
		err = json.Unmarshal([]byte(capturedOutput), &outputList)
		require.NoError(t, err, "Should produce valid JSON")

		// Verify the output matches our expected remotes
		assert.Equal(t, expectedRemotes, outputList, "Listed remotes should match expected list")
	})
}

// Helper function to capture stdout for testing
func captureTempStdout(t *testing.T, fn func()) string {
	// Redirect stdout to a pipe
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	// Run the function that outputs to stdout
	fn()

	// Restore stdout and capture the output
	w.Close()
	os.Stdout = oldStdout

	out, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(out)
}
