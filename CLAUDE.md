# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

### Building
- Build the binary: `make build`
- Build a static binary: `make static`
- Install to your GOPATH: `make install`
- Cross-compile for multiple platforms: `make cross`

### Testing
- Run unit tests: `make test`
- Run integration tests: `make test-integration`

### Documentation
- Generate documentation: `make generate`

### Other
- Clean build artifacts: `make clean`
- Check license headers: `make check-license`

## Code Architecture

jsonnet-bundler (jb) is a package manager for Jsonnet that handles dependency resolution and installation.

### Core Components:

1. **Command Line Interface** (`cmd/jb/`)
   - `main.go`: Entry point, handles CLI arguments and command routing
   - `init.go`: Initializes a new jsonnetfile in the current directory
   - `install.go`: Handles the installation of dependencies
   - `update.go`: Updates existing dependencies
   - `rewrite.go`: Rewrites legacy imports to absolute ones

2. **Package Management** (`pkg/`)
   - `interface.go`: Defines the core interface for package sources
   - `git.go`: Implements Git repository fetching (GitHub optimizations)
   - `local.go`: Implements local filesystem package support
   - `packages.go`: Core dependency resolution and installation logic

3. **Configuration Files** (`pkg/jsonnetfile/`)
   - `jsonnetfile.go`: Handles parsing and writing the jsonnetfile.json and jsonnetfile.lock.json

4. **Specification** (`spec/`)
   - `v0/`: Contains the first version of the spec
   - `v1/`: Contains the current spec, with improvements
     - `deps/`: Defines dependency types and operations

### Workflow:

1. When `jb init` is run, an empty jsonnetfile.json is created
2. When `jb install` is run with a URI, the dependency is added to jsonnetfile.json and installed to vendor/
3. Dependencies are fetched from sources (Git repositories or local paths)
4. When a dependency has a jsonnetfile.json, its dependencies are also installed
5. A jsonnetfile.lock.json is created/updated with exact versions and checksums

### Key Features:

- Transitive dependency resolution
- Support for Git repositories, including specific commits/tags/branches
- Support for local dependencies
- Checksum-based integrity validation
- Caching of GitHub archives for faster installation
- Legacy import compatibility through symlinks
- Parallel cache fetching for improved performance
- Parallel package downloads (enable with JB_PARALLEL_DOWNLOADS=true)
