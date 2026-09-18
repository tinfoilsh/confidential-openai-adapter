package main

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	tinfoil "github.com/tinfoilsh/tinfoil-go"
)

type model struct {
	name    string
	repo    string
	enclave string

	proxy *httputil.ReverseProxy
}

const trustedOwner = "tinfoilsh/"

func main() {
	addr := flag.String("addr", cmp.Or(os.Getenv("TINFOIL_ADDR"), ":8443"), "listen address")
	gateway := flag.String("gateway", cmp.Or(os.Getenv("TINFOIL_GATEWAY_URL"), "https://gateway.tinfoil.sh"), "tinfoil-gateway base URL")
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

	catalog, err := fetchCatalog(base)
	if err != nil {
		slog.Error("fetch catalog", "gateway", base, "error", err)
		os.Exit(1)
	}
	live := attestCatalog(base, catalog, hex.EncodeToString(root))
	if len(live) == 0 {
		slog.Error("no catalog enclave verified", "gateway", base)
		os.Exit(1)
	}

	// Leave WriteTimeout unset for long-running streamed completions.
	srv := &http.Server{Addr: *addr, Handler: newAdapter(live, root), ReadHeaderTimeout: 10 * time.Second}
	signalled, _ := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	context.AfterFunc(signalled, func() { srv.Shutdown(context.Background()) })

	slog.Info("listening", "addr", *addr, "gateway", base, "tls", *certFile != "", "models", names(live))
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

func fetchCatalog(gateway *url.URL) ([]*model, error) {
	resp, err := http.Get(gateway.String() + "/catalog")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(resp.Status)
	}
	var specs map[string]struct {
		Repo  string   `json:"repo"`
		Hosts []string `json:"hosts"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&specs); err != nil {
		return nil, err
	}
	var catalog []*model
	for _, name := range slices.Sorted(maps.Keys(specs)) {
		spec := specs[name]
		if !strings.HasPrefix(spec.Repo, trustedOwner) || len(spec.Hosts) == 0 {
			slog.Warn("catalog entry not served", "model", name, "repo", spec.Repo)
			continue
		}
		catalog = append(catalog, &model{name: name, repo: spec.Repo, enclave: spec.Hosts[0]})
	}
	return catalog, nil
}

func attestCatalog(gateway *url.URL, catalog []*model, cacheSecret string) []*model {
	var wg sync.WaitGroup
	for _, m := range catalog {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// The gateway forwards bodies encrypted to the enclave's attested key.
			verified, err := tinfoil.NewClientWithOptions(
				tinfoil.WithEnclave(m.enclave),
				tinfoil.WithRepo(m.repo),
				tinfoil.WithBaseURL(gateway.String()+"/v1/"),
				tinfoil.WithUserCacheSecret(cacheSecret),
			)
			if err != nil {
				slog.Warn("enclave did not verify", "model", m.name, "enclave", m.enclave, "error", err)
				return
			}
			m.proxy = newProxy(gateway, verified.HTTPClient().Transport)
		}()
	}
	wg.Wait()

	var live []*model
	for _, m := range catalog {
		if m.proxy != nil {
			live = append(live, m)
		}
	}
	return live
}

func names(models []*model) string {
	var out []string
	for _, m := range models {
		out = append(out, m.name)
	}
	return strings.Join(out, ", ")
}
