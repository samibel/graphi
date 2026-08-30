// Command app wires the fixture's layers together: configuration → store →
// HTTP handler. It is the only place the concrete Store implementation is
// chosen.
package main

import (
	"fmt"
	"net/http"
	"os"

	"example.com/fixture/config"
	"example.com/fixture/httpapi"
	"example.com/fixture/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run loads the configuration, opens the configured store and serves the
// handler on the configured address.
func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	s, err := store.Open(cfg.StoreKind, cfg.StoreRoot)
	if err != nil {
		return err
	}
	handler := httpapi.NewHandler(s, cfg.Secret)
	return http.ListenAndServe(cfg.Addr, handler)
}
