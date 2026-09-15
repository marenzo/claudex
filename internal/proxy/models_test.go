package proxy

import (
	"fmt"
	"github.com/tidwall/gjson"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFamilyRouting(t *testing.T) {
	s := fixture(t, func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		model := gjson.GetBytes(body, "model").String()
		events := strings.ReplaceAll(sse(created, `{"type":"response.output_text.delta","delta":"OK","output_index":0,"content_index":0}`, `{"type":"response.output_item.done","output_index":0,"item":`+messageItem+`}`, completed), "gpt-6-astra", model)
		return upstreamResponse(200, events), nil
	})
	for input, want := range map[string]string{"haiku": "gpt-5.6-luna", "claude-sonnet-4-6": "gpt-5.6-terra", "opus": "gpt-5.6-sol", "fable": "gpt-6-astra", "gpt-5.6-sol": "gpt-5.6-sol"} {
		for _, stream := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/%t", input, stream), func(t *testing.T) {
				t.Parallel()
				w := request(s, "/v1/messages", fmt.Sprintf(`{"model":%q,"stream":%t,"messages":[{"role":"user","content":"hi"}]}`, input, stream))
				if w.Code != 200 || !strings.Contains(w.Body.String(), `"model":"`+want+`"`) || !strings.Contains(w.Body.String(), "OK") {
					t.Fatalf("%d: %s", w.Code, w.Body)
				}
				if s.Config.Model != "gpt-6-astra" {
					t.Fatal("request changed default model")
				}
			})
		}
	}
}

func TestModelDiscovery(t *testing.T) {
	s := fixture(t, func(*http.Request) (*http.Response, error) { t.Fatal("discovery spent inference"); return nil, nil })
	for path, want := range map[string]string{"/v1/models": "", "/v1/models/haiku": "gpt-5.6-luna", "/v1/models/sonnet": "gpt-5.6-terra", "/v1/models/opus": "gpt-5.6-sol", "/v1/models/fable": "gpt-6-astra"} {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("X-Api-Key", strings.Repeat("k", 32))
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
		if want == "" {
			if gjson.Get(w.Body.String(), "data.#").Int() != 4 {
				t.Fatal(w.Body)
			}
		} else if gjson.Get(w.Body.String(), "id").String() != want {
			t.Fatal(w.Body)
		}
	}
	for _, model := range []string{"luna", "terra", "sol", "astra"} {
		w := request(s, "/v1/messages/count_tokens", fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hello"}]}`, model))
		if w.Code != 200 || gjson.Get(w.Body.String(), "input_tokens").Int() <= 0 {
			t.Fatal(w.Code, w.Body)
		}
	}
}

func TestSelectedModelSurvivesMissingUpstreamModel(t *testing.T) {
	for _, stream := range []bool{false, true} {
		s := fixture(t, func(*http.Request) (*http.Response, error) {
			events := strings.ReplaceAll(sse(created, `{"type":"response.output_item.done","output_index":0,"item":`+messageItem+`}`, completed), `,"model":"gpt-6-astra"`, "")
			return upstreamResponse(200, events), nil
		})
		w := request(s, "/v1/messages", fmt.Sprintf(`{"model":"sonnet","stream":%t,"messages":[]}`, stream))
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"model":"gpt-5.6-terra"`) {
			t.Fatalf("%d: %s", w.Code, w.Body)
		}
	}
}
