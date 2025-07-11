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
	"sync"
	"testing"
	"time"

	"github.com/jsonnet-bundler/jsonnet-bundler/spec/v1/deps"
)

// BenchmarkSequentialVsParallelEnsure compares sequential vs parallel dependency processing
func BenchmarkSequentialVsParallelEnsure(b *testing.B) {
	// Save and restore original GitQuiet
	originalGitQuiet := GitQuiet
	GitQuiet = true
	defer func() { GitQuiet = originalGitQuiet }()

	// Create test dependencies
	createTestDeps := func(count int) *deps.Ordered {
		testDeps := deps.NewOrdered()
		for i := 0; i < count; i++ {
			depName := fmt.Sprintf("test-dep-%d", i)
			testDeps.Set(depName, deps.Dependency{
				Source: deps.Source{
					LocalSource: &deps.Local{
						Directory: fmt.Sprintf("testdata/dep-%d", i),
					},
				},
			})
		}
		return testDeps
	}

	benchmarks := []struct {
		name     string
		depCount int
	}{
		{"5deps", 5},
		{"10deps", 10},
		{"20deps", 20},
		{"50deps", 50},
	}

	for _, bm := range benchmarks {
		testDeps := createTestDeps(bm.depCount)
		locks := deps.NewOrdered()

		b.Run(fmt.Sprintf("Sequential_%s", bm.name), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				tempDir := b.TempDir()
				vendorDir := filepath.Join(tempDir, "vendor")
				os.MkdirAll(vendorDir, 0755)

				// Force sequential processing
				_, _ = ensure(testDeps, vendorDir, tempDir, locks)
			}
		})

		b.Run(fmt.Sprintf("Parallel_%s", bm.name), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				tempDir := b.TempDir()
				vendorDir := filepath.Join(tempDir, "vendor")
				os.MkdirAll(vendorDir, 0755)

				// Force parallel processing
				var locksSharedMutex sync.Mutex
				_, _ = parallelEnsure(testDeps, vendorDir, tempDir, locks, &locksSharedMutex)
			}
		})
	}
}

// BenchmarkWorkerPoolScaling tests worker pool performance with different concurrency levels
func BenchmarkWorkerPoolScaling(b *testing.B) {
	originalGitQuiet := GitQuiet
	GitQuiet = true
	defer func() { GitQuiet = originalGitQuiet }()

	// Create a realistic workload
	const numTasks = 100
	tasks := make([]downloadTask, numTasks)
	for i := 0; i < numTasks; i++ {
		tasks[i] = downloadTask{
			dep: deps.Dependency{
				Source: deps.Source{
					LocalSource: &deps.Local{
						Directory: fmt.Sprintf("testdata/task-%d", i),
					},
				},
			},
			vendorDir:  "/tmp/vendor",
			parentPath: "/tmp",
		}
	}

	workerCounts := []int{1, 2, 4, 8, 16, 32}

	for _, workers := range workerCounts {
		b.Run(fmt.Sprintf("Workers_%d", workers), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				ctx := context.Background()
				resultDeps := deps.NewOrdered()
				depsMutex := &sync.RWMutex{}
				locks := deps.NewOrdered()
				locksSharedMutex := &sync.Mutex{}

				// Simulate worker pool processing
				taskCh := make(chan downloadTask, len(tasks))
				for _, task := range tasks {
					taskCh <- task
				}
				close(taskCh)

				var wg sync.WaitGroup
				for w := 0; w < workers; w++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						for task := range taskCh {
							// Simulate work (this will fail quickly, which is what we want for benchmarking)
							_ = processDownloadTask(ctx, task, resultDeps, depsMutex, locks, locksSharedMutex)
						}
					}()
				}
				wg.Wait()
			}
		})
	}
}

// BenchmarkConcurrentDataStructureAccess measures performance of concurrent access patterns
func BenchmarkConcurrentDataStructureAccess(b *testing.B) {
	benchmarks := []struct {
		name       string
		readers    int
		writers    int
		operations int
	}{
		{"LowContention_1r1w", 1, 1, 1000},
		{"ReadHeavy_10r2w", 10, 2, 1000},
		{"WriteHeavy_2r10w", 2, 10, 1000},
		{"HighContention_10r10w", 10, 10, 1000},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				resultDeps := deps.NewOrdered()
				depsMutex := &sync.RWMutex{}
				var wg sync.WaitGroup

				// Start readers
				for r := 0; r < bm.readers; r++ {
					wg.Add(1)
					go func(readerID int) {
						defer wg.Done()
						for op := 0; op < bm.operations; op++ {
							depsMutex.RLock()
							_ = resultDeps.Keys()
							depsMutex.RUnlock()
						}
					}(r)
				}

				// Start writers
				for w := 0; w < bm.writers; w++ {
					wg.Add(1)
					go func(writerID int) {
						defer wg.Done()
						for op := 0; op < bm.operations; op++ {
							depName := fmt.Sprintf("dep-%d-%d", writerID, op)
							dep := deps.Dependency{
								Source: deps.Source{
									LocalSource: &deps.Local{
										Directory: "test",
									},
								},
							}
							depsMutex.Lock()
							resultDeps.Set(depName, dep)
							depsMutex.Unlock()
						}
					}(w)
				}

				wg.Wait()
			}
		})
	}
}

