package agent

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Autumn-27/norma/llm"
)

func TestProviderRotatesKeysAcrossAllFormatsAndModes(t *testing.T) {
	for _, format := range []string{"openai", "openai-responses", "anthropic"} {
		for _, streaming := range []bool{false, true} {
			mode := "complete"
			if streaming {
				mode = "stream"
			}
			t.Run(format+"/"+mode, func(t *testing.T) {
				var headers []string
				client := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
					key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
					if format == "anthropic" {
						key = r.Header.Get("x-api-key")
					}
					headers = append(headers, key)
					return &http.Response{StatusCode: 401, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":{"message":"test"}}`))}, nil
				})}
				first, second := t.Name()+"-a", t.Name()+"-b"
				cfg := ConfigFrom(format, "test", "http://example.invalid", first+"\n"+second, "")
				cfg.Retry.ConnectAttempts = -1
				prov, err := cfg.newProvider(client, nil)
				if err != nil {
					t.Fatal(err)
				}
				for range 2 {
					req := llm.CompletionRequest{Messages: []llm.Message{llm.UserText("test")}}
					if streaming {
						for range prov.Stream(context.Background(), req) {
						}
					} else {
						prov.Complete(context.Background(), req)
					}
				}
				if len(headers) != 2 || headers[0] != first || headers[1] != second {
					t.Fatalf("keys = %v", headers)
				}
			})
		}
	}
}
