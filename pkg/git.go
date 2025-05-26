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
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/fatih/color"
	"github.com/pkg/errors"

	"github.com/jsonnet-bundler/jsonnet-bundler/pkg/cache"
	"github.com/jsonnet-bundler/jsonnet-bundler/pkg/s3"
	"github.com/jsonnet-bundler/jsonnet-bundler/spec/v1/deps"
)

// extractGitHubCommitSHA extracts the commit SHA from a GitHub archive URL
// Returns empty string if not a GitHub archive URL or cannot extract SHA
func extractGitHubCommitSHA(archiveUrl string) string {
	// Pattern for GitHub archive URLs: https://github.com/owner/repo/archive/{sha}.tar.gz
	pattern := regexp.MustCompile(`^https://github\.com/[^/]+/[^/]+/archive/([0-9a-f]{40,})\.tar\.gz$`)
	matches := pattern.FindStringSubmatch(archiveUrl)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

// getCacheKeyForURL generates a cache key for the given URL
// For GitHub archive URLs with commit SHAs, uses the SHA directly
// For other URLs, uses a hash of the URL
func getCacheKeyForURL(archiveUrl string) string {
	// Try to extract GitHub commit SHA first
	if sha := extractGitHubCommitSHA(archiveUrl); sha != "" {
		return sha
	}

	// Fall back to URL hash for non-GitHub or non-standard URLs
	urlHash := sha256.Sum256([]byte(archiveUrl))
	return hex.EncodeToString(urlHash[:16])
}

// getArchiveMutex returns a mutex for the given cache key, creating one if necessary
func getArchiveMutex(cacheKey string) *sync.Mutex {
	archiveCacheMutexesLock.Lock()
	defer archiveCacheMutexesLock.Unlock()

	if mutex, exists := archiveCacheMutexes[cacheKey]; exists {
		return mutex
	}

	mutex := &sync.Mutex{}
	archiveCacheMutexes[cacheKey] = mutex
	return mutex
}

type GitPackage struct {
	Source *deps.Git
}

func NewGitPackage(source *deps.Git) Interface {
	return &GitPackage{
		Source: source,
	}
}

var (
	GitQuiet = false
	// GlobalCacheEnabled controls whether to use a global cache
	GlobalCacheEnabled = true
	// DefaultGlobalCacheDir is the default location for the global cache
	DefaultGlobalCacheDir = ""

	// globalCacheMutex protects concurrent access to the global cache
	globalCacheMutex sync.Mutex
	// archiveCacheMutexes protects concurrent downloads of the same archive
	archiveCacheMutexes     = make(map[string]*sync.Mutex)
	archiveCacheMutexesLock sync.Mutex
)

// validateGzipFile checks if a file is a valid gzip file by reading through the entire archive
func validateGzipFile(filepath string) error {
	file, err := os.Open(filepath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// Get file info for size
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat file: %w", err)
	}

	// Try to read the gzip header
	gr, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("invalid gzip header: %w", err)
	}
	defer gr.Close()

	// Read through the entire tar archive to ensure it's not corrupted
	tr := tar.NewReader(gr)
	entriesProcessed := 0
	totalSize := int64(0)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			// Reached end of archive
			break
		}
		if err != nil {
			return fmt.Errorf("corrupted tar entry at position %d: %w", entriesProcessed, err)
		}

		if header == nil {
			continue
		}

		entriesProcessed++

		// For regular files, read through the content to ensure it's not corrupted
		if header.Typeflag == tar.TypeReg {
			// Use io.CopyN to read the exact file size
			n, err := io.CopyN(io.Discard, tr, header.Size)
			if err != nil {
				return fmt.Errorf("corrupted file content for %s (read %d of %d bytes): %w",
					header.Name, n, header.Size, err)
			}
			totalSize += n
		}
	}

	if entriesProcessed == 0 {
		return fmt.Errorf("tar archive is empty")
	}

	// Archive is valid - we successfully read through all entries
	if !GitQuiet {
		color.Green("Archive validation passed (size: %d bytes, entries: %d, content size: %d bytes)",
			info.Size(), entriesProcessed, totalSize)
	}

	return nil
}

