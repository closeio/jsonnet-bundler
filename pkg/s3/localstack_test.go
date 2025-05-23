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

//go:build integration

package s3

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

const (
	testBucket     = "test-bucket"
	localstackHost = "http://127.0.0.1:4566"
)

// setupLocalStackClient creates a client for testing with LocalStack
func setupLocalStackClient(t *testing.T) *Client {
	t.Helper()

	// Skip tests if LocalStack is not available
	if os.Getenv("AWS_ENDPOINT") == "" {
		t.Skip("Skipping test because LocalStack endpoint not found in environment")
	}

	// Create client
	client, err := NewClientFromEnv(testBucket)
	if err != nil {
		t.Fatalf("Failed to create S3 client: %v", err)
	}

	// Create bucket if it doesn't exist
	ctx := context.Background()
	exists, err := client.BucketExists(ctx)
	if err != nil {
		t.Fatalf("Failed to check bucket existence: %v", err)
	}

	if !exists {
		// Create the bucket using AWS CLI because the AWS SDK doesn't have a simple
		// CreateBucket method in the S3 client
		cmd := "aws --endpoint-url=" + localstackHost + " s3 mb s3://" + testBucket
		_, err := exec.Command("sh", "-c", cmd).Output()
		if err != nil {
			t.Fatalf("Failed to create test bucket: %v", err)
		}

		// Give a moment for the bucket to be available
		time.Sleep(1 * time.Second)
	}

	return client
}

// cleanupTestFiles removes the test files and directories
func cleanupTestFiles(t *testing.T, paths ...string) {
	t.Helper()
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			err := os.RemoveAll(path)
			if err != nil {
				t.Logf("Warning: failed to clean up %s: %v", path, err)
			}
		}
	}
}

// TestS3Upload tests uploading a file to S3
func TestS3Upload(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	client := setupLocalStackClient(t)
	ctx := context.Background()

	// Create a temp file
	tempDir, err := os.MkdirTemp("", "s3-upload-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer cleanupTestFiles(t, tempDir)

	testFilePath := filepath.Join(tempDir, "test-file.txt")
	testContent := []byte("This is a test file for S3 upload")
	if err := os.WriteFile(testFilePath, testContent, 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Upload to S3
	testKey := "test-uploads/test-file.txt"
	if err := client.Upload(ctx, testFilePath, testKey); err != nil {
		t.Fatalf("Failed to upload file: %v", err)
	}

	// Verify the upload by fetching it back
	fetchedContent, err := client.GetObject(ctx, testKey)
	if err != nil {
		t.Fatalf("Failed to fetch uploaded object: %v", err)
	}

	if !bytes.Equal(fetchedContent, testContent) {
		t.Errorf("Fetched content doesn't match original: got %s, want %s", string(fetchedContent), string(testContent))
	}
}

// TestS3Download tests downloading a file from S3
func TestS3Download(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	client := setupLocalStackClient(t)
	ctx := context.Background()

	// Create a test object in S3
	testKey := "test-downloads/test-file.txt"
	testContent := []byte("This is a test file for S3 download")

	if err := client.PutObject(ctx, testKey, testContent); err != nil {
		t.Fatalf("Failed to put test object: %v", err)
	}

	// Create a temp directory for download
	tempDir, err := os.MkdirTemp("", "s3-download-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer cleanupTestFiles(t, tempDir)

	// Download from S3
	downloadPath := filepath.Join(tempDir, "downloaded-file.txt")
	if err := client.Download(ctx, testKey, downloadPath); err != nil {
		t.Fatalf("Failed to download file: %v", err)
	}

	// Verify the download
	downloadedContent, err := os.ReadFile(downloadPath)
	if err != nil {
		t.Fatalf("Failed to read downloaded file: %v", err)
	}

	if !bytes.Equal(downloadedContent, testContent) {
		t.Errorf("Downloaded content doesn't match original: got %s, want %s", string(downloadedContent), string(testContent))
	}
}

// TestS3List tests listing objects in an S3 bucket
func TestS3List(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	client := setupLocalStackClient(t)
	ctx := context.Background()

	// Create test prefix and objects
	testPrefix := "test-list/"
	testObjects := []string{
		testPrefix + "file1.txt",
		testPrefix + "file2.txt",
		testPrefix + "subdir/file3.txt",
	}

	// Put test objects
	for _, key := range testObjects {
		if err := client.PutObject(ctx, key, []byte("test content for "+key)); err != nil {
			t.Fatalf("Failed to put test object %s: %v", key, err)
		}
	}

	// List objects with the test prefix
	keys, err := client.List(ctx, testPrefix)
	if err != nil {
		t.Fatalf("Failed to list objects: %v", err)
	}

	// Verify all test objects are in the list
	foundCount := 0
	for _, testKey := range testObjects {
		found := false
		for _, key := range keys {
			if key == testKey {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Test object %s not found in listing", testKey)
		} else {
			foundCount++
		}
	}

	if foundCount != len(testObjects) {
		t.Errorf("Not all test objects were found in listing: found %d, expected %d", foundCount, len(testObjects))
	}
}

// TestS3FolderOperations tests folder-related operations in S3
func TestS3FolderOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	client := setupLocalStackClient(t)
	ctx := context.Background()

	// Test folder path
	testFolder := "test-folder-ops/nested/folder"

	// Check non-existent folder
	exists, err := client.FolderExists(ctx, testFolder)
	if err != nil {
		t.Fatalf("Failed to check folder existence: %v", err)
	}
	if exists {
		t.Errorf("Folder should not exist yet")
	}

	// Create the folder
	if err := client.CreateFolder(ctx, testFolder); err != nil {
		t.Fatalf("Failed to create folder: %v", err)
	}

	// Check folder exists now
	exists, err = client.FolderExists(ctx, testFolder)
	if err != nil {
		t.Fatalf("Failed to check folder existence: %v", err)
	}
	if !exists {
		t.Errorf("Folder should exist after creation")
	}

	// Put an object in the folder
	testKey := testFolder + "/test-file.txt"
	if err := client.PutObject(ctx, testKey, []byte("test content")); err != nil {
		t.Fatalf("Failed to put test object: %v", err)
	}

	// List the folder contents
	keys, err := client.List(ctx, testFolder)
	if err != nil {
		t.Fatalf("Failed to list folder: %v", err)
	}

	// Should find at least the folder marker and the test file
	expectedKeys := 2
	if len(keys) < expectedKeys {
		t.Errorf("Expected at least %d keys in folder, got %d", expectedKeys, len(keys))
	}

	// Verify the test file is in the list
	found := false
	for _, key := range keys {
		if key == testKey {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Test file not found in folder listing")
	}
}

// TestS3BucketExists tests checking bucket existence
func TestS3BucketExists(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	client := setupLocalStackClient(t)
	ctx := context.Background()

	// Check the test bucket exists
	exists, err := client.BucketExists(ctx)
	if err != nil {
		t.Fatalf("Failed to check bucket existence: %v", err)
	}
	if !exists {
		t.Errorf("Test bucket should exist")
	}

	// Check a non-existent bucket
	nonExistentClient, err := NewClientFromEnv("non-existent-bucket-" + time.Now().Format("20060102150405"))
	if err != nil {
		t.Fatalf("Failed to create client for non-existent bucket: %v", err)
	}

	exists, err = nonExistentClient.BucketExists(ctx)
	if err != nil {
		t.Fatalf("Failed to check non-existent bucket: %v", err)
	}
	if exists {
		t.Errorf("Non-existent bucket should not exist")
	}
}
