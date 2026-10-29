// Command nexovirt-box is NexoVirt Box: a web UI on top of a normal Linux server.
package main

import "github.com/NexoVirt/nexovirt-box/internal/box"

// version is stamped at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() { box.Run(version) }
