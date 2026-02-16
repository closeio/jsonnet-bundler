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
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jsonnet-bundler/jsonnet-bundler/spec/v1/deps"
)

// TestDownloadValidation tests that invalid/incomplete downloads are properly detected
func TestDownloadValidation(t *testing.T) {
	tests := []struct {
		name          string
		content       []byte
		expectedError string
	}{
		{
			name:          "Empty file",
			content:       []byte{},
			expectedError: "invalid gzip header: EOF",
		},
		{
			name:          "Invalid gzip header",
			content:       []byte("not a gzip file"),
			expectedError: "invalid gzip header: gzip: invalid header",
		},
		{
			name:          "Truncated gzip",
			content:       []byte{0x1f, 0x8b, 0x08}, // Partial gzip magic header
			expectedError: "invalid gzip header: unexpected EOF",
		},
		{
			name:          "Valid gzip with corrupted tar content",
			content:       createCorruptedGzipTar(t),
			expectedError: "corrupted tar entry at position 0",
		},
	}

	// Save original quiet value
	oldQuiet := GitQuiet
	GitQuiet = true
	defer func() { GitQuiet = oldQuiet }()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test directory
			tempDir, err := os.MkdirTemp("", "download-validation-*")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(tempDir)

			// Create test server that returns the test content
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write(tt.content)
			}))
			defer server.Close()

			// Try to download
			filepath := filepath.Join(tempDir, "test.tar.gz")
			err = downloadGitHubArchive(filepath, server.URL)

			// Should get an error
			if err == nil {
				t.Errorf("Expected error for %s, got nil", tt.name)
			} else if !contains(err.Error(), tt.expectedError) {
				t.Errorf("Expected error containing '%s', got '%s'", tt.expectedError, err.Error())
			}

			// File should not exist
			if _, err := os.Stat(filepath); !os.IsNotExist(err) {
				t.Errorf("File should not exist after failed download")
			}
		})
	}
}

// TestSuccessfulDownloadValidation tests that valid gzip files are accepted
func TestSuccessfulDownloadValidation(t *testing.T) {
	// Save original quiet value
	oldQuiet := GitQuiet
	GitQuiet = true
	defer func() { GitQuiet = oldQuiet }()

	// Create valid gzip content
	validContent, err := createValidGzipTar("test content")
	if err != nil {
		t.Fatal(err)
	}

	// Create test directory
	tempDir, err := os.MkdirTemp("", "download-success-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	// Create test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(validContent)
	}))
	defer server.Close()

	// Download should succeed
	filepath := filepath.Join(tempDir, "test.tar.gz")
	err = downloadGitHubArchive(filepath, server.URL)
	if err != nil {
		t.Errorf("Expected no error for valid gzip, got: %v", err)
	}

	// File should exist and contain the valid content
	data, err := os.ReadFile(filepath)
	if err != nil {
		t.Fatal(err)
	}

	if len(data) != len(validContent) {
		t.Errorf("Downloaded file size mismatch: got %d, want %d", len(data), len(validContent))
	}
}

// createCorruptedGzipTar creates a gzip file with corrupted tar content
func createCorruptedGzipTar(t *testing.T) []byte {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	// Write some garbage that's not valid tar format
	gw.Write([]byte("this is not a valid tar format"))
	gw.Close()
	return buf.Bytes()
}

// contains checks if string s contains substring
func contains(s, substr string) bool {
	return bytes.Contains([]byte(s), []byte(substr))
}

// parseGitUrl parses a Git URL and returns a Git struct
func parseGitUrl(url string) *deps.Git {
	// For GitHub URLs, extract the parts
	if strings.HasPrefix(url, "https://github.com/") {
		parts := strings.Split(strings.TrimPrefix(url, "https://github.com/"), "/")
		if len(parts) >= 2 {
			return &deps.Git{
				Scheme: "https://",
				Host:   "github.com",
				User:   parts[0],
				Repo:   parts[1],
				Subdir: "",
			}
		}
	}
	return nil
}