func downloadGitHubArchive(filepath string, urlStr string) error {
	// Check if this is an S3 URL
	if s3.IsS3URL(urlStr) {
		if !GitQuiet {
			color.Cyan("S3 GET %s", urlStr)
		}

		// Download directly from S3 to file
		err := s3.SaveObjectToFile(urlStr, filepath, GitQuiet)
		if err != nil {
			// Parse the S3 URL to provide more debug information
			parsedURL, parseErr := url.Parse(urlStr)
			if parseErr != nil {
				// Can't parse URL, return the original error with a note
				return errors.Wrap(err, "S3 download failed (URL could not be parsed)")
			}

			// Extract components
			bucket := parsedURL.Host
			path := parsedURL.Path
			queryParams := parsedURL.Query()

			// Create detailed error message with URL components
			errorDetails := fmt.Sprintf(
				"S3 download failed with URL components: bucket=%s, path=%s, query_params=%v",
				bucket, path, queryParams,
			)
			return errors.Wrap(err, errorDetails)
		}
		return nil
	}

	// Handle regular HTTP URLs with retry logic
	const maxRetries = 3
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		// Add exponential backoff for retries
		if attempt > 1 {
			backoffTime := time.Duration(attempt-1) * time.Second
			if !GitQuiet {
				color.Yellow("Retrying download (attempt %d/%d) after %v...", attempt, maxRetries, backoffTime)
			}
			time.Sleep(backoffTime)
		}

		// Get the data
		resp, err := http.Get(urlStr)
		if err != nil {
			lastErr = err
			if !GitQuiet {
				color.Yellow("Download attempt %d/%d failed: %v", attempt, maxRetries, err)
			}
			continue
		}

		if !GitQuiet {
			color.Cyan("GET %s %d (attempt %d/%d)", urlStr, resp.StatusCode, attempt, maxRetries)
		}

		// Handle different status codes
		if resp.StatusCode == 200 {
			// Success - proceed with download
			defer resp.Body.Close()

			// Get ETag for integrity verification if available
			etag := resp.Header.Get("ETag")
			if etag != "" && !GitQuiet {
				color.Cyan("Download ETag: %s", etag)
			}

			// Create the file
			out, err := os.Create(filepath)
			if err != nil {
				resp.Body.Close()
				return err
			}
			defer out.Close()

			// Write the body to file with a hash calculator
			hasher := sha256.New()
			writer := io.MultiWriter(out, hasher)
			written, err := io.Copy(writer, resp.Body)
			resp.Body.Close()
			out.Close() // Close the file explicitly before validation

			if err != nil {
				os.Remove(filepath) // Clean up partial file
				lastErr = err
				if !GitQuiet {
					color.Yellow("Download attempt %d/%d failed during file write: %v", attempt, maxRetries, err)
				}
				continue
			}

			// Calculate file hash
			fileHash := fmt.Sprintf("%x", hasher.Sum(nil))
			if !GitQuiet {
				color.Cyan("Downloaded file SHA256: %s", fileHash)
			}

			// Verify file size if Content-Length was provided
			if contentLength := resp.ContentLength; contentLength > 0 && written != contentLength {
				os.Remove(filepath)
				lastErr = fmt.Errorf("incomplete download: expected %d bytes, got %d", contentLength, written)
				if !GitQuiet {
					color.Yellow("Download attempt %d/%d incomplete: %v", attempt, maxRetries, lastErr)
				}
				continue
			}

			// Validate it's a valid gzip file by reading through the entire archive
			if err := validateGzipFile(filepath); err != nil {
				os.Remove(filepath)
				// Check if this is an EOF error which might indicate truncation
				if strings.Contains(err.Error(), "unexpected EOF") || strings.Contains(err.Error(), "EOF") {
					lastErr = fmt.Errorf("archive appears to be truncated or corrupted: %w", err)
				} else {
					lastErr = fmt.Errorf("invalid archive: %w", err)
				}
				if !GitQuiet {
					color.Yellow("Download attempt %d/%d produced invalid archive: %v", attempt, maxRetries, err)
				}
				continue
			}

			// Success!
			return nil
		} else if resp.StatusCode >= 500 || resp.StatusCode == 429 {
			// Server error or rate limit - worth retrying
			resp.Body.Close()
			lastErr = fmt.Errorf("unexpected status code %d", resp.StatusCode)
			if !GitQuiet {
				color.Yellow("Download attempt %d/%d failed with status %d", attempt, maxRetries, resp.StatusCode)
			}
			continue
		} else {
			// Client error (4xx except 429) - don't retry
			resp.Body.Close()
			return fmt.Errorf("unexpected status code %d", resp.StatusCode)
		}
	}

	// All retries failed
	if lastErr != nil {
		return fmt.Errorf("download failed after %d attempts: %w", maxRetries, lastErr)
	}
	return fmt.Errorf("download failed after %d attempts", maxRetries)
}

// getGlobalCacheDir returns the path to the global cache directory
func getGlobalCacheDir() (string, error) {
	// Check if a custom cache directory is specified
	if envCacheDir := os.Getenv("JB_CACHE_DIR"); envCacheDir != "" {
		if !GitQuiet {
			color.Cyan("Using environment cache directory: %s", envCacheDir)
		}

		if err := os.MkdirAll(envCacheDir, os.ModePerm); err != nil {
			return "", errors.Wrap(err, "failed to create custom cache directory from JB_CACHE_DIR")
		}

		return envCacheDir, nil
	}

	// Check if we have a pre-configured default
	if DefaultGlobalCacheDir != "" {
		if err := os.MkdirAll(DefaultGlobalCacheDir, os.ModePerm); err != nil {
			return "", errors.Wrap(err, "failed to create configured default cache directory")
		}

		return DefaultGlobalCacheDir, nil
	}

	// Use default location: ~/.cache/jb
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", errors.Wrap(err, "failed to get user home directory")
	}

	cacheDir := filepath.Join(homeDir, ".cache", "jb")
	if err := os.MkdirAll(cacheDir, os.ModePerm); err != nil {
		return "", errors.Wrap(err, "failed to create global cache directory")
	}

	return cacheDir, nil
}

