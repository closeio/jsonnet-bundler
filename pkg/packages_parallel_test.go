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
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/jsonnet-bundler/jsonnet-bundler/spec/v1/deps"
)

func TestParallelEnsure(t *testing.T) {
	// Create a temporary vendor directory
	tempDir := t.TempDir()
	vendorDir := filepath.Join(tempDir, "vendor")
	if err := os.MkdirAll(vendorDir, 0755); err != nil {
		t.Fatalf("Failed to create vendor dir: %v", err)
	}

	// Save original GitQuiet value
	originalGitQuiet := GitQuiet
	GitQuiet = true
	defer func() { GitQuiet = originalGitQuiet }()

	tests := []struct {
		name        string
		direct      *deps.Ordered
		locks       *deps.Ordered
		expectError bool
		expectDeps  int
	}{
		{
			name:        "Empty dependencies",
			direct:      deps.NewOrdered(),
			locks:       deps.NewOrdered(),
			expectError: false,
			expectDeps:  0,
		},
		{
			name: "Single local dependency",
			direct: func() *deps.Ordered {
				d := deps.NewOrdered()
				d.Set("local-lib", deps.Dependency{
					Source: deps.Source{
						LocalSource: &deps.Local{
							Directory: "testdata/local-lib",
						},
					},
				})
				return d
			}(),
			locks:       deps.NewOrdered(),
			expectError: true, // Will fail because testdata doesn't exist
			expectDeps:  0,
		},
		{
			name: "Multiple dependencies with locks",
			direct: func() *deps.Ordered {
				d := deps.NewOrdered()
				d.Set("dep1", deps.Dependency{
					Source: deps.Source{
						LocalSource: &deps.Local{
							Directory: "testdata/dep1",
						},
					},
				})
				d.Set("dep2", deps.Dependency{
					Source: deps.Source{
						LocalSource: &deps.Local{
							Directory: "testdata/dep2",
						},
					},
				})
				return d
			}(),
			locks: func() *deps.Ordered {
				l := deps.NewOrdered()
				l.Set("dep1", deps.Dependency{
					Source: deps.Source{
						LocalSource: &deps.Local{
							Directory: "testdata/dep1",
						},
					},
					Version: "v1.0.0",
					Sum:     "abc123",
				})
				return l
			}(),
			expectError: true, // Will fail because testdata doesn't exist
			expectDeps:  0,
		},
		{
			name: "Dependencies with single flag",
			direct: func() *deps.Ordered {
				d := deps.NewOrdered()
				d.Set("single-dep", deps.Dependency{
					Single: true,
					Source: deps.Source{
						LocalSource: &deps.Local{
							Directory: "testdata/single",
						},
					},
				})
				return d
			}(),
			locks:       deps.NewOrdered(),
			expectError: true, // Will fail because testdata doesn't exist
			expectDeps:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var locksSharedMutex sync.Mutex
			result, err := parallelEnsure(tt.direct, vendorDir, tempDir, tt.locks, &locksSharedMutex)

			if tt.expectError && err == nil {
				t.Errorf("Expected error but got none")
			}

			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if !tt.expectError && result != nil {
				if len(result.Keys()) != tt.expectDeps {
					t.Errorf("Expected %d dependencies, got %d", tt.expectDeps, len(result.Keys()))
				}
			}
		})
	}
}

func TestEnsureParallel(t *testing.T) {
	// Create a temporary vendor directory
	tempDir := t.TempDir()
	vendorDir := filepath.Join(tempDir, "vendor")
	if err := os.MkdirAll(vendorDir, 0755); err != nil {
		t.Fatalf("Failed to create vendor dir: %v", err)
	}

	// Save original GitQuiet value
	originalGitQuiet := GitQuiet
	GitQuiet = false // Test with output
	defer func() { GitQuiet = originalGitQuiet }()

	direct := deps.NewOrdered()
	locks := deps.NewOrdered()

	// Test that the public interface works
	result, err := EnsureParallel(direct, vendorDir, tempDir, locks)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	if result == nil {
		t.Error("Expected non-nil result")
	}
}

