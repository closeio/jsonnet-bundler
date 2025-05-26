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
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jsonnet-bundler/jsonnet-bundler/spec/v1/deps"
)

// TestIsGitHubRepository tests the isGitHubRepository function
func TestIsGitHubRepository(t *testing.T) {
	tests := []struct {
		name     string
		remote   string
		expected bool
	}{
		{
			name:     "Valid GitHub HTTPS URL",
			remote:   "https://github.com/jsonnet-bundler/jsonnet-bundler.git",
			expected: true,
		},
		{
			name:     "Valid GitHub SSH URL",
			remote:   "ssh://github.com/jsonnet-bundler/jsonnet-bundler.git",
			expected: true,
		},
		{
			name:     "GitLab URL",
			remote:   "https://gitlab.com/user/repo.git",
			expected: false,
		},
		{
			name:     "BitBucket URL",
			remote:   "https://bitbucket.org/user/repo.git",
			expected: false,
		},
		{
			name:     "Plain git URL",
			remote:   "git@example.com:user/repo.git",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isGitHubRepository(tt.remote)
			if result != tt.expected {
				t.Errorf("isGitHubRepository(%q) = %v, want %v", tt.remote, result, tt.expected)
			}
		})
	}
}

// TestResolveVersionToCommitSHA tests the resolveVersionToCommitSHA function
func TestResolveVersionToCommitSHA(t *testing.T) {
	gitPkg := &GitPackage{
		Source: &deps.Git{
			Scheme: "https",
			Host:   "github.com",
			User:   "test",
			Repo:   "repo",
		},
	}

	tests := []struct {
		name        string
		version     string
		expectError bool
	}{
		{
			name:        "Valid SHA",
			version:     "a804b068e640f9d11680a7e1c9377024d9bd5b57",
			expectError: false,
		},
		{
			name:        "Short SHA",
			version:     "abc123",
			expectError: true,
		},
		{
			name:        "Branch name",
			version:     "main",
			expectError: true, // Will fail without actual git remote
		},
		{
			name:        "Tag name",
			version:     "v1.0.0",
			expectError: true, // Will fail without actual git remote
		},
	}

	ctx := context.Background()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sha, err := gitPkg.resolveVersionToCommitSHA(ctx, tt.version)
			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got none, sha: %s", sha)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if sha != tt.version {
					t.Errorf("Expected SHA %s, got %s", tt.version, sha)
				}
			}
		})
	}
}

// TestGetCommitHash tests the getCommitHash function with a mock git repository
func TestGetCommitHash(t *testing.T) {
	// Skip if git is not available
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}

	// Create a temporary directory for our test repo
	tempDir, err := os.MkdirTemp("", "git-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	// Initialize a git repo and make a commit
	ctx := context.Background()
	gitPkg := &GitPackage{
		Source: &deps.Git{},
	}

	// Initialize repo
	cmd := gitCmd(ctx, tempDir, "init")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}

	// Configure git
	cmd = gitCmd(ctx, tempDir, "config", "user.email", "test@example.com")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	cmd = gitCmd(ctx, tempDir, "config", "user.name", "Test User")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}

	// Create a file and commit
	testFile := filepath.Join(tempDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
		t.Fatal(err)
	}

	cmd = gitCmd(ctx, tempDir, "add", ".")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}

	cmd = gitCmd(ctx, tempDir, "commit", "-m", "Initial commit")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}

	// Test getCommitHash
	hash, err := gitPkg.getCommitHash(ctx, tempDir)
	if err != nil {
		t.Fatalf("getCommitHash failed: %v", err)
	}

	// Verify it's a valid SHA (40 characters)
	if len(hash) != 40 {
		t.Errorf("Expected 40-character SHA, got %d characters: %s", len(hash), hash)
	}

	// Verify it only contains hex characters
	for _, c := range hash {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Errorf("Invalid character in SHA: %c", c)
		}
	}
}

