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
	"testing"
	"time"

	v1 "github.com/jsonnet-bundler/jsonnet-bundler/spec/v1"
	"github.com/jsonnet-bundler/jsonnet-bundler/spec/v1/deps"
)

// BenchmarkShouldUseParallelDownloads tests the performance of the decision logic
func BenchmarkShouldUseParallelDownloads(b *testing.B) {
	testCases := []struct {
		name     string
		depCount int
	}{
		{"1dep", 1},
		{"3deps", 3},
		{"5deps", 5},
		{"10deps", 10},
		{"50deps", 50},
		{"100deps", 100},
	}

	for _, tc := range testCases {
		dependencies := deps.NewOrdered()
		for i := 0; i < tc.depCount; i++ {
			depName := fmt.Sprintf("dep-%d", i)
			dependencies.Set(depName, deps.Dependency{
				Source: deps.Source{
					LocalSource: &deps.Local{
						Directory: fmt.Sprintf("testdata/%s", depName),
					},
				},
			})
		}

		b.Run(tc.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				_ = shouldUseParallelDownloads(dependencies)
			}
		})
	}
}

// BenchmarkEnsureVsEnsureWithContext compares performance of context-aware operations
func BenchmarkEnsureVsEnsureWithContext(b *testing.B) {
	originalGitQuiet := GitQuiet
	GitQuiet = true
	defer func() { GitQuiet = originalGitQuiet }()

	// Create test jsonnet file
	createTestJsonnetFile := func(depCount int) v1.JsonnetFile {
		dependencies := deps.NewOrdered()
		for i := 0; i < depCount; i++ {
			depName := fmt.Sprintf("test-dep-%d", i)
			dependencies.Set(depName, deps.Dependency{
				Source: deps.Source{
					LocalSource: &deps.Local{
						Directory: fmt.Sprintf("testdata/%s", depName),
					},
				},
			})
		}
		return v1.JsonnetFile{
			Dependencies: dependencies,
		}
	}

	testCases := []int{5, 10, 20}

	for _, depCount := range testCases {
		jsonnetFile := createTestJsonnetFile(depCount)
		locks := deps.NewOrdered()

		b.Run(fmt.Sprintf("Ensure_%ddeps", depCount), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				tempDir := b.TempDir()
				vendorDir := filepath.Join(tempDir, "vendor")
				os.MkdirAll(vendorDir, 0755)

				_, _ = Ensure(jsonnetFile, vendorDir, locks)
			}
		})

		b.Run(fmt.Sprintf("EnsureWithContext_%ddeps", depCount), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				tempDir := b.TempDir()
				vendorDir := filepath.Join(tempDir, "vendor")
				os.MkdirAll(vendorDir, 0755)

				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_, _ = EnsureWithContext(ctx, jsonnetFile, vendorDir, locks)
				cancel()
			}
		})
	}
}

// BenchmarkDirectoryScanning measures performance of directory operations
func BenchmarkDirectoryScanning(b *testing.B) {
	// Create a test directory structure
	tempDir := b.TempDir()
	vendorDir := filepath.Join(tempDir, "vendor")
	os.MkdirAll(vendorDir, 0755)

	// Create various subdirectories and files
	structures := []struct {
		name     string
		dirCount int
		depth    int
	}{
		{"shallow_few", 10, 1},
		{"shallow_many", 100, 1},
		{"deep_few", 10, 5},
		{"deep_many", 50, 3},
	}

	for _, structure := range structures {
		testVendorDir := filepath.Join(vendorDir, structure.name)
		os.MkdirAll(testVendorDir, 0755)

		// Create directory structure
		for i := 0; i < structure.dirCount; i++ {
			dirPath := testVendorDir
			for d := 0; d < structure.depth; d++ {
				dirPath = filepath.Join(dirPath, fmt.Sprintf("level%d", d), fmt.Sprintf("dir%d", i))
			}
			os.MkdirAll(dirPath, 0755)
			// Add some files
			for f := 0; f < 3; f++ {
				filePath := filepath.Join(dirPath, fmt.Sprintf("file%d.json", f))
				os.WriteFile(filePath, []byte("{}"), 0644)
			}
		}

		b.Run(fmt.Sprintf("filepath.Walk_%s", structure.name), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				_, _ = findUnknownDirectories(testVendorDir)
			}
		})
	}
}