// ensureArchiveCache ensures the file exists at the destination path,
// checking in this order:
// 1. Global cache
// 2. Remote caches (if configured)
// 3. Upstream source (if all else fails)
func ensureArchiveCache(archiveFilepath, archiveUrl string) error {
	// Create a unique key based on the URL
	cacheKey := getCacheKeyForURL(archiveUrl)

	// Get a mutex for this specific archive to prevent concurrent downloads
	archiveMutex := getArchiveMutex(cacheKey)
	archiveMutex.Lock()
	defer archiveMutex.Unlock()

	// Check if file already exists at the destination (double-check after acquiring lock)
	if _, err := os.Stat(archiveFilepath); err == nil {
		if !GitQuiet {
			color.Green("FILE ALREADY EXISTS %s", archiveFilepath)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	// Step 1: If global cache is enabled, check the global cache
	if GlobalCacheEnabled {
		globalCacheDir, err := getGlobalCacheDir()
		if err != nil {
			// If we can't access the global cache, log warning and download directly
			if !GitQuiet {
				color.Yellow("WARNING: Could not access global cache: %v", err)
			}
		} else {
			// Global cache path for this file
			globalArchivePath := filepath.Join(globalCacheDir, cacheKey+".tar.gz")

			// Check if file exists in global cache
			if _, err := os.Stat(globalArchivePath); err == nil {
				if !GitQuiet {
					color.Cyan("GLOBAL CACHE HIT %s", globalArchivePath)
				}

				// Ensure the directory for the destination file exists
				if err := os.MkdirAll(filepath.Dir(archiveFilepath), os.ModePerm); err != nil {
					return errors.Wrap(err, "failed to create destination directory")
				}

				// Copy from global cache to destination
				err := CopyFile(globalArchivePath, archiveFilepath)
				if err != nil {
					return errors.Wrap(err, "failed to copy file from global cache")
				}

				if !GitQuiet {
					color.Green("Copied file from global cache")
				}

				// Register in global cache index
				registerInGlobalCacheIndex(globalArchivePath, archiveUrl)

				// Populate remote S3 caches in parallel
				if remoteCaches := cache.GetGlobalRemoteCaches(GitQuiet); len(remoteCaches) > 0 {
					if !GitQuiet {
						color.Green("Populating remote S3 caches after upstream download...")
					}
					parallelPopulateRemoteS3Caches(remoteCaches, globalArchivePath, cacheKey)
				}

				return nil
			}

			// Step 2: Check remote caches
			// Read the index file to get remote caches
			indexPath := filepath.Join(globalCacheDir, "index.json")
			var remoteCaches []string

			if indexData, err := os.ReadFile(indexPath); err == nil {
				// if !GitQuiet {
				// 	color.Cyan("Looking for remote caches in: %s", indexPath)
				// }

				// Try parsing with struct for lowercase field
				var indexWithLower struct {
					RemoteCaches []string `json:"remoteCaches"`
				}
				if err := json.Unmarshal(indexData, &indexWithLower); err == nil && len(indexWithLower.RemoteCaches) > 0 {
					remoteCaches = indexWithLower.RemoteCaches
				} else {
					// Try parsing for uppercase field (legacy)
					var indexWithUpper struct {
						RemoteCaches []string `json:"RemoteCaches"`
					}
					if err := json.Unmarshal(indexData, &indexWithUpper); err == nil && len(indexWithUpper.RemoteCaches) > 0 {
						remoteCaches = indexWithUpper.RemoteCaches
					}
				}

				if len(remoteCaches) > 0 {
					// Use the parallel function to check remote caches
					tempPath, err := parallelCheckRemoteCaches(remoteCaches, cacheKey)
					if err == nil {
						// Ensure destination directory exists
						if err := os.MkdirAll(filepath.Dir(archiveFilepath), os.ModePerm); err != nil {
							os.Remove(tempPath)
							return errors.Wrap(err, "failed to create destination directory")
						}

						// Move the temp file to destination
						err := os.Rename(tempPath, archiveFilepath)
						if err != nil {
							// If rename fails, try copy
							err = CopyFile(tempPath, archiveFilepath)
							os.Remove(tempPath)
							if err != nil {
								return errors.Wrap(err, "failed to move cached file to destination")
							}
						}

						// Copy to global cache too for future use
						if err := os.MkdirAll(filepath.Dir(globalArchivePath), os.ModePerm); err == nil {
							err := CopyFile(archiveFilepath, globalArchivePath)
							if err == nil {
								if !GitQuiet {
									color.Green("Copied file to global cache")
								}
								registerInGlobalCacheIndex(globalArchivePath, archiveUrl)
							}
						}

						return nil
					}
					// If parallel check failed, continue with the old sequential code as fallback
					// Try each remote cache
					for _, remoteURL := range remoteCaches {
						// Parse the remote URL
						cacheBaseURL, err := url.Parse(remoteURL)
						if err != nil {
							if !GitQuiet {
								color.Yellow("Invalid remote cache URL: %v", err)
							}
							continue
						}

						// Check if this is an S3 URL
						if cacheBaseURL.Scheme == "s3" {
							// Construct the S3 URL with the object key
							s3URL := fmt.Sprintf("%s/%s.tar.gz", remoteURL, cacheKey)

							// Ensure destination directory exists
							if err := os.MkdirAll(filepath.Dir(archiveFilepath), os.ModePerm); err != nil {
								return errors.Wrap(err, "failed to create destination directory")
							}

							// Download directly to the destination file
							err := s3.SaveObjectToFile(s3URL, archiveFilepath, GitQuiet)
							if err != nil {
								if !GitQuiet {
									color.Yellow("S3 CACHE MISS: Downloading from upstream")
								}
								continue
							}

							if !GitQuiet {
								color.Cyan("S3 REMOTE CACHE HIT: %s", s3URL)
							}

							// Copy to global cache too for future use
							if err := os.MkdirAll(filepath.Dir(globalArchivePath), os.ModePerm); err == nil {
								err := CopyFile(archiveFilepath, globalArchivePath)
								if err == nil {
									if !GitQuiet {
										color.Green("Copied file to global cache")
									}
									registerInGlobalCacheIndex(globalArchivePath, archiveUrl)
								}
							}

							return nil
						} else {
							// Handle HTTP/HTTPS URLs
							cacheRequestURL := *cacheBaseURL // Copy the URL
							cacheRequestURL.Path = path.Join(cacheRequestURL.Path, cacheKey+".tar.gz")

							if !GitQuiet {
								color.Cyan("Trying remote cache: %s", cacheRequestURL.String())
							}

							// Send GET request to remote cache
							resp, err := http.Get(cacheRequestURL.String())
							if err != nil {
								if !GitQuiet {
									color.Yellow("Remote cache error: %v", err)
								}
								continue
							}

							// Check for cache hit
							if resp.StatusCode == http.StatusOK {
								if !GitQuiet {
									color.Cyan("REMOTE CACHE HIT: %s", remoteURL)
								}

								// Ensure destination directory exists
								if err := os.MkdirAll(filepath.Dir(archiveFilepath), os.ModePerm); err != nil {
									resp.Body.Close()
									return errors.Wrap(err, "failed to create destination directory")
								}

								// Save to destination file
								destFile, err := os.Create(archiveFilepath)
								if err != nil {
									resp.Body.Close()
									return errors.Wrap(err, "failed to create destination file")
								}

								// Copy from response to file
								_, err = io.Copy(destFile, resp.Body)
								resp.Body.Close()
								destFile.Close()
								if err != nil {
									return errors.Wrap(err, "failed to write remote cache content to destination file")
								}

								// Copy to global cache too for future use
								if err := os.MkdirAll(filepath.Dir(globalArchivePath), os.ModePerm); err == nil {
									err := CopyFile(archiveFilepath, globalArchivePath)
									if err == nil {
										if !GitQuiet {
											color.Green("Copied file to global cache")
										}
										registerInGlobalCacheIndex(globalArchivePath, archiveUrl)
									}
								}

								return nil
							}

							// Not found, close and try next
							resp.Body.Close()
						}
					}
				} else if !GitQuiet {
					color.Yellow("No remote caches found in global index")
				}
			}

			// Step 3: Not in any cache, download to global cache and destination
			if !GitQuiet {
				color.Cyan("DOWNLOADING to global cache: %s", archiveUrl)
			}

			// First ensure global cache directory exists
			if err := os.MkdirAll(filepath.Dir(globalArchivePath), os.ModePerm); err != nil {
				if !GitQuiet {
					color.Yellow("WARNING: Could not create global cache directory: %v", err)
				}
				// Fall back to downloading directly to destination
				goto DownloadToDestination
			}

			// Download to global cache
			if err := downloadGitHubArchive(globalArchivePath, archiveUrl); err != nil {
				if !GitQuiet {
					// If this is an S3 error, the detailed URL components are already in the error message
					color.Yellow("WARNING: Could not download to global cache: %v", err)
				}
				// Fall back to downloading directly to destination
				goto DownloadToDestination
			}

			// Register in global cache index
			registerInGlobalCacheIndex(globalArchivePath, archiveUrl)

			// Copy from global cache to destination
			if err := os.MkdirAll(filepath.Dir(archiveFilepath), os.ModePerm); err != nil {
				return errors.Wrap(err, "failed to create destination directory")
			}

			err := CopyFile(globalArchivePath, archiveFilepath)
			if err != nil {
				return errors.Wrap(err, "failed to copy file from global cache")
			}

			if !GitQuiet {
				color.Green("Copied file from global cache")
			}

			return nil
		}
	}

DownloadToDestination:
	// Step 4 (alternative): Global cache is disabled or failed, download directly to destination
	if !GitQuiet {
		color.Cyan("DOWNLOADING directly to destination: %s", archiveUrl)
	}

	// Ensure destination directory exists
	if err := os.MkdirAll(filepath.Dir(archiveFilepath), os.ModePerm); err != nil {
		return errors.Wrap(err, "failed to create destination directory")
	}

	if err := downloadGitHubArchive(archiveFilepath, archiveUrl); err != nil {
		// Return the error directly, which will include detailed S3 URL components
		// if it was an S3 error from our enhanced error handling in downloadGitHubArchive
		return err
	}

	// Populate remote S3 caches in parallel
	if remoteCaches := cache.GetGlobalRemoteCaches(GitQuiet); len(remoteCaches) > 0 {
		if !GitQuiet {
			color.Green("Populating remote S3 caches after upstream download...")
		}
		parallelPopulateRemoteS3Caches(remoteCaches, archiveFilepath, cacheKey)
	}

	return nil
}

// registerInGlobalCacheIndex adds an entry to the global cache index
func registerInGlobalCacheIndex(filePath, url string) {
	// Lock for global cache index operations
	globalCacheMutex.Lock()
	defer globalCacheMutex.Unlock()

	// Create a unique key from the URL
	cacheKey := getCacheKeyForURL(url)

	// Get the global cache directory
	globalCacheDir, err := getGlobalCacheDir()
	if err != nil {
		if !GitQuiet {
			color.Yellow("WARNING: Could not access global cache: %v", err)
		}
		return
	}

	// Get file stats
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		if !GitQuiet {
			color.Yellow("WARNING: Could not register global cache entry: %v", err)
		}
		return
	}

	// Define cache entry struct
	type cacheEntry struct {
		Key          string            `json:"key"`
		Path         string            `json:"path"`
		Size         int64             `json:"size"`
		LastAccessed time.Time         `json:"lastAccessed"`
		Created      time.Time         `json:"created"`
		LastVerified time.Time         `json:"lastVerified"`
		Hash         string            `json:"hash"`
		Metadata     map[string]string `json:"metadata"`
	}

	// Define cache index struct
	type cacheIndex struct {
		Version      int          `json:"version"`
		LastCleaned  time.Time    `json:"lastCleaned"`
		Entries      []cacheEntry `json:"entries"`
		TotalSize    int64        `json:"totalSize"`
		RemoteCaches []string     `json:"remoteCaches"`
	}

	// Create entry
	entry := cacheEntry{
		Key:          cacheKey,
		Path:         filePath,
		Size:         fileInfo.Size(),
		LastAccessed: time.Now(),
		Created:      fileInfo.ModTime(),
		LastVerified: time.Now(),
		Hash:         "",
		Metadata: map[string]string{
			"url": url,
		},
	}

	// Read existing index or create new one
	var index cacheIndex
	indexPath := filepath.Join(globalCacheDir, "index.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		if !os.IsNotExist(err) {
			if !GitQuiet {
				color.Yellow("WARNING: Failed to read global cache index: %v", err)
			}
			return
		}

		// Create new index
		index = cacheIndex{
			Version:      1,
			LastCleaned:  time.Now(),
			Entries:      []cacheEntry{},
			TotalSize:    0,
			RemoteCaches: []string{},
		}
	} else {
		// Try to unmarshal using the strongly typed struct
		if err := json.Unmarshal(data, &index); err != nil {
			// If that fails, try with a raw map to handle field case variations
			var rawMap map[string]interface{}
			if jsonErr := json.Unmarshal(data, &rawMap); jsonErr == nil {
				// Initialize a new index
				index = cacheIndex{
					Version:      1,
					LastCleaned:  time.Now(),
					Entries:      []cacheEntry{},
					TotalSize:    0,
					RemoteCaches: []string{},
				}

				// Import entries array from raw JSON
				if entriesRaw, ok := rawMap["entries"]; ok {
					if entriesArray, ok := entriesRaw.([]interface{}); ok {
						for _, e := range entriesArray {
							if entryMap, ok := e.(map[string]interface{}); ok {
								// Extract fields from entryMap and create a proper cacheEntry
								key, _ := entryMap["key"].(string)
								path, _ := entryMap["path"].(string)

								// Only add valid entries and avoid duplicates
								if key != "" && key != cacheKey { // Skip entry if it's a duplicate of what we're adding
									size := int64(0)
									if s, ok := entryMap["size"].(float64); ok {
										size = int64(s)
									}

									// Create new entry with basic fields
									newEntry := cacheEntry{
										Key:          key,
										Path:         path,
										Size:         size,
										LastAccessed: time.Now(),
										Created:      time.Now(),
										LastVerified: time.Now(),
										Hash:         "",
										Metadata:     map[string]string{},
									}

									index.Entries = append(index.Entries, newEntry)
									index.TotalSize += size
								}
							}
						}
					}
				}

				// Import RemoteCaches array
				if remotesRaw, ok := rawMap["remoteCaches"]; ok {
					if remotesArray, ok := remotesRaw.([]interface{}); ok {
						for _, r := range remotesArray {
							if remoteURL, ok := r.(string); ok {
								index.RemoteCaches = append(index.RemoteCaches, remoteURL)
							}
						}
					}
				} else if remotesRaw, ok := rawMap["RemoteCaches"]; ok {
					// Try uppercase field name (legacy format)
					if remotesArray, ok := remotesRaw.([]interface{}); ok {
						for _, r := range remotesArray {
							if remoteURL, ok := r.(string); ok {
								index.RemoteCaches = append(index.RemoteCaches, remoteURL)
							}
						}
					}
				}
			} else {
				if !GitQuiet {
					color.Yellow("WARNING: Failed to parse global cache index, creating new one: %v", err)
				}
				// Create new index
				index = cacheIndex{
					Version:      1,
					LastCleaned:  time.Now(),
					Entries:      []cacheEntry{},
					TotalSize:    0,
					RemoteCaches: []string{},
				}
			}
		}
	}

	// Filter out duplicates by creating a new slice of entries
	newEntries := []cacheEntry{}
	addedKeys := make(map[string]bool)
	addedKeys[cacheKey] = true // Mark the new entry key as already added

	// First pass: Process existing entries and filter duplicates
	entryExists := false
	for _, existingEntry := range index.Entries {
		if existingEntry.Key == cacheKey {
			// Mark that we found the entry, but don't add it to newEntries yet
			entryExists = true
			continue // Skip adding the old entry, we'll add the new one later
		}

		// Only add entries that haven't been added yet (prevent duplicates)
		if !addedKeys[existingEntry.Key] {
			newEntries = append(newEntries, existingEntry)
			addedKeys[existingEntry.Key] = true
		} else if !GitQuiet {
			color.Yellow("Removing duplicate cache entry with key: %s", existingEntry.Key)
		}
	}

	// Add our new entry
	newEntries = append(newEntries, entry)

	// Update the index entries
	index.Entries = newEntries

	// Recalculate total size
	index.TotalSize = 0
	for _, e := range index.Entries {
		index.TotalSize += e.Size
	}

	// Save index
	data, err = json.MarshalIndent(index, "", "  ")
	if err != nil {
		if !GitQuiet {
			color.Yellow("WARNING: Failed to marshal global cache index: %v", err)
		}
		return
	}

	// Create cache directory if it doesn't exist
	if err := os.MkdirAll(globalCacheDir, os.ModePerm); err != nil {
		if !GitQuiet {
			color.Yellow("WARNING: Failed to create global cache directory: %v", err)
		}
		return
	}

	// Write to file
	if err := os.WriteFile(indexPath, data, 0644); err != nil {
		if !GitQuiet {
			color.Yellow("WARNING: Failed to write global cache index: %v", err)
		}
		return
	}

	if !GitQuiet {
		if entryExists {
			color.Green("Updated file in global cache index: %s.tar.gz", cacheKey)
		} else {
			color.Green("Added file to global cache index: %s.tar.gz", cacheKey)
		}
	}
}

