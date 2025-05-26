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

	"github.com/fatih/color"
	"github.com/jsonnet-bundler/jsonnet-bundler/pkg/s3"
)

// parallelCheckRemoteCaches checks multiple remote caches in parallel and returns the first successful result
func parallelCheckRemoteCaches(remoteCaches []string, cacheKey string) (string, error) {
	if len(remoteCaches) == 0 {
		return "", fmt.Errorf("no remote caches to check")
	}

	type cacheResult struct {
		success  bool
		tempPath string
		cacheURL string
		err      error
	}

	resultChan := make(chan cacheResult, len(remoteCaches))
	var wg sync.WaitGroup

	// Launch goroutines to check each remote cache
	for _, remoteURL := range remoteCaches {
		wg.Add(1)
		go func(cacheURL string) {
			defer wg.Done()

			// Parse the remote URL
			cacheBaseURL, err := url.Parse(cacheURL)
			if err != nil {
				resultChan <- cacheResult{success: false, cacheURL: cacheURL, err: err}
				return
			}

			// Check if this is an S3 URL
			if cacheBaseURL.Scheme == "s3" {
				// Construct the S3 URL with the object key
				s3URL := fmt.Sprintf("%s/%s.tar.gz", cacheURL, cacheKey)

				// Create a temporary file for this goroutine
				tempFile, err := os.CreateTemp("", "jb-cache-*.tar.gz")
				if err != nil {
					resultChan <- cacheResult{success: false, cacheURL: cacheURL, err: err}
					return
				}
				tempPath := tempFile.Name()
				tempFile.Close()

				// Download to the temporary file
				err = s3.SaveObjectToFile(s3URL, tempPath, true) // Use quiet mode in parallel
				if err != nil {
					os.Remove(tempPath)
					resultChan <- cacheResult{success: false, cacheURL: cacheURL, err: err}
					return
				}

				// Success
				resultChan <- cacheResult{success: true, tempPath: tempPath, cacheURL: s3URL, err: nil}
			} else {
				// Handle HTTP/HTTPS URLs
				cacheRequestURL := *cacheBaseURL // Copy the URL
				cacheRequestURL.Path = path.Join(cacheRequestURL.Path, cacheKey+".tar.gz")

				// Send GET request to remote cache
				resp, err := http.Get(cacheRequestURL.String())
				if err != nil {
					resultChan <- cacheResult{success: false, cacheURL: cacheURL, err: err}
					return
				}
				defer resp.Body.Close()

				// Check for cache hit
				if resp.StatusCode == http.StatusOK {
					// Create a temporary file for this goroutine
					tempFile, err := os.CreateTemp("", "jb-cache-*.tar.gz")
					if err != nil {
						resultChan <- cacheResult{success: false, cacheURL: cacheURL, err: err}
						return
					}

					// Copy from response to file
					_, err = io.Copy(tempFile, resp.Body)
					tempFile.Close()
					if err != nil {
						os.Remove(tempFile.Name())
						resultChan <- cacheResult{success: false, cacheURL: cacheURL, err: err}
						return
					}

					// Success
					resultChan <- cacheResult{success: true, tempPath: tempFile.Name(), cacheURL: cacheRequestURL.String(), err: nil}
				} else {
					resultChan <- cacheResult{success: false, cacheURL: cacheURL, err: fmt.Errorf("status code %d", resp.StatusCode)}
				}
			}
		}(remoteURL)
	}

	// Close the result channel when all goroutines are done
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Collect results and return the first successful one
	var tempFiles []string
	defer func() {
		// Clean up any remaining temp files
		for _, tf := range tempFiles {
			os.Remove(tf)
		}
	}()

	for result := range resultChan {
		if result.success {
			if !GitQuiet {
				color.Cyan("REMOTE CACHE HIT (parallel): %s", result.cacheURL)
			}
			// Remove this file from cleanup list
			for i, tf := range tempFiles {
				if tf == result.tempPath {
					tempFiles = append(tempFiles[:i], tempFiles[i+1:]...)
					break
				}
			}
			return result.tempPath, nil
		} else if result.tempPath != "" {
			tempFiles = append(tempFiles, result.tempPath)
		}
	}

	return "", fmt.Errorf("no successful cache hits")
}

// parallelPopulateRemoteS3Caches uploads a file to multiple S3 remote caches in parallel
func parallelPopulateRemoteS3Caches(remoteCaches []string, filePath, cacheKey string) {
	// Get file stats for verification
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		if !GitQuiet {
			color.Yellow("WARNING: Failed to get file info for S3 upload: %v", err)
		}
		return
	}

	// Skip directories
	if fileInfo.IsDir() {
		if !GitQuiet {
			color.Yellow("WARNING: Cannot upload directory to S3 cache: %s", filePath)
		}
		return
	}

	// Format the S3 key
	s3Key := cacheKey + ".tar.gz"

	var wg sync.WaitGroup

	// Loop through remote caches
	for _, remoteURL := range remoteCaches {
		// Skip non-S3 remotes
		if !strings.HasPrefix(remoteURL, "s3://") {
			continue
		}

		wg.Add(1)
		go func(cacheURL string) {
			defer wg.Done()

			// Extract bucket from S3 URL
			parsedURL, parseErr := url.Parse(cacheURL)
			if parseErr != nil {
				if !GitQuiet {
					color.Yellow("WARNING: Failed to parse S3 URL %s: %v", cacheURL, parseErr)
				}
				return
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

			if !GitQuiet {
				color.Green("Uploading to S3 remote cache: s3://%s/%s", bucket, fullS3Key)
			}

			// Create S3 client using environment variables
			client, err := s3.NewClientFromEnv(bucket)
			if err != nil {
				if !GitQuiet {
					color.Yellow("WARNING: Failed to create S3 client for %s: %v", cacheURL, err)
				}
				return
			}

			// Check if bucket exists and is accessible before attempting upload
			ctx := context.Background()
			exists, err := client.BucketExists(ctx)
			if err != nil {
				if !GitQuiet {
					color.Yellow("WARNING: Failed to check if bucket exists: %v", err)
				}
				return
			}

			if !exists {
				if !GitQuiet {
					color.Yellow("WARNING: Bucket '%s' does not exist or is not accessible", client.Bucket)
				}
				return
			}

			// Check if the object already exists in S3
			objectExists, err := client.ObjectExists(ctx, fullS3Key)
			if err != nil {
				if !GitQuiet {
					color.Yellow("WARNING: Failed to check if object exists in S3: %v", err)
				}
				return
			}

			if objectExists {
				if !GitQuiet {
					color.Green("Object already exists in S3 remote cache, skipping upload: s3://%s/%s", bucket, fullS3Key)
				}
				return
			}

			// Upload the file
			err = client.Upload(ctx, filePath, fullS3Key)
			if err != nil {
				if !GitQuiet {
					color.Yellow("WARNING: Failed to upload to S3 cache %s: %v", cacheURL, err)
				}
				return
			}

			if !GitQuiet {
				color.Green("Successfully populated S3 remote cache: %s", cacheURL)
			}
		}(remoteURL)
	}

	// Wait for all uploads to complete
	wg.Wait()
}
