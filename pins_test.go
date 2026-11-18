package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	tinfoil "github.com/tinfoilsh/tinfoil-go"
)

const (
	pinnedModel     = "glm-5-3"
	redirectedModel = "llama3-3-70b"
	unpinnedModel   = "not-a-pinned-model"
	otherRepo       = "tinfoilsh/confidential-debian"
	replicaHost     = "replica.example.com"
)

func TestOnlyPinnedRepositoriesAreServed(t *testing.T) {
	base, err := url.Parse("https://gateway.example.com")
	if err != nil {
		t.Fatal(err)
	}
	catalog := tinfoil.Catalog{
		pinnedModel:     {Repo: modelRepos[pinnedModel], Hosts: []string{replicaHost}},
		redirectedModel: {Repo: otherRepo, Hosts: []string{replicaHost}},
		unpinnedModel:   {Repo: otherRepo, Hosts: []string{replicaHost}},
	}
	root := make([]byte, 32)
	gw, err := newGateway(base, func() tinfoil.Catalog { return catalog }, root)
	if err != nil {
		t.Fatal(err)
	}
	handler := newAdapter(gw, base, root)

	listed := httptest.NewRecorder()
	handler.ServeHTTP(listed, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	var models struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &models); err != nil {
		t.Fatal(err)
	}
	if len(models.Data) != 1 || models.Data[0].ID != pinnedModel {
		t.Fatalf("listed models = %+v, want only %q", models.Data, pinnedModel)
	}

	for _, model := range []string{redirectedModel, unpinnedModel} {
		body := strings.NewReader(`{"model":"` + model + `","messages":[]}`)
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body)
		req.Header.Set("Authorization", "Bearer test-key")
		refused := httptest.NewRecorder()
		handler.ServeHTTP(refused, req)
		if refused.Code != http.StatusNotFound {
			t.Errorf("model %q: status = %d, want %d", model, refused.Code, http.StatusNotFound)
		}
	}
}