func gzipUntar(dst string, r io.Reader, subDir string) error {
	gzr, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzr.Close()

	subDirWithoutSlash := strings.TrimPrefix(subDir, "/")

	tr := tar.NewReader(gzr)

	entriesProcessed := 0
	bytesProcessed := int64(0)
	var lastEntryName string

	for {
		header, err := tr.Next()
		switch {
		case err == io.EOF:
			if entriesProcessed == 0 {
				return fmt.Errorf("tar archive appears to be empty")
			}
			if !GitQuiet {
				color.Green("Successfully extracted %d entries (content size: %d bytes)", entriesProcessed, bytesProcessed)
			}
			return nil

		case err != nil:
			// Provide detailed error context
			errMsg := fmt.Sprintf("failed to read tar entry #%d", entriesProcessed+1)
			if lastEntryName != "" {
				errMsg += fmt.Sprintf(" (after '%s')", lastEntryName)
			}
			errMsg += fmt.Sprintf(", extracted %d bytes of content so far", bytesProcessed)
			return fmt.Errorf("%s: %w", errMsg, err)

		case header == nil:
			continue
		}
		entriesProcessed++
		lastEntryName = header.Name

		// strip the two first components of the path
		parts := strings.SplitAfterN(header.Name, "/", 2)
		if len(parts) < 2 {
			continue
		}
		suffix := parts[1]
		prefix := dst

		// reconstruct the target parh for the archive entry
		target := filepath.Join(prefix, suffix)

		// if subdir is provided and target is not under it, skip it
		subDirPath := filepath.Join(prefix, subDir)
		if subDir != "" && !strings.HasPrefix(target, subDirPath) {
			continue
		}

		// strip the subdir part if present
		if subDir != "" {
			target = filepath.Join(prefix, strings.TrimPrefix(suffix, subDirWithoutSlash))
		}

		// check the file type
		switch header.Typeflag {

		// create directories as needed
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode)); err != nil {
				return err
			}

		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), os.ModePerm); err != nil {
				return err
			}

			err := func() error {
				f, err := os.OpenFile(target, os.O_CREATE|os.O_RDWR, os.FileMode(header.Mode))
				if err != nil {
					return err
				}
				defer f.Close()

				// copy over contents and track bytes
				written, err := io.Copy(f, tr)
				if err != nil {
					return fmt.Errorf("failed to extract %s: %w", header.Name, err)
				}
				bytesProcessed += written

				// Verify we read the expected amount
				if written != header.Size {
					return fmt.Errorf("file %s: size mismatch (expected %d bytes, got %d)", header.Name, header.Size, written)
				}
				return nil
			}()

			if err != nil {
				return err
			}

		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), os.ModePerm); err != nil {
				return err
			}

			if err := os.Symlink(header.Linkname, target); err != nil {
				return err
			}
		}
	}
}

