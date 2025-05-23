//go:build ignore
// +build ignore

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/jsonnet-bundler/jsonnet-bundler/pkg/cache"
)

func main() {
	homeDir, _ := os.UserHomeDir()
	globalCacheDir := filepath.Join(homeDir, ".cache", "jb")

	// Print current state
	fmt.Println("Global cache directory:", globalCacheDir)

	// Try to read the index file directly
	indexPath := filepath.Join(globalCacheDir, cache.IndexFilename)
	data, err := os.ReadFile(indexPath)
	if err != nil {
		fmt.Printf("Error reading index file: %v\n", err)
		return
	}

	fmt.Println("Raw index file content:")
	fmt.Println(string(data))

	// Try to load index using the package
	location := cache.CacheLocation{
		Local: false,
		Path:  globalCacheDir,
	}

	index, err := cache.LoadOrCreateIndex(location)
	if err != nil {
		fmt.Printf("Error loading index: %v\n", err)
		return
	}

	fmt.Println("\nRemote caches via package:")
	for i, remote := range index.RemoteCaches {
		fmt.Printf("%d. %s\n", i+1, remote)
	}

	// Add a test remote
	testRemote := "s3://test-bucket-debug/"
	location.Remote = testRemote

	index, err = cache.LoadOrCreateIndex(location)
	if err != nil {
		fmt.Printf("Error loading index with remote: %v\n", err)
		return
	}

	fmt.Println("\nRemote caches after adding test remote:")
	for i, remote := range index.RemoteCaches {
		fmt.Printf("%d. %s\n", i+1, remote)
	}

	// Save the index
	fmt.Println("\nSaving index with test remote...")
	err = cache.SaveIndex(location, index)
	if err != nil {
		fmt.Printf("Error saving index: %v\n", err)
		return
	}

	// Reload and check again
	location.Remote = ""
	index, err = cache.LoadOrCreateIndex(location)
	if err != nil {
		fmt.Printf("Error reloading index: %v\n", err)
		return
	}

	fmt.Println("\nRemote caches after reload:")
	for i, remote := range index.RemoteCaches {
		fmt.Printf("%d. %s\n", i+1, remote)
	}
}
