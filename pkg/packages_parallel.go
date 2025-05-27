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
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/fatih/color"
	"github.com/pkg/errors"

	"github.com/jsonnet-bundler/jsonnet-bundler/pkg/jsonnetfile"
	"github.com/jsonnet-bundler/jsonnet-bundler/spec/v1/deps"
)

// parallelEnsure is a parallel version of ensure that downloads multiple packages concurrently
func parallelEnsure(direct *deps.Ordered, vendorDir, pathToParentModule string, locks *deps.Ordered) (*deps.Ordered, error) {
	resultDeps := deps.NewOrdered()
	depsMutex := &sync.Mutex{}
	locksMutex := &sync.Mutex{}

	// Configure concurrency limits
	maxConcurrentDownloads := 10
	maxConcurrentNested := 5

	// Create error group for better error handling
	var firstErr error
	var errOnce sync.Once

	// Phase 1: Download all direct dependencies in parallel
	downloadSem := make(chan struct{}, maxConcurrentDownloads)
	var downloadWg sync.WaitGroup

	for _, k := range direct.Keys() {
		d, _ := direct.Get(k)
		l, present := locks.Get(d.Name())

		// Check if already locked and intact
		if present {
			d.Version = l.Version
			if check(l, vendorDir) {
				depsMutex.Lock()
				resultDeps.Set(d.Name(), l)
				depsMutex.Unlock()
				continue
			}
		}

		downloadWg.Add(1)
		go func(dep deps.Dependency, locked deps.Dependency, hasLock bool) {
			defer downloadWg.Done()

			downloadSem <- struct{}{}        // Acquire
			defer func() { <-downloadSem }() // Release

			// Store expected sum before download (same as ensure function)
			expectedSum := ""
			if hasLock {
				expectedSum = locked.Sum
			}

			// Remove existing directory
			dir := filepath.Join(vendorDir, dep.Name())
			os.RemoveAll(dir)

			// Download the package
			downloaded, err := download(dep, vendorDir, pathToParentModule)
			if err != nil {
				errOnce.Do(func() {
					firstErr = errors.Wrap(err, "downloading "+dep.Name())
				})
				return
			}

			// Check sum if expected
			if expectedSum != "" && downloaded.Sum != expectedSum {
				errOnce.Do(func() {
					firstErr = fmt.Errorf("checksum mismatch for %s. Expected %s but got %s", dep.Name(), expectedSum, downloaded.Sum)
				})
				return
			}

			// Update results
			depsMutex.Lock()
			resultDeps.Set(downloaded.Name(), *downloaded)
			depsMutex.Unlock()

			locksMutex.Lock()
			locks.Set(downloaded.Name(), *downloaded)
			locksMutex.Unlock()
		}(d, l, present)
	}

	downloadWg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}

	// Phase 2: Process nested dependencies concurrently
	type nestedTask struct {
		dep          deps.Dependency
		vendorPath   string
		absolutePath string
	}

	nestedChan := make(chan nestedTask, 100)
	nestedSem := make(chan struct{}, maxConcurrentNested)
	var nestedWg sync.WaitGroup

	// Worker pool for processing nested dependencies
	for range maxConcurrentNested {
		nestedWg.Add(1)
		go func() {
			defer nestedWg.Done()
			for task := range nestedChan {
				func() {
					nestedSem <- struct{}{}        // Acquire
					defer func() { <-nestedSem }() // Release

					f, err := jsonnetfile.Load(filepath.Join(task.vendorPath, jsonnetfile.File))
					if err != nil {
						if !os.IsNotExist(err) {
							errOnce.Do(func() {
								firstErr = err
							})
						}
						return
					}

					// Recursively process nested dependencies
					nested, err := parallelEnsure(f.Dependencies, vendorDir, task.absolutePath, locks)
					if err != nil {
						errOnce.Do(func() {
							firstErr = err
						})
						return
					}

					// Merge nested dependencies
					depsMutex.Lock()
					for _, k := range nested.Keys() {
						d, _ := nested.Get(k)
						if _, exists := resultDeps.Get(d.Name()); !exists {
							resultDeps.Set(d.Name(), d)
						}
					}
					depsMutex.Unlock()
				}()
			}
		}()
	}

	// Queue nested dependency tasks
	for _, k := range resultDeps.Keys() {
		d, _ := resultDeps.Get(k)
		if d.Single {
			continue // Skip dependencies that don't want nested ones
		}

		vendorPath := filepath.Join(vendorDir, d.Name())

		// Check if the path exists before evaluating symlinks
		absolutePath := vendorPath
		if _, err := os.Stat(vendorPath); err == nil {
			// Path exists, try to resolve symlinks
			resolvedPath, err := filepath.EvalSymlinks(vendorPath)
			if err == nil {
				absolutePath = resolvedPath
			}
			// If EvalSymlinks fails, just use the original path
		}

		nestedChan <- nestedTask{
			dep:          d,
			vendorPath:   vendorPath,
			absolutePath: absolutePath,
		}
	}

	close(nestedChan)
	nestedWg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}

	return resultDeps, nil
}

// EnsureParallel is the public interface to the parallel ensure function
func EnsureParallel(direct *deps.Ordered, vendorDir, pathToParentModule string, locks *deps.Ordered) (*deps.Ordered, error) {
	if !GitQuiet {
		color.Cyan("Using parallel package downloads...")
	}
	return parallelEnsure(direct, vendorDir, pathToParentModule, locks)
}