// BenchmarkHashDirPerformance measures checksum calculation performance
func BenchmarkHashDirPerformance(b *testing.B) {
	// Create test directories with different sizes
	tempDir := b.TempDir()

	testCases := []struct {
		name      string
		fileCount int
		fileSize  int
	}{
		{"small_files", 10, 1024},        // 10 files, 1KB each
		{"medium_files", 50, 10240},      // 50 files, 10KB each
		{"large_files", 100, 102400},     // 100 files, 100KB each
		{"many_small", 1000, 100},        // 1000 files, 100B each
	}

	for _, tc := range testCases {
		testDir := filepath.Join(tempDir, tc.name)
		os.MkdirAll(testDir, 0755)

		// Create test files
		content := make([]byte, tc.fileSize)
		for i := 0; i < tc.fileCount; i++ {
			// Fill with some pattern
			for j := range content {
				content[j] = byte((i + j) % 256)
			}
			filePath := filepath.Join(testDir, fmt.Sprintf("file%d.dat", i))
			os.WriteFile(filePath, content, 0644)
		}

		b.Run(tc.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				_ = hashDir(testDir)
			}
		})
	}
}

// BenchmarkCheckFunction measures dependency integrity check performance
func BenchmarkCheckFunction(b *testing.B) {
	originalGitQuiet := GitQuiet
	GitQuiet = true
	defer func() { GitQuiet = originalGitQuiet }()

	tempDir := b.TempDir()
	vendorDir := filepath.Join(tempDir, "vendor")
	os.MkdirAll(vendorDir, 0755)

	testCases := []struct {
		name      string
		depType   string
		withSum   bool
		fileCount int
	}{
		{"local_no_sum", "local", false, 10},
		{"remote_no_sum", "remote", false, 10},
		{"remote_with_sum", "remote", true, 10},
		{"remote_large", "remote", true, 100},
	}

	for _, tc := range testCases {
		depName := fmt.Sprintf("test-dep-%s", tc.name)
		depDir := filepath.Join(vendorDir, depName)
		os.MkdirAll(depDir, 0755)

		// Create test files
		for i := 0; i < tc.fileCount; i++ {
			filePath := filepath.Join(depDir, fmt.Sprintf("file%d.json", i))
			content := fmt.Sprintf(`{"test": %d}`, i)
			os.WriteFile(filePath, []byte(content), 0644)
		}

		var dependency deps.Dependency
		if tc.depType == "local" {
			dependency = deps.Dependency{
				Source: deps.Source{
					LocalSource: &deps.Local{
						Directory: depDir,
					},
				},
			}
		} else {
			dependency = deps.Dependency{
				Source: deps.Source{
					GitSource: &deps.Git{
						Scheme: deps.GitSchemeHTTPS,
						Host:   "github.com",
						User:   "test",
						Repo:   "repo",
					},
				},
			}
			if tc.withSum {
				dependency.Sum = hashDir(depDir)
			}
		}

		b.Run(tc.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				_ = check(dependency, vendorDir)
			}
		})
	}
}

// BenchmarkKnownFunction measures dependency name resolution performance
func BenchmarkKnownFunction(b *testing.B) {
	// Create test dependencies
	testSizes := []int{10, 50, 100, 500}

	for _, size := range testSizes {
		dependencies := deps.NewOrdered()
		for i := 0; i < size; i++ {
			depName := fmt.Sprintf("github.com/user%d/repo%d", i%10, i)
			dependencies.Set(depName, deps.Dependency{
				Source: deps.Source{
					GitSource: &deps.Git{
						Scheme: deps.GitSchemeHTTPS,
						Host:   "github.com",
						User:   fmt.Sprintf("user%d", i%10),
						Repo:   fmt.Sprintf("repo%d", i),
					},
				},
			})
		}

		testPaths := []string{
			"github.com",
			"github.com/user1",
			"github.com/user1/repo1",
			"github.com/user1/repo1/subdir",
			"github.com/unknown/repo",
			"bitbucket.org/user/repo",
		}

		b.Run(fmt.Sprintf("known_%ddeps", size), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				for _, path := range testPaths {
					_ = known(dependencies, path)
				}
			}
		})
	}
}

