# Concurrency and Parallelization Benchmarks

This document describes the comprehensive benchmark suite for testing concurrency and parallelization performance in jsonnet-bundler.

## Overview

The benchmark suite consists of three main files:

- `pkg/packages_parallel_bench_test.go` - Parallel package processing benchmarks
- `pkg/git_parallel_bench_test.go` - Git parallel operations benchmarks  
- `pkg/packages_bench_test.go` - Main package functionality benchmarks

## Running Benchmarks

### All Benchmarks
```bash
go test -bench=. -benchmem ./pkg
```

### Specific Benchmark Categories

#### Sequential vs Parallel Performance
```bash
go test -bench=BenchmarkSequentialVsParallelEnsure -benchmem ./pkg
```

#### Worker Pool Scaling
```bash
go test -bench=BenchmarkWorkerPoolScaling -benchmem ./pkg
```

#### Concurrency Patterns
```bash
go test -bench=BenchmarkConcurrentDataStructureAccess -benchmem ./pkg
```

#### Cache Operations
```bash
go test -bench=BenchmarkParallelCacheCheck -benchmem ./pkg
```

#### Memory Allocation Analysis
```bash
go test -bench=BenchmarkMemoryAllocation -benchmem ./pkg
```

## Benchmark Categories

### 1. Core Performance Comparisons

#### BenchmarkSequentialVsParallelEnsure
Compares sequential vs parallel dependency processing across different dependency counts:
- **Purpose**: Measure performance differences between sequential and parallel processing
- **Metrics**: Execution time, memory allocations
- **Variants**: 5, 10, 20, 50 dependencies

**Example Results:**
```
BenchmarkSequentialVsParallelEnsure/Sequential_5deps-10    15664   151034 ns/op    7389 B/op    69 allocs/op
BenchmarkSequentialVsParallelEnsure/Parallel_5deps-10      9499   253403 ns/op   37121 B/op   348 allocs/op
```

**Analysis:** 
- Sequential processing is faster for small dependency sets due to overhead
- Parallel processing scales better with larger dependency counts
- Memory usage increases with parallel processing due to goroutine overhead

### 2. Worker Pool Performance

#### BenchmarkWorkerPoolScaling
Tests performance scaling with different numbers of worker goroutines:
- **Purpose**: Find optimal worker pool sizes
- **Metrics**: Throughput, resource utilization
- **Variants**: 1, 2, 4, 8, 16, 32 workers

### 3. Concurrency Safety

#### BenchmarkConcurrentDataStructureAccess
Measures performance of concurrent read/write operations:
- **Purpose**: Validate thread-safety performance
- **Patterns**: Read-heavy, write-heavy, mixed workloads
- **Metrics**: Operations per second, contention overhead

### 4. Cache Operations

#### BenchmarkParallelCacheCheck
Compares sequential vs parallel remote cache checking:
- **Purpose**: Measure cache operation efficiency
- **Scenarios**: Fast servers, slow servers, mixed latencies
- **Metrics**: Cache hit time, network utilization

### 5. Error Handling Patterns

#### BenchmarkErrorHandlingPatterns
Compares different error propagation mechanisms:
- **Patterns**: Error channels, sync.Once, mutex-protected access
- **Purpose**: Find most efficient error handling approach
- **Metrics**: Error propagation speed, memory overhead

### 6. Memory Analysis

#### BenchmarkMemoryAllocation
Analyzes memory allocation patterns in parallel operations:
- **Purpose**: Identify memory bottlenecks
- **Focus**: Allocation rate, GC pressure
- **Optimization**: Reduce allocations in hot paths

## Performance Analysis Guidelines

### Interpreting Results

1. **ns/op (nanoseconds per operation)**: Lower is better
2. **B/op (bytes per operation)**: Lower memory usage is generally better
3. **allocs/op (allocations per operation)**: Fewer allocations reduce GC pressure

### Key Performance Indicators

- **Scalability**: Performance should improve with more workers up to a point
- **Overhead**: Parallel processing should outperform sequential for larger workloads
- **Memory efficiency**: Memory usage should scale reasonably with parallelism

### Optimization Tips

1. **Worker Count**: Optimal worker count is typically 2-4x CPU cores
2. **Batch Size**: Larger batches reduce coordination overhead
3. **Channel Buffering**: Appropriate buffer sizes prevent blocking
4. **Context Usage**: Minimal overhead for cancellation checks

## Continuous Performance Monitoring

### Running Performance Regression Tests
```bash
# Run benchmarks and save baseline
go test -bench=. -benchmem ./pkg > baseline.txt

# Run benchmarks and compare to baseline
go test -bench=. -benchmem ./pkg > current.txt
benchcmp baseline.txt current.txt
```

### Automated Performance Testing
```bash
# Run benchmarks with consistent environment
go test -bench=. -benchmem -count=5 ./pkg
```

## Environment Considerations

### Hardware Impact
- **CPU Cores**: More cores generally improve parallel performance
- **Memory**: Sufficient RAM prevents swapping during tests  
- **Network**: Cache benchmarks affected by network latency

### Software Factors
- **Go Version**: Performance characteristics may vary between versions
- **OS Scheduler**: Can affect goroutine scheduling efficiency
- **System Load**: Other processes can skew results

## Best Practices

1. **Consistent Environment**: Run benchmarks on dedicated hardware
2. **Multiple Runs**: Use `-count=5` for statistical significance
3. **Warm-up**: First run may be slower due to cold caches
4. **Isolation**: Disable unnecessary services during benchmarking
5. **Version Control**: Track performance changes over time

## Troubleshooting

### Common Issues

1. **High Variance**: System load or thermal throttling
2. **Memory Growth**: Check for goroutine or memory leaks
3. **Poor Scaling**: Lock contention or false sharing
4. **Network Issues**: Cache benchmarks failing due to connectivity

### Debugging Performance

1. **CPU Profiling**: `go test -bench=. -cpuprofile=cpu.prof`
2. **Memory Profiling**: `go test -bench=. -memprofile=mem.prof`
3. **Trace Analysis**: `go test -bench=. -trace=trace.out`
4. **Race Detection**: `go test -bench=. -race` (slower but finds race conditions)

## Benchmark Results Archive

Results from key benchmark runs should be archived for performance regression analysis:

```
_output/benchmarks/
├── baseline/
│   ├── v0.6.0_baseline.txt
│   └── v0.7.0_baseline.txt
├── profiles/
│   ├── cpu.prof
│   └── mem.prof
└── analysis/
    ├── performance_analysis.md
    └── regression_report.md
```

This comprehensive benchmark suite enables continuous performance monitoring and optimization of jsonnet-bundler's concurrency and parallelization features.