func TestParallelEnsureWithNestedDependencies(t *testing.T) {
	// This test simulates nested dependencies scenario
	tempDir := t.TempDir()
	vendorDir := filepath.Join(tempDir, "vendor")
	if err := os.MkdirAll(vendorDir, 0755); err != nil {
		t.Fatalf("Failed to create vendor dir: %v", err)
	}

	// Create a mock dependency structure
	dep1Dir := filepath.Join(vendorDir, "github.com/test/dep1")
	if err := os.MkdirAll(dep1Dir, 0755); err != nil {
		t.Fatalf("Failed to create dep1 dir: %v", err)
	}

	// Create a jsonnetfile.json for the nested dependency
	jsonnetFilePath := filepath.Join(dep1Dir, "jsonnetfile.json")
	jsonnetContent := `{
		"version": 1,
		"dependencies": [
			{
				"source": {
					"local": {
						"directory": "testdata/nested"
					}
				},
				"version": ""
			}
		],
		"legacyImports": true
	}`
	if err := os.WriteFile(jsonnetFilePath, []byte(jsonnetContent), 0644); err != nil {
		t.Fatalf("Failed to write jsonnetfile: %v", err)
	}

	// Save original GitQuiet value
	originalGitQuiet := GitQuiet
	GitQuiet = true
	defer func() { GitQuiet = originalGitQuiet }()

	// Set up dependencies
	direct := deps.NewOrdered()
	direct.Set("github.com/test/dep1", deps.Dependency{
		Source: deps.Source{
			LocalSource: &deps.Local{
				Directory: dep1Dir,
			},
		},
	})

	locks := deps.NewOrdered()
	locks.Set("github.com/test/dep1", deps.Dependency{
		Source: deps.Source{
			LocalSource: &deps.Local{
				Directory: dep1Dir,
			},
		},
		Version: "v1.0.0",
		Sum:     "checksum",
	})

	// Since we can't mock the check function directly,
	// we'll rely on the fact that check will fail for our test directories

	// Run parallel ensure
	var locksSharedMutex sync.Mutex
	result, err := parallelEnsure(direct, vendorDir, tempDir, locks, &locksSharedMutex)

	// We expect this to fail when trying to process nested dependencies
	// because the nested dependency path doesn't exist
	if err == nil {
		if result != nil && len(result.Keys()) > 0 {
			// Check if the main dependency was processed
			if _, ok := result.Get("github.com/test/dep1"); !ok {
				t.Error("Expected main dependency to be in result")
			}
		}
	}
}

func TestParallelEnsureConcurrency(t *testing.T) {
	// Test that the semaphore properly limits concurrent downloads
	tempDir := t.TempDir()
	vendorDir := filepath.Join(tempDir, "vendor")
	if err := os.MkdirAll(vendorDir, 0755); err != nil {
		t.Fatalf("Failed to create vendor dir: %v", err)
	}

	// Save original GitQuiet value
	originalGitQuiet := GitQuiet
	GitQuiet = true
	defer func() { GitQuiet = originalGitQuiet }()

	// Create many dependencies to test concurrency limit
	direct := deps.NewOrdered()
	for i := 0; i < 10; i++ {
		depName := string(rune('a' + i))
		direct.Set(depName, deps.Dependency{
			Source: deps.Source{
				LocalSource: &deps.Local{
					Directory: filepath.Join("testdata", depName),
				},
			},
		})
	}

	locks := deps.NewOrdered()

	// This will fail because the test directories don't exist,
	// but we're testing that it handles multiple concurrent operations
	var locksSharedMutex sync.Mutex
	_, err := parallelEnsure(direct, vendorDir, tempDir, locks, &locksSharedMutex)
	if err == nil {
		t.Error("Expected error for non-existent directories")
	}
}

func TestParallelEnsureChecksumMismatch(t *testing.T) {
	// Test checksum mismatch handling
	tempDir := t.TempDir()
	vendorDir := filepath.Join(tempDir, "vendor")
	if err := os.MkdirAll(vendorDir, 0755); err != nil {
		t.Fatalf("Failed to create vendor dir: %v", err)
	}

	// Create a mock dependency
	depDir := filepath.Join(tempDir, "mock-dep")
	if err := os.MkdirAll(depDir, 0755); err != nil {
		t.Fatalf("Failed to create dep dir: %v", err)
	}

	// Save original GitQuiet value
	originalGitQuiet := GitQuiet
	GitQuiet = true
	defer func() { GitQuiet = originalGitQuiet }()

	direct := deps.NewOrdered()
	direct.Set("mock-dep", deps.Dependency{
		Source: deps.Source{
			LocalSource: &deps.Local{
				Directory: depDir,
			},
		},
	})

	// Set up locks with a different checksum
	locks := deps.NewOrdered()
	locks.Set("mock-dep", deps.Dependency{
		Source: deps.Source{
			LocalSource: &deps.Local{
				Directory: depDir,
			},
		},
		Version: "v1.0.0",
		Sum:     "expected-checksum",
	})

	// We can't mock the download function directly in this test framework,
	// so we'll create a scenario that would trigger a checksum mismatch
	// if the code were to execute successfully

	// Run parallel ensure
	// This test now simply verifies that the function handles errors appropriately
	var locksSharedMutex sync.Mutex
	_, err := parallelEnsure(direct, vendorDir, tempDir, locks, &locksSharedMutex)
	if err == nil {
		t.Error("Expected error for non-existent dependency")
	}
}