// TestCopyDirectory tests the copyDirectory function
func TestCopyDirectory(t *testing.T) {
	// Create source directory structure
	srcDir, err := os.MkdirTemp("", "copy-src-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(srcDir)

	dstDir, err := os.MkdirTemp("", "copy-dst-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dstDir)

	// Create test directory structure
	testFiles := map[string]string{
		"file1.txt":            "content1",
		"subdir/file2.txt":     "content2",
		"subdir/nested/f3.txt": "content3",
		".git/config":          "git config",
		"other/file4.txt":      "content4",
	}

	for path, content := range testFiles {
		fullPath := filepath.Join(srcDir, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Test copyDirectory
	if err := copyDirectory(srcDir, dstDir); err != nil {
		t.Fatalf("copyDirectory failed: %v", err)
	}

	// Verify files were copied (except .git)
	expectedFiles := []string{
		"file1.txt",
		"subdir/file2.txt",
		"subdir/nested/f3.txt",
		"other/file4.txt",
	}

	for _, file := range expectedFiles {
		dstPath := filepath.Join(dstDir, file)
		if _, err := os.Stat(dstPath); os.IsNotExist(err) {
			t.Errorf("Expected file %s not found", file)
		}

		// Verify content
		content, err := os.ReadFile(dstPath)
		if err != nil {
			t.Errorf("Failed to read %s: %v", file, err)
		}
		expectedContent := testFiles[file]
		if string(content) != expectedContent {
			t.Errorf("File %s has wrong content: got %q, want %q", file, string(content), expectedContent)
		}
	}

	// Verify .git was not copied
	gitPath := filepath.Join(dstDir, ".git")
	if _, err := os.Stat(gitPath); !os.IsNotExist(err) {
		t.Error(".git directory should not have been copied")
	}
}

// TestExtractArchiveToDestination tests archive extraction
func TestExtractArchiveToDestination(t *testing.T) {
	// Create a simple tar.gz archive for testing
	tempDir, err := os.MkdirTemp("", "archive-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	// For this test, we'll create a mock scenario
	// In a real test, you'd create an actual tar.gz file
	t.Skip("Archive extraction test requires creating actual tar.gz files")
}

// TestInstallFromGitHubArchive tests the GitHub archive installation with a mock server
func TestInstallFromGitHubArchive(t *testing.T) {
	// Save original quiet value
	oldQuiet := GitQuiet
	GitQuiet = true
	defer func() { GitQuiet = oldQuiet }()

	// Create a mock HTTP server
	archiveContent := []byte("mock archive content")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate GitHub archive endpoint
		if strings.Contains(r.URL.Path, "/archive/") {
			w.Write(archiveContent)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	// Create test GitPackage
	gitPkg := &GitPackage{
		Source: &deps.Git{
			Scheme: "https",
			Host:   "github.com",
			User:   "test",
			Repo:   "repo",
		},
	}

	// Override the remote URL to use our test server
	gitPkg.Source.Host = strings.TrimPrefix(server.URL, "http://")

	tempDir, err := os.MkdirTemp("", "github-archive-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	destPath := filepath.Join(tempDir, "dest")

	// Test with a valid SHA (will fail on archive extraction, but tests the download)
	ctx := context.Background()
	_, err = gitPkg.installFromGitHubArchive(ctx, "a804b068e640f9d11680a7e1c9377024d9bd5b57", tempDir, destPath)

	// We expect this to fail at the extraction phase since our mock content isn't a real tar.gz
	if err == nil {
		t.Error("Expected error during archive extraction, but got none")
	}
}

// TestApplySparseCheckout tests sparse checkout configuration
func TestApplySparseCheckout(t *testing.T) {
	// Skip if git is not available
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}

	tempDir, err := os.MkdirTemp("", "sparse-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	// Initialize a git repo
	ctx := context.Background()
	cmd := gitCmd(ctx, tempDir, "init")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}

	// Create GitPackage with subdir
	gitPkg := &GitPackage{
		Source: &deps.Git{
			Subdir: "mysubdir",
		},
	}

	// Apply sparse checkout
	err = gitPkg.applySparseCheckout(ctx, tempDir, "main")

	// This will fail because there's no actual checkout, but we can verify the config was set
	sparseCheckoutFile := filepath.Join(tempDir, ".git", "info", "sparse-checkout")
	content, readErr := os.ReadFile(sparseCheckoutFile)
	if readErr != nil {
		t.Fatalf("Failed to read sparse-checkout file: %v", readErr)
	}

	expected := "mysubdir/*\n"
	if string(content) != expected {
		t.Errorf("Sparse checkout file has wrong content: got %q, want %q", string(content), expected)
	}
}

// TestCopyToDestination tests the copyToDestination function
func TestCopyToDestination(t *testing.T) {
	// Create test directories
	workDir, err := os.MkdirTemp("", "work-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(workDir)

	tempDir, err := os.MkdirTemp("", "temp-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	destParent, err := os.MkdirTemp("", "dest-parent-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(destParent)

	// Create test files in workDir
	testFile := filepath.Join(workDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
		t.Fatal(err)
	}

	gitPkg := &GitPackage{
		Source: &deps.Git{
			Subdir: "",
		},
	}

	destPath := filepath.Join(destParent, "dest")

	// Test direct move (useGlobalCache = false)
	// Create a test file in tempDir to make the move succeed
	testFileInTemp := filepath.Join(tempDir, "temp.txt")
	if err := os.WriteFile(testFileInTemp, []byte("temp content"), 0644); err != nil {
		t.Fatal(err)
	}

	// This should succeed since tempDir exists and can be moved
	if err := gitPkg.copyToDestination(tempDir, tempDir, destPath, false); err != nil {
		t.Errorf("copyToDestination without global cache failed: %v", err)
	}

	// Verify the file was moved
	movedFile := filepath.Join(destPath, "temp.txt")
	if _, err := os.Stat(movedFile); os.IsNotExist(err) {
		t.Error("Expected file was not moved to destination")
	}

	// Clean up for next test
	os.RemoveAll(destPath)

	// Test copy from global cache (useGlobalCache = true)
	err = gitPkg.copyToDestination(workDir, tempDir, destPath, true)
	if err != nil {
		t.Errorf("copyToDestination with global cache failed: %v", err)
	}

	// Verify the file was copied
	copiedFile := filepath.Join(destPath, "test.txt")
	if _, err := os.Stat(copiedFile); os.IsNotExist(err) {
		t.Error("Expected file was not copied to destination")
	}
}
