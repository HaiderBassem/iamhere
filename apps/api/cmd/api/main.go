package main

import (
	"fmt"
	"log/slog"
	"os"
)

func main() {
	// `api healthcheck` lets the distroless image (no shell, no curl) be probed
	// by the container runtime.
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck())
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	// `api reset-link <email>` prints a password reset link (no email service needed).
	if len(os.Args) > 1 && os.Args[1] == "reset-link" {
		if len(os.Args) != 3 {
			fmt.Fprintln(os.Stderr, "usage: api reset-link <email>")
			os.Exit(2)
		}
		if err := resetLink(slog.New(slog.NewJSONHandler(os.Stderr, nil)), os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}
	if err := run(log); err != nil {
		log.Error("fatal", "err", err.Error())
		os.Exit(1)
	}
}
