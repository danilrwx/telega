package main

import (
	"fmt"
	"os"
	"runtime/debug"
)

var version = "dev"

func appVersion() string {
	if version != "dev" {
		return version
	}

	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}

	return version
}

func main() {
	const usage = "Usage: telega [--demo | --qr | --version]"

	args := os.Args[1:]
	switch {
	case len(args) == 1 && args[0] == "--help":
		fmt.Println(usage)
	case len(args) == 1 && args[0] == "--version":
		fmt.Println("telega", appVersion())
	case len(args) == 0 || len(args) == 1 && (args[0] == "--demo" || args[0] == "--qr"):
		if err := run(len(args) != 0 && args[0] == "--demo", len(args) != 0 && args[0] == "--qr"); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
}