func remoteResolveRef(ctx context.Context, remote string, ref string) (string, error) {
	b := &bytes.Buffer{}
	cmd := exec.CommandContext(ctx, "git", "ls-remote", "--heads", "--tags", "--refs", "--quiet", remote, ref)
	cmd.Stdin = os.Stdin
	cmd.Stdout = b
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		return "", err
	}
	commitShaPattern := regexp.MustCompile(`^([0-9a-f]{40,})\b`)
	commitSha := commitShaPattern.FindString(b.String())
	return commitSha, nil
}

// getGitRepoCacheDir returns the global cache directory for git repositories
func getGitRepoCacheDir(_, name, version string) (globalDir, tempDir string, err error) {
	// Create hash of package name and version
	pkgh := sha256.Sum256([]byte(fmt.Sprintf("jsonnetpkg-%s-%s", strings.Replace(name, "/", "-", -1), strings.Replace(version, "/", "-", -1))))
	cacheKey := hex.EncodeToString(pkgh[:16])

	// Create a temporary directory for operations that still need one
	tempDir, err = os.MkdirTemp("", "jsonnet-bundler-*")
	if err != nil {
		return "", "", errors.Wrap(err, "failed to create temp dir")
	}

	// Global cache directory if enabled
	if GlobalCacheEnabled {
		globalCacheParent, err := getGlobalCacheDir()
		if err == nil {
			globalDir = filepath.Join(globalCacheParent, "repos", cacheKey)
			if err := os.MkdirAll(globalDir, os.ModePerm); err != nil {
				globalDir = "" // If we can't create the global dir, it will be left empty
			}
		}
	}

	return globalDir, tempDir, nil
}

