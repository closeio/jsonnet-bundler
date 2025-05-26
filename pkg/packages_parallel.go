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

	// First, identify packages that need to be downloaded
	type downloadTask struct {
		key           string
		dep           deps.Dependency
		locked        deps.Dependency
		hasLock       bool
		needsDownload bool
	}

	var tasks []downloadTask
	var mu sync.Mutex

	// Prepare download tasks
	for _, k := range direct.Keys() {
		d, _ := direct.Get(k)
		l, present := locks.Get(d.Name())

		task := downloadTask{
			key:           k,
			dep:           d,
			locked:        l,
			hasLock:       present,
			needsDownload: true,
		}

		// already locked and the integrity is intact
		if present {
			d.Version = l.Version

			if check(l, vendorDir) {
				task.needsDownload = false
				resultDeps.Set(d.Name(), l)
			}
		}

		tasks = append(tasks, task)
	}

	// Download packages in parallel
	type downloadResult struct {
		key        string
		dependency *deps.Dependency
		err        error
	}

	resultChan := make(chan downloadResult, len(tasks))
	var wg sync.WaitGroup

	// Limit concurrent downloads to avoid overwhelming the system
	semaphore := make(chan struct{}, 5) // Max 5 concurrent downloads

	for _, task := range tasks {
		if !task.needsDownload {
			continue
		}

		wg.Add(1)
		go func(t downloadTask) {
			defer wg.Done()

			semaphore <- struct{}{}        // Acquire
			defer func() { <-semaphore }() // Release

			// Remove existing directory
			dir := filepath.Join(vendorDir, t.dep.Name())
			os.RemoveAll(dir)

			// Download the package
			locked, err := download(t.dep, vendorDir, pathToParentModule)
			if err != nil {
				resultChan <- downloadResult{key: t.key, dependency: nil, err: errors.Wrap(err, "downloading")}
				return
			}

			// Check sum if expected
			if t.hasLock && t.locked.Sum != "" && locked.Sum != t.locked.Sum {
				resultChan <- downloadResult{
					key:        t.key,
					dependency: nil,
					err:        fmt.Errorf("checksum mismatch for %s. Expected %s but got %s", t.dep.Name(), t.locked.Sum, locked.Sum),
				}
				return
			}

			resultChan <- downloadResult{key: t.key, dependency: locked, err: nil}
		}(task)
	}

	// Close result channel when all downloads are done
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Collect results
	for result := range resultChan {
		if result.err != nil {
			return nil, result.err
		}

		if result.dependency != nil {
			mu.Lock()
			resultDeps.Set(result.dependency.Name(), *result.dependency)
			// we settled on a new version, add it to the locks for recursion
			locks.Set(result.dependency.Name(), *result.dependency)
			mu.Unlock()
		}
	}

	// Process nested dependencies (still sequential for now, could be parallelized too)
	for _, k := range resultDeps.Keys() {
		d, _ := resultDeps.Get(k)
		if d.Single {
			// skip dependencies that explicitly don't want nested ones installed
			continue
		}

		f, err := jsonnetfile.Load(filepath.Join(vendorDir, d.Name(), jsonnetfile.File))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}

		absolutePath, err := filepath.EvalSymlinks(filepath.Join(vendorDir, d.Name()))
		if err != nil {
			return nil, err
		}

		// Recursively process nested dependencies (could use parallelEnsure here too)
		nested, err := parallelEnsure(f.Dependencies, vendorDir, absolutePath, locks)
		if err != nil {
			return nil, err
		}

		for _, k := range nested.Keys() {
			d, _ := nested.Get(k)
			if _, ok := resultDeps.Get(d.Name()); !ok {
				resultDeps.Set(d.Name(), d)
			}
		}
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
