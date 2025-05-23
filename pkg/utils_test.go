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
	"os"
	"path/filepath"
	"testing"
)

// TestCopyFile tests the file copying functionality
func TestCopyFile(t *testing.T) {
	// Create a test directory
	tempDir, err := os.MkdirTemp("", "copyfile_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a source file
	srcContent := "test content"
	srcPath := filepath.Join(tempDir, "source.txt")
	if err := os.WriteFile(srcPath, []byte(srcContent), 0644); err != nil {
		t.Fatalf("Failed to create source file: %v", err)
	}

	// Test CopyFile to a destination
	dstPath := filepath.Join(tempDir, "dest.txt")
	err = CopyFile(srcPath, dstPath)
	if err != nil {
		t.Fatalf("CopyFile failed: %v", err)
	}

	// Check that the destination file exists and has the same content
	dstContent, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("Failed to read destination file: %v", err)
	}
	if string(dstContent) != srcContent {
		t.Errorf("Destination content doesn't match source. Expected: %q, Got: %q", srcContent, string(dstContent))
	}

	// Test failure case - try to copy from a non-existent source
	nonExistentPath := filepath.Join(tempDir, "nonexistent.txt")
	err = CopyFile(nonExistentPath, dstPath)
	if err == nil {
		t.Errorf("Expected error when copying non-existent source, got nil")
	}
}
