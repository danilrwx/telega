package main

import (
	"fmt"
	"os"
	"runtime/debug"

	"github.com/danilrwx/telega/internal/telega"
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
		opts := telega.Options{Version: appVersion()}
		if len(args) != 0 {
			opts.Demo = args[0] == "--demo"
			opts.QR = args[0] == "--qr"
		}

		if err := telega.Run(opts); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
}
