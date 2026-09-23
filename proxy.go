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
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

const maxRequestBody = 8 << 20

const (
	userCacheSecretField = "user_cache_secret"
	cacheSaltField       = "cache_salt"
)

type adapter struct {
	models []*model
	root   []byte
}

func newAdapter(models []*model, root []byte) http.Handler {
	a := &adapter{models: models, root: root}
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
		ErrorHandler:  func(w http.ResponseWriter, r *http.Request, err error) {
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
	want := unquote(body["model"])
	var m *model
	for _, served := range a.models {
		if served.name == want {
			m = served
		}
	}
	if m == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("unknown model %q: this endpoint serves %s", want, names(a.models)))
		return
	}
	if raw, err = a.scopeCache(body, raw, r.URL.Path, apiKey); err != nil {
		writeError(w, http.StatusBadRequest, "could not scope prompt cache")
		return
	}

	r.Header.Set("Content-Type", "application/json")
	r.Body = io.NopCloser(bytes.NewReader(raw))
	r.ContentLength = int64(len(raw))
	m.proxy.ServeHTTP(w, r)
}

func (a *adapter) list(w http.ResponseWriter, r *http.Request) {
	data := make([]map[string]string, 0, len(a.models))
	for _, m := range a.models {
		data = append(data, map[string]string{"id": m.name, "object": "model", "owned_by": "tinfoil"})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
}

// scopeCache replaces the SDK's shared default with a per-API-key secret.
// Keep caller-supplied secrets and limit injection to the SDK's prefix-cache paths.
func (a *adapter) scopeCache(body map[string]json.RawMessage, raw []byte, path, apiKey string) ([]byte, error) {
	if !strings.HasSuffix(path, "/completions") && !strings.HasSuffix(path, "/responses") {
		return raw, nil
	}
	mac := hmac.New(sha256.New, a.root)
	mac.Write([]byte(apiKey))
	tenant := mac.Sum(nil)
	if unquote(body[userCacheSecretField]) == "" {
		body[userCacheSecretField] = json.RawMessage(`"` + hex.EncodeToString(tenant) + `"`)
	}
	mac.Reset()
	mac.Write(tenant)
	mac.Write([]byte(unquote(body[userCacheSecretField])))
	body[cacheSaltField] = json.RawMessage(`"` + hex.EncodeToString(mac.Sum(nil)) + `"`)
	return json.Marshal(body)
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