// BenchmarkCleanLegacyName measures legacy name cleaning performance
func BenchmarkCleanLegacyName(b *testing.B) {
	testSizes := []int{10, 50, 100, 500}

	for _, size := range testSizes {
		dependencies := deps.NewOrdered()
		for i := 0; i < size; i++ {
			depName := fmt.Sprintf("github.com/user%d/repo%d", i%10, i)
			legacy := fmt.Sprintf("legacy%d", i)
			if i%2 == 0 {
				// Half should be auto-generated (will be cleaned)
				git := &deps.Git{
					Scheme: deps.GitSchemeHTTPS,
					Host:   "github.com",
					User:   fmt.Sprintf("user%d", i%10),
					Repo:   fmt.Sprintf("repo%d", i),
				}
				legacy = git.LegacyName()
			}
			dependencies.Set(depName, deps.Dependency{
				LegacyNameCompat: legacy,
				Source: deps.Source{
					GitSource: &deps.Git{
						Scheme: deps.GitSchemeHTTPS,
						Host:   "github.com",
						User:   fmt.Sprintf("user%d", i%10),
						Repo:   fmt.Sprintf("repo%d", i),
					},
				},
			})
		}

		b.Run(fmt.Sprintf("clean_%ddeps", size), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				// Make a copy to avoid modifying the original
				depsCopy := deps.NewOrdered()
				for _, k := range dependencies.Keys() {
					d, _ := dependencies.Get(k)
					depsCopy.Set(k, d)
				}
				CleanLegacyName(depsCopy)
			}
		})
	}
}

// BenchmarkMemoryAllocationPatterns measures memory allocation in different scenarios
func BenchmarkMemoryAllocationPatterns(b *testing.B) {
	originalGitQuiet := GitQuiet
	GitQuiet = true
	defer func() { GitQuiet = originalGitQuiet }()

	b.Run("CreateDependencies", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			dependencies := deps.NewOrdered()
			for j := 0; j < 20; j++ {
				depName := fmt.Sprintf("github.com/user/repo%d", j)
				dependencies.Set(depName, deps.Dependency{
					Source: deps.Source{
						GitSource: &deps.Git{
							Scheme: deps.GitSchemeHTTPS,
							Host:   "github.com",
							User:   "user",
							Repo:   fmt.Sprintf("repo%d", j),
						},
					},
				})
			}
		}
	})

	b.Run("ProcessJsonnetFile", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			dependencies := deps.NewOrdered()
			for j := 0; j < 10; j++ {
				depName := fmt.Sprintf("test-dep-%d", j)
				dependencies.Set(depName, deps.Dependency{
					Source: deps.Source{
						LocalSource: &deps.Local{
							Directory: fmt.Sprintf("testdata/%s", depName),
						},
					},
				})
			}

			jsonnetFile := v1.JsonnetFile{
				Dependencies: dependencies,
			}

			tempDir := b.TempDir()
			vendorDir := filepath.Join(tempDir, "vendor")
			os.MkdirAll(vendorDir, 0755)

			locks := deps.NewOrdered()
			_, _ = Ensure(jsonnetFile, vendorDir, locks)
		}
	})
}

// BenchmarkConcurrentEnsureOperations measures performance under concurrent load
func BenchmarkConcurrentEnsureOperations(b *testing.B) {
	originalGitQuiet := GitQuiet
	GitQuiet = true
	defer func() { GitQuiet = originalGitQuiet }()

	// Create a shared jsonnet file
	dependencies := deps.NewOrdered()
	for i := 0; i < 5; i++ {
		depName := fmt.Sprintf("test-dep-%d", i)
		dependencies.Set(depName, deps.Dependency{
			Source: deps.Source{
				LocalSource: &deps.Local{
					Directory: fmt.Sprintf("testdata/%s", depName),
				},
			},
		})
	}

	jsonnetFile := v1.JsonnetFile{
		Dependencies: dependencies,
	}

	concurrencyLevels := []int{1, 2, 4, 8}

	for _, concurrency := range concurrencyLevels {
		b.Run(fmt.Sprintf("Concurrent_%d", concurrency), func(b *testing.B) {
			b.SetParallelism(concurrency)
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					tempDir := b.TempDir()
					vendorDir := filepath.Join(tempDir, "vendor")
					os.MkdirAll(vendorDir, 0755)

					locks := deps.NewOrdered()
					_, _ = Ensure(jsonnetFile, vendorDir, locks)
				}
			})
		})
	}
}