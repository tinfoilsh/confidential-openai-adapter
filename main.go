package main

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	tinfoil "github.com/tinfoilsh/tinfoil-go"
)

const catalogRefresh = 10 * time.Second

func main() {
	addr := flag.String("addr", cmp.Or(os.Getenv("TINFOIL_ADDR"), ":8443"), "listen address")
	gateway := flag.String("gateway", cmp.Or(os.Getenv("TINFOIL_GATEWAY_URL"), "https://inference-gateway.tinfoil.sh"), "tinfoil-gateway base URL")
	certFile := flag.String("cert", os.Getenv("TINFOIL_TLS_CERT"), "TLS certificate; without one the listener is plaintext, for a CVM whose shim already terminates TLS")
	keyFile := flag.String("key", os.Getenv("TINFOIL_TLS_KEY"), "TLS private key")
	flag.Parse()

	base, err := url.Parse(strings.TrimRight(*gateway, "/"))
	if err != nil || base.Host == "" {
		slog.Error("gateway must be an absolute URL", "gateway", *gateway, "error", err)
		os.Exit(1)
	}

	// Cache namespaces and replica routing reset on restart.
	root := make([]byte, 32)
	rand.Read(root)

	var catalog atomic.Pointer[tinfoil.Catalog]
	catalog.Store(&tinfoil.Catalog{})
	go refreshCatalog(base, &catalog)
	gw, err := tinfoil.NewGateway(base.String()+"/v1/",
		func() tinfoil.Catalog { return *catalog.Load() },
		tinfoil.WithUserCacheSecret(hex.EncodeToString(root)),
	)
	if err != nil {
		slog.Error("configure gateway", "gateway", base, "error", err)
		os.Exit(1)
	}

	// Leave WriteTimeout unset for long-running streamed completions.
	srv := &http.Server{Addr: *addr, Handler: newAdapter(gw, &catalog, base, root), ReadHeaderTimeout: 10 * time.Second}
	signalled, _ := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	context.AfterFunc(signalled, func() { srv.Shutdown(context.Background()) })

	slog.Info("listening", "addr", *addr, "gateway", base, "tls", *certFile != "")
	if *certFile != "" {
		err = srv.ListenAndServeTLS(*certFile, *keyFile)
	} else {
		err = srv.ListenAndServe()
	}
	if err != http.ErrServerClosed {
		slog.Error("serve", "error", err)
		os.Exit(1)
	}
}

func refreshCatalog(gateway *url.URL, catalog *atomic.Pointer[tinfoil.Catalog]) {
	for ; ; time.Sleep(catalogRefresh) {
		fetched, err := tinfoil.FetchCatalog(gateway.Host)
		if err != nil {
			slog.Warn("fetch catalog", "gateway", gateway, "error", err)
			continue
		}
		logCatalogChanges(*catalog.Swap(&fetched), fetched)
	}
}

// The gateway lists only replicas passing its health checks, so a replica
// leaving is one it stopped routing to.
func logCatalogChanges(prev, next tinfoil.Catalog) {
	for model, entry := range prev {
		for _, host := range entry.Hosts {
			if !slices.Contains(next[model].Hosts, host) {
				slog.Info("replica left catalog", "model", model, "host", host)
			}
		}
	}
	for model, entry := range next {
		for _, host := range entry.Hosts {
			if !slices.Contains(prev[model].Hosts, host) {
				slog.Info("replica joined catalog", "model", model, "host", host)
			}
		}
	}
}
