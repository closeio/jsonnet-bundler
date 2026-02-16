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
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/fatih/color"
	"github.com/jsonnet-bundler/jsonnet-bundler/pkg/s3"
)

// cacheResult represents the result of a cache operation
type cacheResult struct {
	success  bool
	tempPath string
	cacheURL string
	err      error
}

// parallelCheckRemoteCaches checks multiple remote caches in parallel and returns the first successful result
func parallelCheckRemoteCaches(remoteCaches []string, cacheKey string) (string, error) {
	return parallelCheckRemoteCachesWithTimeout(remoteCaches, cacheKey, 30*time.Second)
}

// parallelCheckRemoteCachesWithTimeout checks multiple remote caches with a timeout
func parallelCheckRemoteCachesWithTimeout(remoteCaches []string, cacheKey string, timeout time.Duration) (string, error) {
	if len(remoteCaches) == 0 {
		return "", fmt.Errorf("no remote caches to check")
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	resultChan := make(chan cacheResult, len(remoteCaches))
	var wg sync.WaitGroup

	// Launch goroutines to check each remote cache with context support
	for _, remoteURL := range remoteCaches {
		wg.Add(1)
		go func(cacheURL string) {
			defer wg.Done()

			// Check for context cancellation
			select {
			case <-ctx.Done():
				resultChan <- cacheResult{success: false, cacheURL: cacheURL, err: ctx.Err()}
				return
			default:
			}

			result := fetchFromRemoteCache(ctx, cacheURL, cacheKey)
			resultChan <- result
		}(remoteURL)
	}

	// Close the result channel when all goroutines are done
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Use a more efficient approach to handle results
	return handleCacheResults(ctx, resultChan)
}

// fetchFromRemoteCache handles fetching from a single remote cache
func fetchFromRemoteCache(ctx context.Context, cacheURL, cacheKey string) cacheResult {
	// Parse the remote URL
	cacheBaseURL, err := url.Parse(cacheURL)
	if err != nil {
		return cacheResult{success: false, cacheURL: cacheURL, err: err}
	}

	// Check if this is an S3 URL
	if cacheBaseURL.Scheme == "s3" {
		return fetchFromS3Cache(ctx, cacheURL, cacheKey)
	}
	return fetchFromHTTPCache(ctx, cacheBaseURL, cacheURL, cacheKey)
}

// fetchFromS3Cache handles S3 cache fetching
func fetchFromS3Cache(ctx context.Context, cacheURL, cacheKey string) cacheResult {
	// Construct the S3 URL with the object key
	s3URL := fmt.Sprintf("%s/%s.tar.gz", cacheURL, cacheKey)

	// Create a temporary file for this goroutine
	tempFile, err := os.CreateTemp("", "jb-cache-*.tar.gz")
	if err != nil {
		return cacheResult{success: false, cacheURL: cacheURL, err: err}
	}
	tempPath := tempFile.Name()
	tempFile.Close()

	// Download to the temporary file with context support
	// Note: s3.SaveObjectToFile doesn't support context yet, but we can add timeout
	done := make(chan error, 1)
	go func() {
		done <- s3.SaveObjectToFile(s3URL, tempPath, true) // Use quiet mode in parallel
	}()

	select {
	case <-ctx.Done():
		os.Remove(tempPath)
		return cacheResult{success: false, cacheURL: cacheURL, err: ctx.Err()}
	case err := <-done:
		if err != nil {
			os.Remove(tempPath)
			return cacheResult{success: false, cacheURL: cacheURL, err: err}
		}
		return cacheResult{success: true, tempPath: tempPath, cacheURL: s3URL, err: nil}
	}
}

// fetchFromHTTPCache handles HTTP/HTTPS cache fetching
func fetchFromHTTPCache(ctx context.Context, cacheBaseURL *url.URL, cacheURL, cacheKey string) cacheResult {
	// Handle HTTP/HTTPS URLs
	cacheRequestURL := *cacheBaseURL // Copy the URL
	cacheRequestURL.Path = path.Join(cacheRequestURL.Path, cacheKey+".tar.gz")

	// Create request with context
	req, err := http.NewRequestWithContext(ctx, "GET", cacheRequestURL.String(), nil)
	if err != nil {
		return cacheResult{success: false, cacheURL: cacheURL, err: err}
	}

	// Configure HTTP client with timeout
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	// Send GET request to remote cache
	resp, err := client.Do(req)
	if err != nil {
		return cacheResult{success: false, cacheURL: cacheURL, err: err}
	}
	defer resp.Body.Close()

	// Check for cache hit
	if resp.StatusCode == http.StatusOK {
		return downloadFromHTTPResponse(ctx, resp, cacheRequestURL.String(), cacheURL)
	}
	return cacheResult{success: false, cacheURL: cacheURL, err: fmt.Errorf("status code %d", resp.StatusCode)}
}

// downloadFromHTTPResponse handles downloading the response body to a temp file
func downloadFromHTTPResponse(ctx context.Context, resp *http.Response, requestURL, cacheURL string) cacheResult {
	// Create a temporary file for this goroutine
	tempFile, err := os.CreateTemp("", "jb-cache-*.tar.gz")
	if err != nil {
		return cacheResult{success: false, cacheURL: cacheURL, err: err}
	}
	defer tempFile.Close()

	// Copy from response to file with context support
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(tempFile, resp.Body)
		done <- err
	}()

	select {
	case <-ctx.Done():
		os.Remove(tempFile.Name())
		return cacheResult{success: false, cacheURL: cacheURL, err: ctx.Err()}
	case err := <-done:
		if err != nil {
			os.Remove(tempFile.Name())
			return cacheResult{success: false, cacheURL: cacheURL, err: err}
		}
		// Success
		return cacheResult{success: true, tempPath: tempFile.Name(), cacheURL: requestURL, err: nil}
	}
}

