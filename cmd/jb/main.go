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

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/fatih/color"
	"github.com/pkg/errors"
	"gopkg.in/alecthomas/kingpin.v2"

	"github.com/jsonnet-bundler/jsonnet-bundler/pkg"
)

const (
	installActionName = "install"
	updateActionName  = "update"
	initActionName    = "init"
	rewriteActionName = "rewrite"
	cacheActionName   = "cache"
)

var Version = "dev"

func main() {
	os.Exit(Main())
}

func Main() int {
	cfg := struct {
		JsonnetHome   string
		NoGlobalCache bool
		Quiet         bool
	}{}

	color.Output = color.Error

	a := kingpin.New(filepath.Base(os.Args[0]), "A jsonnet package manager").Version(Version)
	a.HelpFlag.Short('h')

	a.Flag("jsonnetpkg-home", "The directory used to cache packages in.").
		Default("vendor").StringVar(&cfg.JsonnetHome)
	a.Flag("quiet", "Suppress any output from git command.").
		Short('q').BoolVar(&cfg.Quiet)
	a.Flag("no-global-cache", "Disable the global cache at ~/.cache/jb.").
		BoolVar(&cfg.NoGlobalCache)

	initCmd := a.Command(initActionName, "Initialize a new empty jsonnetfile")

	installCmd := a.Command(installActionName, "Install new dependencies. Existing ones are silently skipped")
	installCmdURIs := installCmd.Arg("uris", "URIs to packages to install, URLs or file paths").Strings()
	installCmdSingle := installCmd.Flag("single", "install package without dependencies").Short('1').Bool()
	installCmdLegacyName := installCmd.Flag("legacy-name", "set legacy name").String()

	updateCmd := a.Command(updateActionName, "Update all or specific dependencies.")
	updateCmdURIs := updateCmd.Arg("uris", "URIs to packages to update, URLs or file paths").Strings()

	rewriteCmd := a.Command(rewriteActionName, "Automatically rewrite legacy imports to absolute ones")

	// Cache command with subcommands
	cacheCmd := a.Command(cacheActionName, "Cache management commands")

	cacheStatusCmd := cacheCmd.Command("status", "Show status of the global cache")

	// Clean command removed

	cacheFlushCmd := cacheCmd.Command("flush", "Completely empty the cache")
	// All flags related to local cache removed

	cacheAddRemoteCmd := cacheCmd.Command("add-remote", "Add a remote cache server")
	cacheAddRemoteCmdURL := cacheAddRemoteCmd.Arg("url", "URL of the remote cache server").Required().String()

	cacheListRemoteCmd := cacheCmd.Command("list-remote", "List remote cache servers")
	cacheListRemoteCmdJSON := cacheListRemoteCmd.Flag("json", "Output in JSON format").Bool()

	cacheRemoveRemoteCmd := cacheCmd.Command("remove-remote", "Remove a remote cache server")
	cacheRemoveRemoteCmdURL := cacheRemoveRemoteCmd.Arg("url", "URL of the remote cache server to remove").Required().String()

	cacheListCmd := cacheCmd.Command("list", "List cache entries")
	cacheListCmdJSON := cacheListCmd.Flag("json", "Output in JSON format").Bool()
	// Global cache is the only cache now

	// Config command removed

	command, err := a.Parse(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, errors.Wrapf(err, "Error parsing commandline arguments"))
		a.Usage(os.Args[1:])
		return 2
	}

	workdir, err := os.Getwd()
	if err != nil {
		return 1
	}

	cfg.JsonnetHome = filepath.Clean(cfg.JsonnetHome)

	// Process global cache flag
	pkg.GlobalCacheEnabled = !cfg.NoGlobalCache
	
	// Set GitQuiet using the thread-safe function
	pkg.SetGitQuiet(cfg.Quiet)

	switch command {
	case initCmd.FullCommand():
		return initCommand(workdir)
	case installCmd.FullCommand():
		return installCommand(workdir, cfg.JsonnetHome, *installCmdURIs, *installCmdSingle, *installCmdLegacyName)
	case updateCmd.FullCommand():
		return updateCommand(workdir, cfg.JsonnetHome, *updateCmdURIs)
	case rewriteCmd.FullCommand():
		return rewriteCommand(workdir, cfg.JsonnetHome)

	// Cache commands with subcommands
	case cacheStatusCmd.FullCommand():
		return cacheStatusCommand(workdir, cfg.JsonnetHome)
	// Clean command removed
	case cacheFlushCmd.FullCommand():
		return cacheFlushCommand(workdir, cfg.JsonnetHome)
	case cacheAddRemoteCmd.FullCommand():
		return cacheAddRemoteCommand(workdir, cfg.JsonnetHome, *cacheAddRemoteCmdURL)
	case cacheListRemoteCmd.FullCommand():
		return cacheListRemoteCommand(workdir, cfg.JsonnetHome, *cacheListRemoteCmdJSON)
	case cacheRemoveRemoteCmd.FullCommand():
		return cacheRemoveRemoteCommand(workdir, cfg.JsonnetHome, *cacheRemoveRemoteCmdURL)
	case cacheListCmd.FullCommand():
		return cacheListCommand(workdir, cfg.JsonnetHome, *cacheListCmdJSON)
	// Config command removed

	default:
		installCommand(workdir, cfg.JsonnetHome, []string{}, false, "")
	}

	return 0
}
