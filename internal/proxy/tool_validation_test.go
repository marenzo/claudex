package proxy

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestInvalidCompletedToolArgumentsFail(t *testing.T) {
	for _, args := range []string{"{", "[]", "null", `{"broken":`} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", args, stream), func(t *testing.T) {
				s := fixture(t, func(*http.Request) (*http.Response, error) {
					return upstreamResponse(200, sse(created, fmt.Sprintf(`{"type":"response.completed","response":{"output":[{"type":"function_call","name":"Read","call_id":"x","arguments":%q}]}}`, args))), nil
				})
				w := request(s, "/v1/messages", fmt.Sprintf(`{"stream":%t,"messages":[]}`, stream))
				if w.Code != 502 || !strings.Contains(w.Body.String(), "invalid tool arguments") {
					t.Fatalf("%d: %s", w.Code, w.Body)
				}
			})
		}
	}
}