func (p *GitPackage) Install(ctx context.Context, name, dir, version string) (string, error) {
	destPath := path.Join(dir, name)

	// Get cache directory and temporary directory
	globalCacheDir, tempDir, err := getGitRepoCacheDir(dir, name, version)
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tempDir) // Clean up temp directory when done

	// Try GitHub archive installation first
	if isGitHubRepository(p.Source.Remote()) {
		commitSha, err := p.installFromGitHubArchive(ctx, version, tempDir, destPath)
		if err == nil {
			return commitSha, nil
		}
		// Fall back to git clone on error
		if !GitQuiet {
			color.Yellow("archive install failed: %v", err)
			color.Yellow("falling back to git clone...")
		}
	}

	// Try to use global cache or fall back to git clone
	return p.installFromGit(ctx, version, globalCacheDir, tempDir, destPath)
}

// isGitHubRepository checks if the remote URL is a GitHub repository
func isGitHubRepository(remote string) bool {
	match, _ := regexp.MatchString(`^(https|ssh)://github\.com/.+$`, remote)
	return match
}

// installFromGitHubArchive attempts to install a package from a GitHub archive URL
func (p *GitPackage) installFromGitHubArchive(ctx context.Context, version, tempDir, destPath string) (string, error) {
	// Resolve version to commit SHA
	commitSha, err := p.resolveVersionToCommitSHA(ctx, version)
	if err != nil {
		return "", err
	}

	archiveUrl := fmt.Sprintf("%s/archive/%s.tar.gz", strings.TrimSuffix(p.Source.Remote(), ".git"), commitSha)
	archiveFilepath := filepath.Join(tempDir, commitSha+".tar.gz")

	// Use the caching helper for downloading/updating the archive
	err = ensureArchiveCache(archiveFilepath, archiveUrl)
	if err != nil {
		return "", err
	}

	return p.extractArchiveToDestination(archiveFilepath, destPath, commitSha)
}

