# jsonnet-bundler

> NOTE: This project is *alpha* stage. Flags, configuration, behavior and design may change significantly in following releases.

The jsonnet-bundler is a package manager for [Jsonnet](http://jsonnet.org/).


## Install

```
go install -a github.com/jsonnet-bundler/jsonnet-bundler/cmd/jb@latest
```
**NOTE**: please use a recent Go version to do this, ideally Go 1.13 or greater.

This will put `jb` in `$(go env GOPATH)/bin`. If you encounter the error
`jb: command not found` after installation then you may need to add that directory to your `$PATH` as shown [in their docs](https://golang.org/doc/code.html#GOPATH).

## Package Install

* [Arch Linux AUR](https://aur.archlinux.org/packages/jsonnet-bundler-bin)
* Mac OS X via Homebrew: `brew install jsonnet-bundler`
* Fedora (>= 32): `sudo dnf install golang-github-jsonnet-bundler`

## Features

- Fetches transitive dependencies
- Can vendor subtrees, as opposed to whole repositories


## Current Limitations

- If two dependencies depend on the same package (diamond problem), they must require the same version

## Caching

jsonnet-bundler uses a sophisticated caching system with concurrent lookups for optimal performance:

1. **Global Cache**: Located in `~/.cache/jb` by default. This cache is shared between all projects on your system and persists between different runs. The global cache includes:
   - Downloaded GitHub archives
   - Git repositories

2. **Remote Caches**: Optional HTTP/HTTPS or S3 servers that can provide cached dependencies to multiple developers or build environments. Supports:
   - HTTP/HTTPS servers
   - Amazon S3 buckets with the format `s3://bucket-name/path`

**Performance Optimizations**:
- **Concurrent Lookups**: Uses goroutines to check multiple cache sources simultaneously
  - All cache lookups happen in parallel to minimize wait time
  - Remote requests are made concurrently with timeout controls
  - Results are processed in priority order

The caching system keeps track of metadata including:
- File size and creation time
- Last access time for expiration calculation
- Source information and content hash

### Cache Options

You can control the cache with various options:

- Disable global caching with the `--no-global-cache` flag
- Change the global cache location by setting the `JB_CACHE_DIR` environment variable:
  ```bash
  export JB_CACHE_DIR="/custom/path/to/cache"
  jb update
  ```

### Cache Commands

jsonnet-bundler provides several subcommands under the `cache` command:

```
jb cache status                   # Show cache status (size, entries, etc.)
jb cache add-remote URL           # Add a remote cache server
jb cache list-remote              # List configured remote cache servers
jb cache remove-remote URL        # Remove a remote cache server
jb cache list                     # List all cache entries
```

#### Cache Management

The cache is automatically managed. Dependencies are cached globally and can be shared through remote cache servers.

#### Remote Cache Sharing

You can share caches between team members or CI systems:

##### HTTP Remote Cache

```bash
# Add an HTTP remote cache server
jb cache add-remote https://cache.example.com/jsonnet

# List cache entries in JSON format
jb cache list --json
```

##### S3 Remote Cache

```bash
# Configure AWS credentials (if not using instance profiles or other methods)
export AWS_ACCESS_KEY_ID=your_access_key
export AWS_SECRET_ACCESS_KEY=your_secret_key
export AWS_REGION=us-west-2  # Optional, defaults to us-east-1

# Add an S3 remote cache
jb cache add-remote s3://my-jsonnet-cache-bucket/jsonnet

# List configured remote caches
jb cache list-remote

# Remove a remote cache when no longer needed
jb cache remove-remote s3://my-jsonnet-cache-bucket/jsonnet
```

## Example Usage

Initialize your project:

```sh
mkdir myproject
cd myproject
jb init
```

The existence of the `jsonnetfile.json` file means your directory is now a
jsonnet-bundler package that can define dependencies.

To depend on another package (another Github repository):
*Note that your dependency need not be initialized with a `jsonnetfile.json`.
If it is not, it is assumed it has no transitive dependencies.*

```sh
jb install https://github.com/anguslees/kustomize-libsonnet
```

Now write `myconfig.jsonnet`, which can import a file from that package.
Remember to use `-J vendor` when running Jsonnet to include the vendor tree.

```jsonnet
local kustomize = import 'kustomize-libsonnet/kustomize.libsonnet';

local my_resource = {
  metadata: {
    name: 'my-resource',
  },
};

kustomize.namePrefix('staging-')(my_resource)
```

To depend on a package that is in a subtree of a Github repo (this package also
happens to bring in a transitive dependency):

```sh
jb install https://github.com/prometheus-operator/prometheus-operator/jsonnet/prometheus-operator
```

*Note that if you are copy pasting from the Github website's address bar,
remove the `tree/master` from the path.*

If pushed to Github, your project can now be referenced from other packages in
the same way, with its dependencies fetched automatically.


## All command line flags

[embedmd]:# (_output/help.txt)
```txt
$ jb -h
usage: jb [<flags>] <command> [<args> ...]

A jsonnet package manager

Flags:
  -h, --help             Show context-sensitive help (also try --help-long and
                         --help-man).
      --version          Show application version.
      --jsonnetpkg-home="vendor"
                         The directory used to cache packages in.
  -q, --quiet            Suppress any output from git command.
      --no-global-cache  Disable the global cache at ~/.cache/jb.

Commands:
  help [<command>...]
    Show help.

  init
    Initialize a new empty jsonnetfile

  install [<flags>] [<uris>...]
    Install new dependencies. Existing ones are silently skipped

  update [<uris>...]
    Update all or specific dependencies.

  rewrite
    Automatically rewrite legacy imports to absolute ones

  cache status
    Show status of the global cache

  cache flush
    Completely empty the cache

  cache add-remote <url>
    Add a remote cache server

  cache list-remote [<flags>]
    List remote cache servers

  cache remove-remote <url>
    Remove a remote cache server

  cache list [<flags>]
    List cache entries


```

## Design

This is an implemention of the design specified in this document: https://docs.google.com/document/d/1czRScSvvOiAJaIjwf3CogOULgQxhY9MkiBKOQI1yR14/edit#heading=h.upn4d5pcxy4c
