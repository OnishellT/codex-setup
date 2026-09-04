package main

import "embed"

// Files are bundled: the installer does not need the source PC or this directory at runtime.
//
//go:embed payload
var assets embed.FS
