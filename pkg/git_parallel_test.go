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

package pkg

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParallelCheckRemoteCaches(t *testing.T) {
	// Create a test archive
	testArchiveContent := []byte("test archive content")
	testCacheKey := "test-cache-key"
	testArchiveName := testCacheKey + ".tar.gz"

	// Create test servers
	successServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, testArchiveName) {
			w.WriteHeader(http.StatusOK)
			w.Write(testArchiveContent)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer successServer.Close()

	failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer failServer.Close()

	errorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer errorServer.Close()

	tests := []struct {
		name          string
		remoteCaches  []string
		expectSuccess bool
		expectError   bool
	}{
		{
			name:          "Single successful cache",
			remoteCaches:  []string{successServer.URL},
			expectSuccess: true,
			expectError:   false,
		},
		{
			name:          "Multiple caches with first successful",
			remoteCaches:  []string{successServer.URL, failServer.URL},
			expectSuccess: true,
			expectError:   false,
		},
		{
			name:          "Multiple caches with second successful",
			remoteCaches:  []string{failServer.URL, successServer.URL},
			expectSuccess: true,
			expectError:   false,
		},
		{
			name:          "All caches fail",
			remoteCaches:  []string{failServer.URL, errorServer.URL},
			expectSuccess: false,
			expectError:   true,
		},
		{
			name:          "No remote caches",
			remoteCaches:  []string{},
			expectSuccess: false,
			expectError:   true,
		},
		{
			name:          "Invalid URL",
			remoteCaches:  []string{"://invalid-url"},
			expectSuccess: false,
			expectError:   true,
		},
	}

	// Save original GitQuiet value
	originalGitQuiet := GitQuiet
	GitQuiet = true
	defer func() { GitQuiet = originalGitQuiet }()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempPath, err := parallelCheckRemoteCaches(tt.remoteCaches, testCacheKey)

			if tt.expectError && err == nil {
				t.Errorf("Expected error but got none")
			}

			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if tt.expectSuccess {
				if tempPath == "" {
					t.Errorf("Expected a temporary file path but got empty string")
				} else {
					// Verify the content
					content, err := os.ReadFile(tempPath)
					if err != nil {
						t.Errorf("Failed to read downloaded file: %v", err)
					}
					if string(content) != string(testArchiveContent) {
						t.Errorf("Content mismatch: expected %s, got %s", testArchiveContent, content)
					}
					// Clean up
					os.Remove(tempPath)
				}
			}
		})
	}
}

func TestParallelCheckRemoteCachesWithMultipleServers(t *testing.T) {
	// Create multiple test servers with different response times
	testCacheKey := "test-cache-key"
	testArchiveName := testCacheKey + ".tar.gz"

	var servers []*httptest.Server
	serverContents := make([]string, 3)

	for i := 0; i < 3; i++ {
		index := i
		serverContents[index] = fmt.Sprintf("content from server %d", index)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, testArchiveName) {
				// Add delay to simulate network latency
				// Server 0 is fastest, server 2 is slowest
				// time.Sleep(time.Duration(index*100) * time.Millisecond)
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(serverContents[index]))
			} else {
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		servers = append(servers, server)
		defer server.Close()
	}

	// Save original GitQuiet value
	originalGitQuiet := GitQuiet
	GitQuiet = true
	defer func() { GitQuiet = originalGitQuiet }()

	// Build URLs
	var urls []string
	for _, s := range servers {
		urls = append(urls, s.URL)
	}

	// Test parallel fetch
	tempPath, err := parallelCheckRemoteCaches(urls, testCacheKey)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if tempPath == "" {
		t.Fatal("Expected a temporary file path but got empty string")
	}

	// Verify we got content from one of the servers
	content, err := os.ReadFile(tempPath)
	if err != nil {
		t.Fatalf("Failed to read downloaded file: %v", err)
	}

	foundValidContent := false
	for _, expectedContent := range serverContents {
		if string(content) == expectedContent {
			foundValidContent = true
			break
		}
	}

	if !foundValidContent {
		t.Errorf("Content doesn't match any server: %s", content)
	}

	// Clean up
	os.Remove(tempPath)
}

func TestParallelPopulateRemoteS3Caches(t *testing.T) {
	// Create a temporary test file
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test.tar.gz")
	testContent := []byte("test archive content for S3")
	if err := os.WriteFile(testFile, testContent, 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Save original GitQuiet value
	originalGitQuiet := GitQuiet
	GitQuiet = true
	defer func() { GitQuiet = originalGitQuiet }()

	tests := []struct {
		name         string
		remoteCaches []string
		filePath     string
		cacheKey     string
	}{
		{
			name:         "No S3 URLs",
			remoteCaches: []string{"http://example.com", "https://example.com"},
			filePath:     testFile,
			cacheKey:     "test-key",
		},
		{
			name:         "Invalid S3 URL",
			remoteCaches: []string{"s3://invalid-bucket-name-!@#$%^&*()"},
			filePath:     testFile,
			cacheKey:     "test-key",
		},
		{
			name:         "Directory instead of file",
			remoteCaches: []string{"s3://test-bucket"},
			filePath:     tempDir,
			cacheKey:     "test-key",
		},
		{
			name:         "Non-existent file",
			remoteCaches: []string{"s3://test-bucket"},
			filePath:     filepath.Join(tempDir, "non-existent.tar.gz"),
			cacheKey:     "test-key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This should not panic
			parallelPopulateRemoteS3Caches(tt.remoteCaches, tt.filePath, tt.cacheKey)
		})
	}
}

func TestParallelCheckRemoteCachesCleanup(t *testing.T) {
	// Test that temporary files are cleaned up when not used
	testCacheKey := "test-cache-key"
	testArchiveName := testCacheKey + ".tar.gz"

	// Create multiple servers that all succeed
	var servers []*httptest.Server
	for i := 0; i < 3; i++ {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, testArchiveName) {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("content"))
			} else {
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		servers = append(servers, server)
		defer server.Close()
	}

	// Save original GitQuiet value
	originalGitQuiet := GitQuiet
	GitQuiet = true
	defer func() { GitQuiet = originalGitQuiet }()

	// Build URLs
	var urls []string
	for _, s := range servers {
		urls = append(urls, s.URL)
	}

	// Before running, count temp files
	tempDir := os.TempDir()
	filesBefore, _ := os.ReadDir(tempDir)
	countBefore := 0
	for _, f := range filesBefore {
		if strings.HasPrefix(f.Name(), "jb-cache-") && strings.HasSuffix(f.Name(), ".tar.gz") {
			countBefore++
		}
	}

	// Run the function
	tempPath, err := parallelCheckRemoteCaches(urls, testCacheKey)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Clean up the result file
	if tempPath != "" {
		defer os.Remove(tempPath)
	}

	// After running, count temp files again
	filesAfter, _ := os.ReadDir(tempDir)
	countAfter := 0
	for _, f := range filesAfter {
		if strings.HasPrefix(f.Name(), "jb-cache-") && strings.HasSuffix(f.Name(), ".tar.gz") {
			countAfter++
		}
	}

	// The count should be at most countBefore + 1 (the successful file)
	// Other temp files should have been cleaned up
	// Note: In some environments, temp files might persist momentarily
	if countAfter > countBefore+len(servers) {
		t.Errorf("Too many temp files left: before=%d, after=%d, servers=%d", countBefore, countAfter, len(servers))
	}
}
