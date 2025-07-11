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
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// BenchmarkParallelCacheCheck compares sequential vs parallel cache checking
func BenchmarkParallelCacheCheck(b *testing.B) {
	// Save and restore original GitQuiet
	originalGitQuiet := GitQuiet
	GitQuiet = true
	defer func() { GitQuiet = originalGitQuiet }()

	// Create test servers with different response delays
	createTestServer := func(delay time.Duration, shouldSucceed bool) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(delay)
			if shouldSucceed && strings.HasSuffix(r.URL.Path, ".tar.gz") {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("test content"))
			} else {
				w.WriteHeader(http.StatusNotFound)
			}
		}))
	}

	benchmarks := []struct {
		name        string
		serverCount int
		delay       time.Duration
		successRate float64
	}{
		{"5servers_fast", 5, 1 * time.Millisecond, 0.2},
		{"10servers_medium", 10, 10 * time.Millisecond, 0.3},
		{"20servers_slow", 20, 50 * time.Millisecond, 0.1},
	}

	for _, bm := range benchmarks {
		// Create test servers
		var servers []*httptest.Server
		var urls []string
		
		for i := 0; i < bm.serverCount; i++ {
			shouldSucceed := float64(i)/float64(bm.serverCount) < bm.successRate
			server := createTestServer(bm.delay, shouldSucceed)
			servers = append(servers, server)
			urls = append(urls, server.URL)
		}

		b.Run(fmt.Sprintf("Sequential_%s", bm.name), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				// Sequential cache checking simulation
				for _, url := range urls {
					ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
					_ = fetchFromRemoteCache(ctx, url, "test-key")
					cancel()
				}
			}
		})

		b.Run(fmt.Sprintf("Parallel_%s", bm.name), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				_, _ = parallelCheckRemoteCachesWithTimeout(urls, "test-key", 200*time.Millisecond)
			}
		})

		// Cleanup servers
		for _, server := range servers {
			server.Close()
		}
	}
}

// BenchmarkCacheResultHandling measures performance of different result handling patterns
func BenchmarkCacheResultHandling(b *testing.B) {
	originalGitQuiet := GitQuiet
	GitQuiet = true
	defer func() { GitQuiet = originalGitQuiet }()

	const numResults = 100

	b.Run("ChannelBased", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			ctx := context.Background()
			resultChan := make(chan cacheResult, numResults)

			// Generate results
			go func() {
				for r := 0; r < numResults; r++ {
					result := cacheResult{
						success:  r == numResults/2, // Only one success
						tempPath: fmt.Sprintf("/tmp/file-%d", r),
						cacheURL: fmt.Sprintf("http://cache-%d", r),
					}
					resultChan <- result
				}
				close(resultChan)
			}()

			_, _ = handleCacheResults(ctx, resultChan)
		}
	})

	b.Run("WaitGroupBased", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			var wg sync.WaitGroup
			var mu sync.Mutex
			var firstSuccess *cacheResult

			for r := 0; r < numResults; r++ {
				wg.Add(1)
				go func(resultID int) {
					defer wg.Done()
					result := cacheResult{
						success:  resultID == numResults/2,
						tempPath: fmt.Sprintf("/tmp/file-%d", resultID),
						cacheURL: fmt.Sprintf("http://cache-%d", resultID),
					}

					if result.success {
						mu.Lock()
						if firstSuccess == nil {
							firstSuccess = &result
						}
						mu.Unlock()
					}
				}(r)
			}

			wg.Wait()
		}
	})
}

// BenchmarkContextOperations measures the overhead of context operations
func BenchmarkContextOperations(b *testing.B) {
	benchmarks := []struct {
		name            string
		timeout         time.Duration
		operationsCount int
	}{
		{"ShortTimeout_1ms", 1 * time.Millisecond, 1000},
		{"MediumTimeout_100ms", 100 * time.Millisecond, 1000},
		{"LongTimeout_1s", 1 * time.Second, 1000},
		{"NoTimeout", 0, 1000},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				var ctx context.Context
				var cancel context.CancelFunc

				if bm.timeout > 0 {
					ctx, cancel = context.WithTimeout(context.Background(), bm.timeout)
				} else {
					ctx, cancel = context.WithCancel(context.Background())
				}

				for op := 0; op < bm.operationsCount; op++ {
					select {
					case <-ctx.Done():
						cancel()
						return
					default:
						// Simulate work
						_ = fmt.Sprintf("operation-%d", op)
					}
				}
				cancel()
			}
		})
	}
}

// BenchmarkHTTPClientPerformance compares different HTTP client configurations
func BenchmarkHTTPClientPerformance(b *testing.B) {
	originalGitQuiet := GitQuiet
	GitQuiet = true
	defer func() { GitQuiet = originalGitQuiet }()

	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("test content"))
	}))
	defer server.Close()

	b.Run("DefaultClient", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			resp, err := http.Get(server.URL + "/test.tar.gz")
			if err == nil {
				resp.Body.Close()
			}
		}
	})

	b.Run("CustomClient_5s", func(b *testing.B) {
		client := &http.Client{
			Timeout: 5 * time.Second,
		}
		for i := 0; i < b.N; i++ {
			resp, err := client.Get(server.URL + "/test.tar.gz")
			if err == nil {
				resp.Body.Close()
			}
		}
	})

	b.Run("CustomClient_100ms", func(b *testing.B) {
		client := &http.Client{
			Timeout: 100 * time.Millisecond,
		}
		for i := 0; i < b.N; i++ {
			resp, err := client.Get(server.URL + "/test.tar.gz")
			if err == nil {
				resp.Body.Close()
			}
		}
	})

	b.Run("ContextAware", func(b *testing.B) {
		client := &http.Client{
			Timeout: 30 * time.Second,
		}
		for i := 0; i < b.N; i++ {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/test.tar.gz", nil)
			resp, err := client.Do(req)
			if err == nil {
				resp.Body.Close()
			}
			cancel()
		}
	})
}