// resolveVersionToCommitSHA resolves a version string to a commit SHA
func (p *GitPackage) resolveVersionToCommitSHA(ctx context.Context, version string) (string, error) {
	// Let git ls-remote decide if "version" is a ref or a commit SHA
	commitSha, _ := remoteResolveRef(ctx, p.Source.Remote(), version)

	// If the ref resolution failed and "version" looks like a SHA, assume it is one
	commitShaPattern := regexp.MustCompile("^([0-9a-f]{40,})$")
	if commitSha == "" && commitShaPattern.MatchString(version) {
		commitSha = version
	}

	if commitSha == "" {
		return "", fmt.Errorf("could not resolve version %s to commit SHA", version)
	}

	return commitSha, nil
}

// extractArchiveToDestination extracts a downloaded archive to the destination path
func (p *GitPackage) extractArchiveToDestination(archiveFilepath, destPath, commitSha string) (string, error) {
	// Get file info for debugging before opening
	info, err := os.Stat(archiveFilepath)
	if err != nil {
		return "", fmt.Errorf("failed to stat archive file before extraction: %w", err)
	}

	if !GitQuiet {
		color.Cyan("Extracting archive: %s (size: %d bytes)", archiveFilepath, info.Size())
	}

	// Open the archive file
	ar, err := os.Open(archiveFilepath)
	if err != nil {
		return "", fmt.Errorf("failed to open archive file: %w", err)
	}
	defer ar.Close()

	// Ensure the destination directory exists
	if err := os.MkdirAll(filepath.Dir(destPath), os.ModePerm); err != nil {
		return "", errors.Wrap(err, "failed to create destination directory")
	}

	// Extract the sub-directory (if any) from the archive to the final destination
	err = gzipUntar(destPath, ar, p.Source.Subdir)
	if err != nil {
		// Provide more context about the error
		if strings.Contains(err.Error(), "unexpected EOF") {
			// Re-validate the archive to get more details
			ar.Close()
			if validateErr := validateGzipFile(archiveFilepath); validateErr != nil {
				return "", fmt.Errorf("archive validation failed after extraction error - archive may be corrupted or truncated (size: %d bytes): %w", info.Size(), validateErr)
			}
			return "", fmt.Errorf("extraction failed with unexpected EOF - archive may be truncated (size: %d bytes): %w", info.Size(), err)
		}
		return "", fmt.Errorf("failed to extract archive %s (size: %d bytes): %w", archiveFilepath, info.Size(), err)
	}

	return commitSha, nil
}

// installFromGit installs a package using git clone/fetch operations
func (p *GitPackage) installFromGit(ctx context.Context, version, globalCacheDir, tempDir, destPath string) (string, error) {
	// We'll work in the temp directory if global cache is not available
	workDir := tempDir
	useGlobalCache := false
	var commitHash string

	// Try to use global cache first
	if globalCacheDir != "" {
		workDir, commitHash, useGlobalCache = p.tryUseGlobalCache(ctx, globalCacheDir, version)
	}

	// If global cache didn't work, clone directly to temp directory
	if !useGlobalCache {
		var err error
		commitHash, err = p.cloneToDirectory(ctx, tempDir, version)
		if err != nil {
			return "", err
		}
		workDir = tempDir
	}

	// Copy/move files to destination
	if err := p.copyToDestination(workDir, tempDir, destPath, useGlobalCache); err != nil {
		return "", err
	}

	return commitHash, nil
}

