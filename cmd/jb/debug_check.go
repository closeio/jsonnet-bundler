//go:build ignore
// +build ignore

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jsonnet-bundler/jsonnet-bundler/pkg/cache"
	"github.com/jsonnet-bundler/jsonnet-bundler/pkg/s3"
)

func main() {
	// Target remote cache URL
	targetURL := "s3://jsonnet-bundler/"

	// Get cache directories
	homeDir, _ := os.UserHomeDir()
	workDir, _ := os.Getwd()
	globalCacheDir := filepath.Join(homeDir, ".cache", "jb")
	localCacheDir := filepath.Join(workDir, "vendor", "cache")

	fmt.Println("Checking global cache...")
	checkLocation(globalCacheDir, false, targetURL)

	fmt.Println("\nChecking local cache...")
	checkLocation(localCacheDir, true, targetURL)
}

func checkLocation(cacheDir string, isLocal bool, targetURL string) {
	// Create location without target URL
	locationNoURL := cache.CacheLocation{
		Local: isLocal,
		Path:  cacheDir,
	}

	// Also test location with URL (will use later)
	_ = cache.CacheLocation{
		Local:  isLocal,
		Path:   cacheDir,
		Remote: targetURL,
	}

	// Check if cacheDir exists
	_, err := os.Stat(cacheDir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("Cache directory does not exist: %s\n", cacheDir)
		} else {
			fmt.Printf("Error checking cache directory: %v\n", err)
		}
		return
	}

	// Get index without URL
	index, err := cache.LoadOrCreateIndex(locationNoURL)
	if err != nil {
		fmt.Printf("Error loading index: %v\n", err)
		return
	}

	fmt.Printf("Remote caches in index: %d\n", len(index.RemoteCaches))
	for i, remote := range index.RemoteCaches {
		fmt.Printf("  %d. %s\n", i+1, remote)
	}

	// Check if URL is in the list
	found := false
	for _, remote := range index.RemoteCaches {
		if remote == targetURL {
			found = true
			break
		}
	}

	fmt.Printf("Is target URL in list? %t\n", found)

	// Validation when using 's3://' URLs
	if strings.HasPrefix(targetURL, "s3://") {
		// Test parsing the URL
		_, err := s3.NewClientFromURL(targetURL)
		if err != nil {
			fmt.Printf("Error parsing S3 URL: %v\n", err)
		} else {
			fmt.Println("S3 URL parsed successfully")
		}
	}
}