// handleCacheResults processes cache results and returns the first successful one
func handleCacheResults(ctx context.Context, resultChan <-chan cacheResult) (string, error) {
	var tempFiles []string
	var tempFilesMutex sync.Mutex
	
	defer func() {
		// Clean up any remaining temp files with mutex protection
		tempFilesMutex.Lock()
		defer tempFilesMutex.Unlock()
		for _, tf := range tempFiles {
			os.Remove(tf)
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case result, ok := <-resultChan:
			if !ok {
				return "", fmt.Errorf("no successful cache hits")
			}

			if result.success {
				if !GetGitQuiet() {
					color.Cyan("REMOTE CACHE HIT (parallel): %s", result.cacheURL)
				}
				// Remove successful temp file from cleanup list
				tempFilesMutex.Lock()
				for i, tf := range tempFiles {
					if tf == result.tempPath {
						tempFiles = append(tempFiles[:i], tempFiles[i+1:]...)
						break
					}
				}
				tempFilesMutex.Unlock()
				return result.tempPath, nil
			} else if result.tempPath != "" {
				// Add failed temp file to cleanup list
				tempFilesMutex.Lock()
				tempFiles = append(tempFiles, result.tempPath)
				tempFilesMutex.Unlock()
			}
		}
	}
}

// parallelPopulateRemoteS3Caches uploads a file to multiple S3 remote caches in parallel
func parallelPopulateRemoteS3Caches(remoteCaches []string, filePath, cacheKey string) {
	parallelPopulateRemoteS3CachesWithTimeout(remoteCaches, filePath, cacheKey, 5*time.Minute)
}

// S3UploadResult represents the result of an S3 upload operation
type S3UploadResult struct {
	CacheURL string
	Success  bool
	Error    error
}