// gitCmd creates a git command with the proper configuration
func gitCmd(ctx context.Context, workingDir string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Stdin = os.Stdin
	if GitQuiet {
		cmd.Stdout = nil
		cmd.Stderr = nil
	} else {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	cmd.Dir = workingDir
	return cmd
}

// tryUseGlobalCache attempts to use the global cache for the repository
func (p *GitPackage) tryUseGlobalCache(ctx context.Context, globalCacheDir, version string) (workDir string, commitHash string, useCache bool) {
	// Check if global cache already contains this repository
	if _, err := os.Stat(globalCacheDir); err == nil {
		if !GitQuiet {
			color.Cyan("GLOBAL GIT CACHE HIT: %s", globalCacheDir)
		}

		// Try to checkout the requested version
		commitHash, err := p.checkoutVersionInCache(ctx, globalCacheDir, version)
		if err == nil {
			return globalCacheDir, commitHash, true
		}

		// Update the cache if checkout failed
		if !GitQuiet {
			color.Yellow("Version not found in global cache, updating: %s", version)
		}

		// Update and try again
		if err := p.updateGlobalCache(ctx, globalCacheDir); err == nil {
			commitHash, err := p.checkoutVersionInCache(ctx, globalCacheDir, version)
			if err == nil {
				return globalCacheDir, commitHash, true
			}
		}
	} else {
		// Initialize new global cache repository
		commitHash, err := p.initializeGlobalCache(ctx, globalCacheDir, version)
		if err == nil {
			return globalCacheDir, commitHash, true
		}
	}

	// Global cache failed
	return "", "", false
}

// checkoutVersionInCache checks out a specific version in the cache directory
func (p *GitPackage) checkoutVersionInCache(ctx context.Context, cacheDir, version string) (string, error) {
	cmd := gitCmd(ctx, cacheDir, "-c", "advice.detachedHead=false", "checkout", version)
	if err := cmd.Run(); err != nil {
		return "", err
	}

	// Get the commit hash
	return p.getCommitHash(ctx, cacheDir)
}

// getCommitHash retrieves the current commit hash
func (p *GitPackage) getCommitHash(ctx context.Context, dir string) (string, error) {
	b := bytes.NewBuffer(nil)
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
	cmd.Stdout = b
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(b.String()), nil
}

// updateGlobalCache updates the global cache with latest changes
func (p *GitPackage) updateGlobalCache(ctx context.Context, cacheDir string) error {
	cmd := gitCmd(ctx, cacheDir, "fetch", "--tags", "origin")
	return cmd.Run()
}

// initializeGlobalCache initializes a new global cache repository
func (p *GitPackage) initializeGlobalCache(ctx context.Context, cacheDir, version string) (string, error) {
	if !GitQuiet {
		color.Cyan("INITIALIZING GLOBAL GIT CACHE: %s", cacheDir)
	}

	// Initialize git repo
	cmd := gitCmd(ctx, cacheDir, "init")
	if err := cmd.Run(); err != nil {
		return "", err
	}

	// Add remote
	cmd = gitCmd(ctx, cacheDir, "remote", "add", "origin", p.Source.Remote())
	if err := cmd.Run(); err != nil {
		return "", err
	}

	// Try shallow fetch first
	cmd = gitCmd(ctx, cacheDir, "fetch", "--tags", "--depth", "1", "origin", version)
	if err := cmd.Run(); err != nil {
		// Fall back to full fetch
		cmd = gitCmd(ctx, cacheDir, "fetch", "origin")
		if err := cmd.Run(); err != nil {
			return "", err
		}
	}

	// Checkout version
	return p.checkoutVersionInCache(ctx, cacheDir, version)
}

// cloneToDirectory clones the repository to a specific directory
func (p *GitPackage) cloneToDirectory(ctx context.Context, dir, version string) (string, error) {
	if !GitQuiet {
		color.Cyan("CLONING TO TEMPORARY DIRECTORY: %s", dir)
	}

	// Initialize repository
	cmd := gitCmd(ctx, dir, "init")
	if err := cmd.Run(); err != nil {
		return "", err
	}

	// Add remote
	cmd = gitCmd(ctx, dir, "remote", "add", "origin", p.Source.Remote())
	if err := cmd.Run(); err != nil {
		return "", err
	}

	// Attempt shallow fetch at specific revision
	cmd = gitCmd(ctx, dir, "fetch", "--tags", "--depth", "1", "origin", version)
	if err := cmd.Run(); err != nil {
		// Fall back to normal fetch
		cmd = gitCmd(ctx, dir, "fetch", "origin")
		if err := cmd.Run(); err != nil {
			return "", err
		}
	}

	// Checkout the requested version
	cmd = gitCmd(ctx, dir, "-c", "advice.detachedHead=false", "checkout", version)
	if err := cmd.Run(); err != nil {
		return "", err
	}

	// Get commit hash
	commitHash, err := p.getCommitHash(ctx, dir)
	if err != nil {
		return "", err
	}

	// Apply sparse checkout if needed
	if p.Source.Subdir != "" {
		if err := p.applySparseCheckout(ctx, dir, version); err != nil {
			return "", err
		}
	}

	// Remove .git directory to save space
	if err := os.RemoveAll(path.Join(dir, ".git")); err != nil {
		return "", err
	}

	return commitHash, nil
}

// applySparseCheckout configures and applies sparse checkout
func (p *GitPackage) applySparseCheckout(ctx context.Context, dir, version string) error {
	cmd := gitCmd(ctx, dir, "config", "core.sparsecheckout", "true")
	if err := cmd.Run(); err != nil {
		return err
	}

	glob := []byte(p.Source.Subdir + "/*\n")
	if err := os.WriteFile(filepath.Join(dir, ".git", "info", "sparse-checkout"), glob, 0644); err != nil {
		return err
	}

	// Checkout again with sparse-checkout config
	cmd = gitCmd(ctx, dir, "-c", "advice.detachedHead=false", "checkout", version)
	return cmd.Run()
}

// copyToDestination copies files from source to destination
func (p *GitPackage) copyToDestination(workDir, tempDir, destPath string, useGlobalCache bool) error {
	// Prepare destination directory
	if err := os.MkdirAll(path.Dir(destPath), os.ModePerm); err != nil {
		return errors.Wrap(err, "failed to create parent path")
	}

	if err := os.RemoveAll(destPath); err != nil {
		return errors.Wrap(err, "failed to clean previous destination path")
	}

	if useGlobalCache {
		// Copy from global cache
		return p.copyFromGlobalCache(workDir, tempDir, destPath)
	}

	// Move directly from temp directory
	srcPath := path.Join(tempDir, p.Source.Subdir)
	if err := os.Rename(srcPath, destPath); err != nil {
		return errors.Wrap(err, "failed to move package")
	}

	return nil
}

// copyFromGlobalCache copies files from global cache to destination
func (p *GitPackage) copyFromGlobalCache(workDir, tempDir, destPath string) error {
	// Create a temp directory for transferring the code
	transferDir := filepath.Join(tempDir, "transfer")
	if err := os.MkdirAll(transferDir, os.ModePerm); err != nil {
		return errors.Wrap(err, "failed to create transfer directory")
	}

	srcPath := path.Join(workDir, p.Source.Subdir)

	// Copy files without .git directory
	if err := copyDirectory(srcPath, transferDir); err != nil {
		return errors.Wrap(err, "failed to copy files from global cache")
	}

	// Move from transfer directory to final destination
	if err := os.Rename(transferDir, destPath); err != nil {
		return errors.Wrap(err, "failed to move package")
	}

	return nil
}

// copyDirectory recursively copies a directory while skipping .git folders
func copyDirectory(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip .git directory
		if info.IsDir() && info.Name() == ".git" {
			return filepath.SkipDir
		}

		// Get relative path
		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		// Skip self
		if relPath == "." {
			return nil
		}

		destPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			// Create directory
			return os.MkdirAll(destPath, info.Mode())
		}

		// Copy file
		return copyFile(path, destPath)
	})
}

// copyFile copies a single file
func copyFile(src, dst string) error {
	// Create parent directory if needed
	if err := os.MkdirAll(filepath.Dir(dst), os.ModePerm); err != nil {
		return err
	}

	// Use the existing CopyFile utility function
	return CopyFile(src, dst)
}
