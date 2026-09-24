package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"

	tinfoil "github.com/tinfoilsh/tinfoil-go"
)

const maxRequestBody = 8 << 20

const (
	userCacheSecretField = "user_cache_secret"
	promptCacheKeyField  = "prompt_cache_key"
)

type adapter struct {
	catalog *atomic.Pointer[tinfoil.Catalog]
	proxy   *httputil.ReverseProxy
	root    []byte
}

func newAdapter(gw *tinfoil.Gateway, catalog *atomic.Pointer[tinfoil.Catalog], gateway *url.URL, root []byte) http.Handler {
	a := &adapter{catalog: catalog, proxy: newProxy(gateway, gw.HTTPClient().Transport), root: root}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") })
	mux.HandleFunc("GET /v1/models", a.list)
	mux.HandleFunc("POST /v1/", a.forward)
	return mux
}

func newProxy(gateway *url.URL, sealing http.RoundTripper) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme, pr.Out.URL.Host, pr.Out.Host = gateway.Scheme, gateway.Host, gateway.Host
			// The SDK sets routing and encryption headers; discard caller-supplied values.
			for name := range pr.Out.Header {
				if strings.HasPrefix(name, "X-Tinfoil-") {
					pr.Out.Header.Del(name)
				}
			}
		},
		Transport:     sealing,
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			slog.Error("upstream failed", "path", r.URL.Path, "error", err)
			writeError(w, http.StatusBadGateway, "gateway unavailable")
		},
	}
}

func (a *adapter) forward(w http.ResponseWriter, r *http.Request) {
	// The gateway validates the key.
	apiKey := r.Header.Get("Authorization")
	if apiKey == "" {
		writeError(w, http.StatusUnauthorized, "missing API key")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBody))
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not read request body")
		return
	}
	var body map[string]json.RawMessage
	if json.Unmarshal(raw, &body) != nil {
		writeError(w, http.StatusBadRequest, "body must be a JSON object")
		return
	}
	models := a.models()
	if len(models) == 0 {
		writeError(w, http.StatusServiceUnavailable, "gateway catalog unavailable")
		return
	}
	if want := unquote(body["model"]); !slices.Contains(models, want) {
		writeError(w, http.StatusNotFound, fmt.Sprintf("unknown model %q: this endpoint serves %s", want, strings.Join(models, ", ")))
		return
	}
	if raw, err = a.scopeCache(body, raw, r.URL.Path, apiKey); err != nil {
		writeError(w, http.StatusBadRequest, "could not scope prompt cache")
		return
	}

	r.Header.Set("Content-Type", "application/json")
	r.Body = io.NopCloser(bytes.NewReader(raw))
	r.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(raw)), nil }
	r.ContentLength = int64(len(raw))
	a.proxy.ServeHTTP(w, r)
}

func (a *adapter) list(w http.ResponseWriter, r *http.Request) {
	models := a.models()
	data := make([]map[string]string, 0, len(models))
	for _, name := range models {
		data = append(data, map[string]string{"id": name, "object": "model", "owned_by": "tinfoil"})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
}

// scopeCache sets a per-API-key cache secret, split by prompt_cache_key when
// given; the SDK derives cache_salt from it. Limited to prefix-cache paths.
func (a *adapter) scopeCache(body map[string]json.RawMessage, raw []byte, path, apiKey string) ([]byte, error) {
	if !strings.HasSuffix(path, "/completions") && !strings.HasSuffix(path, "/responses") {
		return raw, nil
	}
	mac := hmac.New(sha256.New, a.root)
	mac.Write([]byte(apiKey))
	if key := unquote(body[promptCacheKeyField]); key != "" {
		mac = hmac.New(sha256.New, mac.Sum(nil))
		mac.Write([]byte(key))
	}
	body[userCacheSecretField] = json.RawMessage(`"` + hex.EncodeToString(mac.Sum(nil)) + `"`)
	return json.Marshal(body)
}

func (a *adapter) models() []string {
	return slices.Sorted(maps.Keys(*a.catalog.Load()))
}

func writeError(w http.ResponseWriter, status int, message string) {
	kind := "invalid_request_error"
	if status >= 500 {
		kind = "server_error"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"message": message, "type": kind},
	})
}

func unquote(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s
}
