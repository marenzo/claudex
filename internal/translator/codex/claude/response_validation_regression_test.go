package claude

import (
	"fmt"
	"testing"
)

func TestToolIdentityCannotChangeMidStream(t *testing.T) {
	for _, field := range []string{"name", "call_id"} {
		t.Run(field, func(t *testing.T) {
			s := NewStream("sol", nil)
			_, err := s.Convert([]byte(`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_same","call_id":"toolu_first","name":"Read","arguments":""}}`))
			if err != nil {
				t.Fatal(err)
			}
			name, id := "Read", "toolu_first"
			if field == "name" {
				name = "Bash"
			} else {
				id = "toolu_other"
			}
			_, err = s.Convert([]byte(fmt.Sprintf(`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc_same","call_id":%q,"name":%q,"arguments":"{}"}}`, id, name)))
			if err == nil {
				t.Fatal("changed identity accepted")
			}
		})
	}
}

func TestAmbiguousArgumentsRejectedWithoutReviewer(t *testing.T) {
	for _, args := range []string{`{"path":"safe","path":"other"}`, `{"path":"safe","\u0070ath":"other"}`, `{"nested":{"path":"safe","path":"other"}}`} {
		raw := []byte(fmt.Sprintf(`{"type":"response.completed","response":{"output":[{"type":"function_call","name":"Read","call_id":"toolu_a","arguments":%q}]}}`, args))
		if _, err := ConvertResponse("sol", nil, raw); err == nil {
			t.Fatal("ambiguous arguments accepted")
		}
	}
}
