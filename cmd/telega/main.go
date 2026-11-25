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
	const usage = "Usage: telega [--demo | --phone | --qr | --version | --open LINK]\n\nSign in with a QR code by default. Use --phone to sign in with a phone number."

	args := os.Args[1:]
	switch {
	case len(args) == 1 && args[0] == "--help":
		fmt.Println(usage)
	case len(args) == 1 && args[0] == "--version":
		fmt.Println("telega", appVersion())
	case len(args) == 2 && args[0] == "--open" && args[1] != "",
		len(args) == 0 || len(args) == 1 && (args[0] == "--demo" || args[0] == "--phone" || args[0] == "--qr"):
		opts := telega.Options{Version: appVersion(), QR: true}
		if len(args) == 2 {
			opts.OpenLink = args[1]
		} else if len(args) == 1 {
			opts.Demo = args[0] == "--demo"
			opts.QR = args[0] != "--phone"
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