// TestParallelGitHubArchiveDownloads tests concurrent downloads of GitHub archives
func TestParallelGitHubArchiveDownloads(t *testing.T) {
	// Skip if not connected to internet
	if os.Getenv("SKIP_NETWORK_TESTS") == "true" {
		t.Skip("Skipping network test")
	}

	// Create a temporary vendor directory
	vendorDir, err := os.MkdirTemp("", "parallel-test-vendor-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(vendorDir)

	// Test with multiple packages that might use the same commit SHA
	// Using a smaller repo for faster testing
	packages := []struct {
		name    string
		remote  string
		version string
	}{
		{
			name:    "grafana/jsonnet-libs-1",
			remote:  "https://github.com/grafana/jsonnet-libs",
			version: "a7aa385b9fb7da06657b09e776e915572fe25445", // specific commit
		},
		{
			name:    "grafana/jsonnet-libs-2",
			remote:  "https://github.com/grafana/jsonnet-libs",
			version: "a7aa385b9fb7da06657b09e776e915572fe25445", // same commit
		},
		{
			name:    "grafana/jsonnet-libs-3",
			remote:  "https://github.com/grafana/jsonnet-libs",
			version: "a7aa385b9fb7da06657b09e776e915572fe25445", // same commit
		},
	}

	// Enable debug output for this test
	oldGitQuiet := GitQuiet
	GitQuiet = false
	defer func() { GitQuiet = oldGitQuiet }()

	// Run downloads in parallel
	var wg sync.WaitGroup
	errors := make(chan error, len(packages))

	for _, pkg := range packages {
		wg.Add(1)
		go func(p struct {
			name    string
			remote  string
			version string
		}) {
			defer wg.Done()

			// Parse the remote URL to get the Git struct
			parsed := parseGitUrl(p.remote)
			if parsed == nil {
				errors <- fmt.Errorf("failed to parse git URL: %s", p.remote)
				return
			}

			gitPkg := NewGitPackage(parsed)

			ctx := context.Background()
			_, err := gitPkg.Install(ctx, p.name, vendorDir, p.version)
			if err != nil {
				errors <- fmt.Errorf("failed to install %s: %w", p.name, err)
			}
		}(pkg)
	}

	// Wait for all downloads to complete
	wg.Wait()
	close(errors)

	// Check for errors
	for err := range errors {
		t.Error(err)
	}

	// Verify all packages were installed
	for _, pkg := range packages {
		destPath := filepath.Join(vendorDir, pkg.name)
		if _, err := os.Stat(destPath); os.IsNotExist(err) {
			t.Errorf("Package %s was not installed", pkg.name)
		}
	}
}

// TestParallelCacheOperations tests concurrent cache operations
func TestParallelCacheOperations(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "cache-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	// Test URLs that would generate the same cache key
	urls := []string{
		"https://github.com/owner/repo/archive/a7aa385b9fb7da06657b09e776e915572fe25445.tar.gz",
		"https://github.com/owner/repo/archive/a7aa385b9fb7da06657b09e776e915572fe25445.tar.gz",
		"https://github.com/owner/repo/archive/a7aa385b9fb7da06657b09e776e915572fe25445.tar.gz",
	}

	// Test concurrent cache key generation and mutex acquisition
	var wg sync.WaitGroup
	results := make(chan string, len(urls))

	for i, url := range urls {
		wg.Add(1)
		go func(idx int, u string) {
			defer wg.Done()

			// Get cache key
			cacheKey := getCacheKeyForURL(u)

			// Get mutex for this cache key
			mutex := getArchiveMutex(cacheKey)

			// Try to lock (this should serialize access)
			mutex.Lock()
			results <- fmt.Sprintf("goroutine %d acquired lock for key %s", idx, cacheKey)
			mutex.Unlock()
		}(i, url)
	}

	wg.Wait()
	close(results)

	// Verify all goroutines completed
	count := 0
	for result := range results {
		t.Log(result)
		count++
	}

	if count != len(urls) {
		t.Errorf("Expected %d results, got %d", len(urls), count)
	}
}
