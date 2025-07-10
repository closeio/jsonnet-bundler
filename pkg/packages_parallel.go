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
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/fatih/color"
	"github.com/pkg/errors"

	"github.com/jsonnet-bundler/jsonnet-bundler/pkg/jsonnetfile"
	"github.com/jsonnet-bundler/jsonnet-bundler/spec/v1/deps"
)

// parallelEnsure is a parallel version of ensure that downloads multiple packages concurrently
func parallelEnsure(direct *deps.Ordered, vendorDir, pathToParentModule string, locks *deps.Ordered, locksSharedMutex *sync.Mutex) (*deps.Ordered, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	return parallelEnsureWithContext(ctx, direct, vendorDir, pathToParentModule, locks, locksSharedMutex)
}

// parallelEnsureWithContext implements the parallel ensure logic with context support
func parallelEnsureWithContext(ctx context.Context, direct *deps.Ordered, vendorDir, pathToParentModule string, locks *deps.Ordered, locksSharedMutex *sync.Mutex) (*deps.Ordered, error) {
	resultDeps := deps.NewOrdered()
	depsMutex := &sync.RWMutex{}

	// Configure concurrency limits based on available CPU cores
	numCPU := runtime.NumCPU()
	maxConcurrentDownloads := min(numCPU*2, 16) // Cap at 16 to avoid overwhelming network
	maxConcurrentNested := min(numCPU, 8)       // Limit nested processing

	// Create error collection with context cancellation
	errorCh := make(chan error, maxConcurrentDownloads+maxConcurrentNested)
	var firstErr error
	var errOnce sync.Once

	// Phase 1: Download all direct dependencies in parallel
	var downloadWg sync.WaitGroup

	// Pre-check which dependencies need downloading
	toDownload := make([]downloadTask, 0, len(direct.Keys()))
	for _, k := range direct.Keys() {
		d, _ := direct.Get(k)
		l, present := locks.Get(d.Name())

		// Check if already locked and intact
		if present {
			depCopy := d
			depCopy.Version = l.Version
			if check(l, vendorDir) {
				depsMutex.Lock()
				resultDeps.Set(depCopy.Name(), l)
				depsMutex.Unlock()
				continue
			}
			d = depCopy
		}

		toDownload = append(toDownload, downloadTask{
			dep:         d,
			locked:      l,
			hasLock:     present,
			vendorDir:   vendorDir,
			parentPath:  pathToParentModule,
		})
	}

	// Process downloads with worker pool
	downloadCh := make(chan downloadTask, len(toDownload))
	for _, task := range toDownload {
		downloadCh <- task
	}
	close(downloadCh)

	// Start download workers
	for i := 0; i < maxConcurrentDownloads; i++ {
		downloadWg.Add(1)
		go func() {
			defer downloadWg.Done()
			for {
				select {
				case <-ctx.Done():
					errorCh <- ctx.Err()
					return
				case task, ok := <-downloadCh:
					if !ok {
						return
					}
					if err := processDownloadTask(ctx, task, resultDeps, depsMutex, locks, locksSharedMutex); err != nil {
						errOnce.Do(func() {
							firstErr = err
						})
						return
					}
				}
			}
		}()
	}

	downloadWg.Wait()

	// Check for context cancellation or errors
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	if firstErr != nil {
		return nil, firstErr
	}

	// Phase 2: Process nested dependencies concurrently
	nestedTasks := collectNestedTasks(ctx, resultDeps, depsMutex, vendorDir)
	if len(nestedTasks) > 0 {
		if err := processNestedDependencies(ctx, nestedTasks, maxConcurrentNested, vendorDir, locks, locksSharedMutex, resultDeps, depsMutex); err != nil {
			return nil, err
		}
	}

	return resultDeps, nil
}

// downloadTask represents a package download operation
type downloadTask struct {
	dep        deps.Dependency
	locked     deps.Dependency
	hasLock    bool
	vendorDir  string
	parentPath string
}

// nestedTask represents a nested dependency processing operation
type nestedTask struct {
	dep          deps.Dependency
	vendorPath   string
	absolutePath string
}