// BenchmarkS3Operations simulates S3 operation patterns
func BenchmarkS3Operations(b *testing.B) {
	originalGitQuiet := GitQuiet
	GitQuiet = true
	defer func() { GitQuiet = originalGitQuiet }()

	benchmarks := []struct {
		name         string
		cacheCount   int
		parallelism  int
		simulateWork time.Duration
	}{
		{"5caches_seq", 5, 1, 1 * time.Millisecond},
		{"5caches_par", 5, 5, 1 * time.Millisecond},
		{"10caches_par", 10, 5, 1 * time.Millisecond},
		{"20caches_par", 20, 10, 1 * time.Millisecond},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				// Simulate S3 upload operations
				ctx := context.Background()
				var wg sync.WaitGroup

				cacheCh := make(chan string, bm.cacheCount)
				for c := 0; c < bm.cacheCount; c++ {
					cacheCh <- fmt.Sprintf("s3://bucket-%d/cache", c)
				}
				close(cacheCh)

				// Start workers
				for w := 0; w < bm.parallelism; w++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						for cacheURL := range cacheCh {
							// Simulate S3 upload work
							time.Sleep(bm.simulateWork)
							uploadToS3Cache(ctx, cacheURL, "/tmp/test.tar.gz", "test-key.tar.gz")
						}
					}()
				}

				wg.Wait()
			}
		})
	}
}

// BenchmarkMemoryAllocation measures memory allocation patterns in parallel operations
func BenchmarkMemoryAllocation(b *testing.B) {
	originalGitQuiet := GitQuiet
	GitQuiet = true
	defer func() { GitQuiet = originalGitQuiet }()

	// Create mock servers
	const serverCount = 10
	var servers []*httptest.Server
	var urls []string

	for i := 0; i < serverCount; i++ {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, ".tar.gz") {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("test content"))
			} else {
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		servers = append(servers, server)
		urls = append(urls, server.URL)
	}

	defer func() {
		for _, server := range servers {
			server.Close()
		}
	}()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _ = parallelCheckRemoteCachesWithTimeout(urls, "test-key", 1*time.Second)
	}
}

// BenchmarkConcurrentChannelOperations measures channel operation performance
func BenchmarkConcurrentChannelOperations(b *testing.B) {
	benchmarks := []struct {
		name       string
		producers  int
		consumers  int
		bufferSize int
		itemCount  int
	}{
		{"1p1c_unbuf", 1, 1, 0, 1000},
		{"1p1c_buf100", 1, 1, 100, 1000},
		{"5p5c_unbuf", 5, 5, 0, 1000},
		{"5p5c_buf100", 5, 5, 100, 1000},
		{"10p2c_buf1000", 10, 2, 1000, 1000},
		{"2p10c_buf1000", 2, 10, 1000, 1000},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				ch := make(chan cacheResult, bm.bufferSize)
				var wg sync.WaitGroup

				// Start consumers
				for c := 0; c < bm.consumers; c++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						for result := range ch {
							// Simulate processing
							_ = result.success
						}
					}()
				}

				// Start producers
				var producerWg sync.WaitGroup
				itemsPerProducer := bm.itemCount / bm.producers
				for p := 0; p < bm.producers; p++ {
					producerWg.Add(1)
					go func(producerID int) {
						defer producerWg.Done()
						for item := 0; item < itemsPerProducer; item++ {
							result := cacheResult{
								success:  item%10 == 0,
								tempPath: fmt.Sprintf("/tmp/file-%d-%d", producerID, item),
								cacheURL: fmt.Sprintf("http://cache-%d", item),
							}
							ch <- result
						}
					}(p)
				}

				// Close channel when all producers are done
				go func() {
					producerWg.Wait()
					close(ch)
				}()

				wg.Wait()
			}
		})
	}
}

// BenchmarkErrorPropagation measures different error propagation patterns
func BenchmarkErrorPropagation(b *testing.B) {
	const goroutineCount = 100
	const errorRate = 0.1 // 10% of operations fail

	b.Run("ErrorChannel", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			errorCh := make(chan error, goroutineCount)
			var wg sync.WaitGroup

			for g := 0; g < goroutineCount; g++ {
				wg.Add(1)
				go func(id int) {
					defer wg.Done()
					if float64(id)/float64(goroutineCount) < errorRate {
						errorCh <- fmt.Errorf("error from goroutine %d", id)
					}
				}(g)
			}

			go func() {
				wg.Wait()
				close(errorCh)
			}()

			// Collect first error
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

			for g := 0; g < goroutineCount; g++ {
				wg.Add(1)
				go func(id int) {
					defer wg.Done()
					if float64(id)/float64(goroutineCount) < errorRate {
						errOnce.Do(func() {
							_ = fmt.Errorf("error from goroutine %d", id)
						})
					}
				}(g)
			}

			wg.Wait()
		}
	})

	b.Run("ContextCancellation", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			ctx, cancel := context.WithCancel(context.Background())
			var wg sync.WaitGroup

			for g := 0; g < goroutineCount; g++ {
				wg.Add(1)
				go func(id int) {
					defer wg.Done()
					select {
					case <-ctx.Done():
						return
					default:
						if float64(id)/float64(goroutineCount) < errorRate {
							cancel() // Cancel on first error
						}
					}
				}(g)
			}

			wg.Wait()
			cancel()
		}
	})
}