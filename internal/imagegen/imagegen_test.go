package imagegen

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marenzo/claudex/internal/auth"
	"github.com/marenzo/claudex/internal/codex"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func fixture(t *testing.T) (*codex.Client, []byte, string) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "auth.json")
	if err := os.WriteFile(path, []byte(`{"type":"codex","account_id":"fixture-account","access_token":"fixture-access"}`), 0600); err != nil {
		t.Fatal(err)
	}
	img := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	img.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 128})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return NewClient(auth.NewStore(path)), b.Bytes(), filepath.Join(root, "asset.png")
}

func response(data []byte) *http.Response {
	body, _ := json.Marshal(map[string]any{"background": "transparent", "data": []any{map[string]any{"b64_json": base64.StdEncoding.EncodeToString(data), "generation_id": "fixture-generation"}}})
	return &http.Response{StatusCode: 200, Header: http.Header{"X-Codex-Imagegen-Request-Id": []string{"fixture-request"}}, Body: io.NopCloser(bytes.NewReader(body))}
}

func TestNativeGenerationAndEditsPreserveBytesAndContract(t *testing.T) {
	for _, edit := range []bool{false, true} {
		t.Run(map[bool]string{false: "generate", true: "edit"}[edit], func(t *testing.T) {
			client, pngBytes, output := fixture(t)
			var refs []string
			if edit {
				for _, name := range []string{"first.png", "second.png"} {
					path := filepath.Join(filepath.Dir(output), name)
					if err := os.WriteFile(path, pngBytes, 0600); err != nil {
						t.Fatal(err)
					}
					refs = append(refs, path)
				}
			}
			calls := 0
			client.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				path := "/backend-api/codex/images/generations"
				if edit {
					path = "/backend-api/codex/images/edits"
				}
				if r.URL.Path != path || r.Header.Get("Accept") != "application/json" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Authorization") != "Bearer fixture-access" || r.Header.Get("Chatgpt-Account-Id") != "fixture-account" || len(r.Header.Get("X-Codex-Image-Turn-Id")) != 36 {
					t.Fatalf("wrong native request: %s, headers=%v", r.URL, r.Header)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body["model"] != Model || body["prompt"] != "preserve the subject" || body["size"] != "auto" || body["quality"] != "auto" || body["background"] != "transparent" {
					t.Fatalf("wrong native body: %v", body)
				}
				if edit {
					images := body["images"].([]any)
					if len(images) != 2 || images[0].(map[string]any)["image_url"] != "data:image/png;base64,"+base64.StdEncoding.EncodeToString(pngBytes) {
						t.Fatal("reference bytes changed")
					}
				} else if _, ok := body["images"]; ok {
					t.Fatal("unexpected edit inputs")
				}
				return response(pngBytes), nil
			})
			receipt, err := Generate(context.Background(), client, Request{Prompt: "preserve the subject", OutputPath: output, ReferencedImagePaths: refs, TransparentBackground: true})
			if err != nil {
				t.Fatal(err)
			}
			saved, err := os.ReadFile(receipt.Path)
			if err != nil || !bytes.Equal(saved, pngBytes) || receipt.Width != 2 || receipt.Height != 1 || receipt.Background != "transparent" || receipt.RequestID != "fixture-request" || receipt.GenerationID != "fixture-generation" || calls != 1 {
				t.Fatalf("bad receipt or changed PNG: %+v, %v", receipt, err)
			}
			if _, err := Generate(context.Background(), client, Request{Prompt: "another image", OutputPath: output}); err == nil || calls != 1 {
				t.Fatal("existing image was overwritten or requested again")
			}
		})
	}
}

func TestInvalidInputsFailBeforeNativeRequest(t *testing.T) {
	for _, scenario := range []string{"empty", "references", "extension", "not_image", "large_reference", "directory", "symlink_output"} {
		t.Run(scenario, func(t *testing.T) {
			client, _, output := fixture(t)
			client.HTTP.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Fatal("invalid request reached provider")
				return nil, nil
			})
			r := Request{Prompt: "a circle", OutputPath: output}
			switch scenario {
			case "empty":
				r.Prompt = " "
			case "references":
				r.ReferencedImagePaths = make([]string, 6)
			case "extension":
				r.OutputPath += ".jpg"
			case "directory":
				r.ReferencedImagePaths = []string{filepath.Dir(output)}
			case "symlink_output":
				if err := os.Symlink(filepath.Join(filepath.Dir(output), "missing"), output); err != nil {
					t.Fatal(err)
				}
			default:
				path := filepath.Join(filepath.Dir(output), "input")
				if err := os.WriteFile(path, []byte("not an image"), 0600); err != nil {
					t.Fatal(err)
				}
				if scenario == "large_reference" {
					if err := os.Truncate(path, MaxReferenceBytes+1); err != nil {
						t.Fatal(err)
					}
				}
				r.ReferencedImagePaths = []string{path}
			}
			if _, err := Generate(context.Background(), client, r); err == nil {
				t.Fatal("invalid input accepted")
			}
		})
	}
}

func TestFailureDoesNotPublishOrRetry(t *testing.T) {
	for _, scenario := range []string{"quota", "server", "invalid", "truncated_png", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			client, pngBytes, output := fixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			client.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if scenario == "cancelled" {
					cancel()
					return nil, r.Context().Err()
				}
				if scenario == "quota" || scenario == "server" {
					status := 429
					if scenario == "server" {
						status = 502
					}
					return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"usage_limit_reached","message":"image allowance exhausted"}}`))}, nil
				}
				if scenario == "invalid" {
					return response([]byte("not a PNG")), nil
				}
				return response(pngBytes[:len(pngBytes)-12]), nil
			})
			_, err := Generate(ctx, client, Request{Prompt: "a circle", OutputPath: output})
			if err == nil || calls != 1 {
				t.Fatalf("failure accepted or retried: %v, %d", err, calls)
			}
			if scenario == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if scenario == "quota" && !strings.Contains(err.Error(), "usage_limit_reached") {
				t.Fatal("quota reason lost", err)
			}
			if _, err := os.Lstat(output); !os.IsNotExist(err) {
				t.Fatal("failed output was published")
			}
			files, _ := filepath.Glob(filepath.Join(filepath.Dir(output), ".imagegen-*"))
			if len(files) != 0 {
				t.Fatal("failed staging files retained", files)
			}
		})
	}
}

func TestConcurrentOutputCreationPreservesBothImages(t *testing.T) {
	client, pngBytes, output := fixture(t)
	client.HTTP.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		if err := os.WriteFile(output, []byte("concurrent work"), 0600); err != nil {
			t.Fatal(err)
		}
		return response(pngBytes), nil
	})
	_, err := Generate(context.Background(), client, Request{Prompt: "a circle", OutputPath: output})
	if err == nil || !strings.Contains(err.Error(), "retained at") {
		t.Fatal("missing recoverable image", err)
	}
	saved, _ := os.ReadFile(output)
	if string(saved) != "concurrent work" {
		t.Fatal("concurrent output replaced")
	}
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(output), ".imagegen-*"))
	if len(files) != 1 {
		t.Fatal("generated image not retained")
	}
	saved, _ = os.ReadFile(files[0])
	if !bytes.Equal(saved, pngBytes) {
		t.Fatal("generated image corrupted")
	}
}