// processDownloadTask handles a single download task with proper error handling
func processDownloadTask(ctx context.Context, task downloadTask, resultDeps *deps.Ordered, depsMutex *sync.RWMutex, locks *deps.Ordered, locksSharedMutex *sync.Mutex) error {
	// Check for context cancellation
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// Store expected sum before download
	expectedSum := ""
	if task.hasLock {
		expectedSum = task.locked.Sum
	}

	// Remove existing directory
	dir := filepath.Join(task.vendorDir, task.dep.Name())
	os.RemoveAll(dir)

	// Download the package
	downloaded, err := download(task.dep, task.vendorDir, task.parentPath)
	if err != nil {
		return errors.Wrapf(err, "downloading %s", task.dep.Name())
	}

	// Check sum if expected
	if expectedSum != "" && downloaded.Sum != expectedSum {
		return fmt.Errorf("checksum mismatch for %s. Expected %s but got %s", task.dep.Name(), expectedSum, downloaded.Sum)
	}

	// Update results with proper locking
	depsMutex.Lock()
	resultDeps.Set(downloaded.Name(), *downloaded)
	depsMutex.Unlock()

	locksSharedMutex.Lock()
	locks.Set(downloaded.Name(), *downloaded)
	locksSharedMutex.Unlock()

	return nil
}

// collectNestedTasks gathers all nested dependency tasks that need processing
func collectNestedTasks(ctx context.Context, resultDeps *deps.Ordered, depsMutex *sync.RWMutex, vendorDir string) []nestedTask {
	depsMutex.RLock()
	keys := resultDeps.Keys()
	depsMutex.RUnlock()

	tasks := make([]nestedTask, 0, len(keys))
	for _, k := range keys {
		select {
		case <-ctx.Done():
			return tasks
		default:
		}

		depsMutex.RLock()
		d, exists := resultDeps.Get(k)
		depsMutex.RUnlock()

		if !exists || d.Single {
			continue // Skip dependencies that don't want nested ones
		}

		vendorPath := filepath.Join(vendorDir, d.Name())
		absolutePath := resolveAbsolutePath(vendorPath)

		tasks = append(tasks, nestedTask{
			dep:          d,
			vendorPath:   vendorPath,
			absolutePath: absolutePath,
		})
	}
	return tasks
}

// resolveAbsolutePath safely resolves symlinks or returns the original path
func resolveAbsolutePath(vendorPath string) string {
	if _, err := os.Stat(vendorPath); err == nil {
		if resolvedPath, err := filepath.EvalSymlinks(vendorPath); err == nil {
			return resolvedPath
		}
	}
	return vendorPath
}

// processNestedDependencies handles nested dependency processing with worker pools
func processNestedDependencies(ctx context.Context, tasks []nestedTask, maxWorkers int, vendorDir string, locks *deps.Ordered, locksSharedMutex *sync.Mutex, resultDeps *deps.Ordered, depsMutex *sync.RWMutex) error {
	taskCh := make(chan nestedTask, len(tasks))
	for _, task := range tasks {
		taskCh <- task
	}
	close(taskCh)

	var wg sync.WaitGroup
	errorCh := make(chan error, maxWorkers)

	// Start worker goroutines
	for i := 0; i < maxWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					errorCh <- ctx.Err()
					return
				case task, ok := <-taskCh:
					if !ok {
						return
					}
					if err := processNestedTask(ctx, task, vendorDir, locks, locksSharedMutex, resultDeps, depsMutex); err != nil {
						errorCh <- err
						return
					}
				}
			}
		}()
	}

	// Wait for completion
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case err := <-errorCh:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// processNestedTask handles a single nested dependency task
func processNestedTask(ctx context.Context, task nestedTask, vendorDir string, locks *deps.Ordered, locksSharedMutex *sync.Mutex, resultDeps *deps.Ordered, depsMutex *sync.RWMutex) error {
	f, err := jsonnetfile.Load(filepath.Join(task.vendorPath, jsonnetfile.File))
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No nested dependencies, skip silently
		}
		return errors.Wrapf(err, "loading jsonnetfile for %s", task.dep.Name())
	}

	// Recursively process nested dependencies
	nested, err := parallelEnsureWithContext(ctx, f.Dependencies, vendorDir, task.absolutePath, locks, locksSharedMutex)
	if err != nil {
		return errors.Wrapf(err, "processing nested dependencies for %s", task.dep.Name())
	}

	// Merge nested dependencies
	depsMutex.Lock()
	defer depsMutex.Unlock()
	for _, k := range nested.Keys() {
		d, _ := nested.Get(k)
		if _, exists := resultDeps.Get(d.Name()); !exists {
			resultDeps.Set(d.Name(), d)
		}
	}

	return nil
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// EnsureParallel is the public interface to the parallel ensure function
func EnsureParallel(direct *deps.Ordered, vendorDir, pathToParentModule string, locks *deps.Ordered) (*deps.Ordered, error) {
	if !GetGitQuiet() {
		color.Cyan("Using parallel package downloads...")
	}
	var locksSharedMutex sync.Mutex
	return parallelEnsure(direct, vendorDir, pathToParentModule, locks, &locksSharedMutex)
}
