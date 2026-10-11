package main

import (
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
	if err := run(log); err != nil {
		log.Error("fatal", "err", err.Error())
		os.Exit(1)
	}
}
