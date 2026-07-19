package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"os"
	"strings"
)

// Embedder turns text into vectors for semantic retrieval (ADR-0039). It is separate from the completion
// Provider: embeddings default to a LOCAL endpoint (ollama) so the corpus is never sent off-host to be
// embedded, even when the completion provider is external.
type Embedder interface {
	// Name identifies the embedding backend (for provenance / the stored `model`).
	EmbedName() string
	// Embed returns one vector per input text (same order). All vectors share a dimension.
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// Embed calls an OpenAI-compatible /v1/embeddings endpoint (ollama serves this too). It reuses the
// provider's BaseURL/APIKey/HTTP, so the same config that drives completions can embed.
func (p *OpenAIProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if p.BaseURL == "" {
		return nil, errors.New("llm embed: base URL not set")
	}
	if len(texts) == 0 {
		return nil, nil
	}
	body, err := json.Marshal(map[string]any{"model": p.Model, "input": texts})
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimRight(p.BaseURL, "/") + "/embeddings"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}
	client := p.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= http.StatusBadRequest {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("llm embed %s: %s: %s", p.EmbedName(), resp.Status, string(b))
	}
	var out struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if len(out.Data) != len(texts) {
		return nil, fmt.Errorf("llm embed: got %d vectors for %d inputs", len(out.Data), len(texts))
	}
	vecs := make([][]float32, len(out.Data))
	for i, d := range out.Data {
		vecs[i] = d.Embedding
	}
	return vecs, nil
}

// EmbedName reports the embedding model id, stored alongside each vector.
func (p *OpenAIProvider) EmbedName() string {
	if p.Model != "" {
		return p.Label + ":" + p.Model
	}
	return p.Label
}

// EmbedderFromEnv builds the embedding backend from OSB_EMBED_* — defaulting to a LOCAL ollama server so
// corpus text is embedded on-host. OSB_EMBED_BASE_URL (default http://127.0.0.1:11434/v1), OSB_EMBED_MODEL
// (default nomic-embed-text), OSB_EMBED_API_KEY. Returns an *OpenAIProvider (which is IsLocal for a loopback
// base URL, so it's never an egress risk).
func EmbedderFromEnv() Embedder {
	base := orDefault(os.Getenv("OSB_EMBED_BASE_URL"), "http://127.0.0.1:11434/v1")
	model := orDefault(os.Getenv("OSB_EMBED_MODEL"), "nomic-embed-text")
	return &OpenAIProvider{Label: "embed", BaseURL: base, Model: model, APIKey: os.Getenv("OSB_EMBED_API_KEY")}
}

// mockEmbedder is a deterministic, offline embedder for tests: it hashes tokens into a fixed-dimension
// bag-of-words vector, so texts sharing words land close in cosine space. Not semantic, but enough to
// exercise the index → search pipeline without a live embedding server.
type mockEmbedder struct{ dim int }

// NewMockEmbedder returns a deterministic offline embedder (test/dev only).
func NewMockEmbedder() Embedder { return &mockEmbedder{dim: 64} }

func (m *mockEmbedder) EmbedName() string { return "mock" }

func (m *mockEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, m.dim)
		for _, tok := range strings.Fields(strings.ToLower(t)) {
			h := fnv.New32a()
			_, _ = h.Write([]byte(tok))
			v[h.Sum32()%uint32(m.dim)] += 1
		}
		out[i] = v
	}
	return out, nil
}