// parallelPopulateRemoteS3CachesWithTimeout uploads with a timeout
func parallelPopulateRemoteS3CachesWithTimeout(remoteCaches []string, filePath, cacheKey string, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Get file stats for verification
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		if !GetGitQuiet() {
			color.Yellow("WARNING: Failed to get file info for S3 upload: %v", err)
		}
		return
	}

	// Skip directories
	if fileInfo.IsDir() {
		if !GetGitQuiet() {
			color.Yellow("WARNING: Cannot upload directory to S3 cache: %s", filePath)
		}
		return
	}

	// Format the S3 key
	s3Key := cacheKey + ".tar.gz"

	// Filter S3 URLs and create upload tasks
	s3URLs := make([]string, 0, len(remoteCaches))
	for _, remoteURL := range remoteCaches {
		if strings.HasPrefix(remoteURL, "s3://") {
			s3URLs = append(s3URLs, remoteURL)
		}
	}

	if len(s3URLs) == 0 {
		return // No S3 caches to populate
	}

	var wg sync.WaitGroup

	// Process S3 uploads with worker pool for better resource management
	uploadCh := make(chan string, len(s3URLs))
	for _, s3URL := range s3URLs {
		uploadCh <- s3URL
	}
	close(uploadCh)

	// Limit concurrent uploads to avoid overwhelming S3
	maxWorkers := min(len(s3URLs), 5)
	for i := 0; i < maxWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case cacheURL, ok := <-uploadCh:
					if !ok {
						return
					}
					uploadToS3Cache(ctx, cacheURL, filePath, s3Key)
				}
			}
		}()
	}

	// Wait for all uploads to complete or timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All uploads completed
	case <-ctx.Done():
		if !GetGitQuiet() {
			color.Yellow("WARNING: S3 cache population timed out")
		}
	}
}

// uploadToS3Cache handles uploading to a single S3 cache
func uploadToS3Cache(ctx context.Context, cacheURL, filePath, s3Key string) {
	result := uploadToS3CacheWithResult(ctx, cacheURL, filePath, s3Key)
	if !result.Success && !GetGitQuiet() {
		color.Yellow("WARNING: Failed to upload to S3 cache %s: %v", cacheURL, result.Error)
	} else if result.Success && !GetGitQuiet() {
		color.Green("Successfully populated S3 remote cache: %s/%s", cacheURL, s3Key)
	}
}

// uploadToS3CacheWithResult handles uploading to a single S3 cache and returns detailed result
func uploadToS3CacheWithResult(ctx context.Context, cacheURL, filePath, s3Key string) S3UploadResult {
	// Extract bucket from S3 URL
	parsedURL, parseErr := url.Parse(cacheURL)
	if parseErr != nil {
		return S3UploadResult{
			CacheURL: cacheURL,
			Success:  false,
			Error:    fmt.Errorf("failed to parse S3 URL: %w", parseErr),
		}
	}

	// Get bucket name from URL host
	bucket := parsedURL.Host

	// Get the path prefix from the URL (e.g., /jsonnet-bundler/cache)
	pathPrefix := strings.TrimPrefix(parsedURL.Path, "/")

	// Construct the full S3 key with path
	fullS3Key := s3Key
	if pathPrefix != "" {
		fullS3Key = pathPrefix + "/" + s3Key
	}

	// Create S3 client using environment variables
	client, err := s3.NewClientFromEnv(bucket)
	if err != nil {
		return S3UploadResult{
			CacheURL: cacheURL,
			Success:  false,
			Error:    fmt.Errorf("failed to create S3 client: %w", err),
		}
	}

	// Check if bucket exists and is accessible before attempting upload
	exists, err := client.BucketExists(ctx)
	if err != nil {
		return S3UploadResult{
			CacheURL: cacheURL,
			Success:  false,
			Error:    fmt.Errorf("failed to check bucket existence: %w", err),
		}
	}

	if !exists {
		return S3UploadResult{
			CacheURL: cacheURL,
			Success:  false,
			Error:    fmt.Errorf("bucket '%s' does not exist or is not accessible", client.Bucket),
		}
	}

	// Check if the object already exists in S3
	objectExists, err := client.ObjectExists(ctx, fullS3Key)
	if err != nil {
		return S3UploadResult{
			CacheURL: cacheURL,
			Success:  false,
			Error:    fmt.Errorf("failed to check object existence: %w", err),
		}
	}

	if objectExists {
		if !GetGitQuiet() {
			color.Green("Object %s already exists in S3 cache %s, skipping upload", fullS3Key, cacheURL)
		}
		return S3UploadResult{
			CacheURL: cacheURL,
			Success:  true,
			Error:    nil,
		}
	}

	// Upload the file
	err = client.Upload(ctx, filePath, fullS3Key)
	if err != nil {
		return S3UploadResult{
			CacheURL: cacheURL,
			Success:  false,
			Error:    fmt.Errorf("upload failed: %w", err),
		}
	}

	return S3UploadResult{
		CacheURL: cacheURL,
		Success:  true,
		Error:    nil,
	}
}