// BenchmarkContextCancellation measures the overhead of context cancellation checks
func BenchmarkContextCancellation(b *testing.B) {
	benchmarks := []struct {
		name           string
		withContext    bool
		cancelAfter    time.Duration
		operationCount int
	}{
		{"NoContext", false, 0, 10000},
		{"WithContext_NeverCancel", true, time.Hour, 10000},
		{"WithContext_CancelMidway", true, 50 * time.Millisecond, 10000},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				var ctx context.Context
				var cancel context.CancelFunc

				if bm.withContext {
					ctx, cancel = context.WithTimeout(context.Background(), bm.cancelAfter)
					defer cancel()
				} else {
					ctx = context.Background()
				}

				// Simulate operations with context checks
				for op := 0; op < bm.operationCount; op++ {
					select {
					case <-ctx.Done():
						// Context cancelled
						return
					default:
						// Simulate some work
						_ = fmt.Sprintf("operation-%d", op)
					}
				}
			}
		})
	}
}

// BenchmarkParallelEnsureMemoryAllocation measures memory allocation patterns
func BenchmarkParallelEnsureMemoryAllocation(b *testing.B) {
	originalGitQuiet := GitQuiet
	GitQuiet = true
	defer func() { GitQuiet = originalGitQuiet }()

	// Create test dependencies
	testDeps := deps.NewOrdered()
	for i := 0; i < 20; i++ {
		depName := fmt.Sprintf("test-dep-%d", i)
		testDeps.Set(depName, deps.Dependency{
			Source: deps.Source{
				LocalSource: &deps.Local{
					Directory: fmt.Sprintf("testdata/dep-%d", i),
				},
			},
		})
	}

	locks := deps.NewOrdered()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		tempDir := b.TempDir()
		vendorDir := filepath.Join(tempDir, "vendor")
		os.MkdirAll(vendorDir, 0755)

		var locksSharedMutex sync.Mutex
		_, _ = parallelEnsure(testDeps, vendorDir, tempDir, locks, &locksSharedMutex)
	}
}

// BenchmarkTaskChannelThroughput measures channel throughput for task distribution
func BenchmarkTaskChannelThroughput(b *testing.B) {
	const numTasks = 10000

	benchmarks := []struct {
		name       string
		bufferSize int
		workers    int
	}{
		{"Unbuffered_1worker", 0, 1},
		{"Unbuffered_4workers", 0, 4},
		{"Buffered100_1worker", 100, 1},
		{"Buffered100_4workers", 100, 4},
		{"Buffered1000_4workers", 1000, 4},
		{"BufferedAll_4workers", numTasks, 4},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				taskCh := make(chan int, bm.bufferSize)
				var wg sync.WaitGroup

				// Start workers
				for w := 0; w < bm.workers; w++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						for task := range taskCh {
							// Simulate minimal work
							_ = task * 2
						}
					}()
				}

				// Send tasks
				go func() {
					for t := 0; t < numTasks; t++ {
						taskCh <- t
					}
					close(taskCh)
				}()

				wg.Wait()
			}
		})
	}
}

// BenchmarkErrorHandlingPatterns compares different error handling approaches
func BenchmarkErrorHandlingPatterns(b *testing.B) {
	const numOperations = 1000

	b.Run("ErrorChannel", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			errorCh := make(chan error, numOperations)
			var wg sync.WaitGroup

			for op := 0; op < numOperations; op++ {
				wg.Add(1)
				go func(opID int) {
					defer wg.Done()
					if opID%100 == 0 {
						errorCh <- fmt.Errorf("error %d", opID)
					}
				}(op)
			}

			go func() {
				wg.Wait()
				close(errorCh)
			}()

			// Collect errors
			for err := range errorCh {
				if err != nil {
					break // Just break on first error for benchmarking
				}
			}
		}
	})

	b.Run("SyncOnce", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			var errOnce sync.Once
			var wg sync.WaitGroup

			for op := 0; op < numOperations; op++ {
				wg.Add(1)
				go func(opID int) {
					defer wg.Done()
					if opID%100 == 0 {
						errOnce.Do(func() {
							_ = fmt.Errorf("error %d", opID)
						})
					}
				}(op)
			}

			wg.Wait()
		}
	})

	b.Run("MutexProtected", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			var mu sync.Mutex
			var wg sync.WaitGroup

			for op := 0; op < numOperations; op++ {
				wg.Add(1)
				go func(opID int) {
					defer wg.Done()
					if opID%100 == 0 {
						mu.Lock()
						_ = fmt.Errorf("error %d", opID)
						mu.Unlock()
					}
				}(op)
			}

			wg.Wait()
		}
	})
}