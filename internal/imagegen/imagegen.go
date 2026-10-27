// Package imagegen exposes Codex's subscription image tool to Claude Code.
package imagegen

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/marenzo/claudex/internal/auth"
	"github.com/marenzo/claudex/internal/codex"
)

const Model = "gpt-image-2"
const Timeout = 10 * time.Minute
const MaxImageBytes = 32 << 20
const MaxReferenceBytes = 20 << 20
const maxResponseBytes = 48 << 20

type Request struct {
	Prompt                string
	ReferencedImagePaths  []string
	TransparentBackground bool
	OutputPath            string
}

type Receipt struct {
	Path          string `json:"path"`
	Prompt        string `json:"prompt"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
	Model         string `json:"model"`
	Background    string `json:"background,omitempty"`
	GenerationID  string `json:"generation_id,omitempty"`
	RequestID     string `json:"request_id,omitempty"`
	Width         int    `json:"width"`
	Height        int    `json:"height"`
}

func NewClient(store *auth.Store) *codex.Client {
	c := codex.NewClient(store)
	// Image requests can take several minutes before response headers arrive.
	c.HTTP.Transport.(*http.Transport).ResponseHeaderTimeout = Timeout
	return c
}

// Generate validates local inputs before spending image allowance. Native image
// responses are decoded privately and published atomically without overwrites.
func Generate(ctx context.Context, client *codex.Client, request Request) (Receipt, error) {
	var receipt Receipt
	if !utf8.ValidString(request.Prompt) || strings.TrimSpace(request.Prompt) == "" || utf8.RuneCountInString(request.Prompt) > 32000 {
		return receipt, errors.New("prompt must contain 1 to 32000 characters")
	}
	if len(request.ReferencedImagePaths) > 5 {
		return receipt, errors.New("at most five reference images are supported")
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return receipt, err
	}
	id[6] = id[6]&0x0f | 0x40
	id[8] = id[8]&0x3f | 0x80
	turnID := fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:])
	if request.OutputPath == "" {
		request.OutputPath = filepath.Join("output", "imagegen", turnID+".png")
	}
	output, err := filepath.Abs(request.OutputPath)
	if err != nil {
		return receipt, err
	}
	if !strings.EqualFold(filepath.Ext(output), ".png") {
		return receipt, errors.New("output path must end in .png")
	}
	if _, err := os.Lstat(output); err == nil {
		return receipt, fmt.Errorf("output already exists; choose a new filename: %s", output)
	} else if !os.IsNotExist(err) {
		return receipt, err
	}
	wire := map[string]any{"prompt": request.Prompt, "model": Model, "quality": "auto", "size": "auto", "background": "opaque"}
	if request.TransparentBackground {
		wire["background"] = "transparent"
	}
	var images []map[string]string
	for _, path := range request.ReferencedImagePaths {
		data, err := readBoundedFile(path, MaxReferenceBytes)
		if err != nil {
			return receipt, fmt.Errorf("read reference image: %w", err)
		}
		mime := http.DetectContentType(data)
		if mime != "image/png" && mime != "image/jpeg" && mime != "image/webp" && mime != "image/gif" {
			return receipt, fmt.Errorf("reference must be PNG, JPEG, WebP, or GIF: %s", path)
		}
		images = append(images, map[string]string{"image_url": "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)})
	}
	if len(images) != 0 {
		wire["images"] = images
	}
	if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
		return receipt, err
	}
	// Reserve a writable staging file before the remote request. The destination
	// remains absent until the complete, validated image can be published.
	file, err := os.CreateTemp(filepath.Dir(output), ".imagegen-*.png")
	if err != nil {
		return receipt, err
	}
	temporary := file.Name()
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(temporary)
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	credential, err := client.Auth.Get(ctx)
	if err != nil {
		return receipt, err
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return receipt, err
	}
	response, _, err := client.Images(ctx, credential, body, turnID, len(images) != 0)
	if err != nil {
		return receipt, fmt.Errorf("native image request failed; completion is unknown, check before retrying: %w", err)
	}
	defer response.Body.Close()
	limit := int64(maxResponseBytes)
	if response.StatusCode != http.StatusOK {
		limit = 64 << 10
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return receipt, fmt.Errorf("native image response was interrupted; completion is unknown: %w", err)
	}
	if int64(len(data)) > limit {
		return receipt, errors.New("native image response exceeds size limit")
	}
	requestID := response.Header.Get("X-Codex-Imagegen-Request-Id")
	if response.StatusCode != http.StatusOK {
		var failure struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(data, &failure)
		return receipt, fmt.Errorf("native image request returned HTTP %d (code=%q, request_id=%q): %.1000s", response.StatusCode, failure.Error.Code, requestID, failure.Error.Message)
	}
	var result struct {
		Background string `json:"background"`
		Data       []struct {
			B64           string `json:"b64_json"`
			RevisedPrompt string `json:"revised_prompt"`
			GenerationID  string `json:"generation_id"`
		} `json:"data"`
	}
	if json.Unmarshal(data, &result) != nil || len(result.Data) == 0 || result.Data[0].B64 == "" {
		return receipt, errors.New("native image response contains no usable image")
	}
	image, err := base64.StdEncoding.DecodeString(result.Data[0].B64)
	if err != nil || len(image) > MaxImageBytes {
		return receipt, errors.New("native image data is invalid or exceeds size limit")
	}
	dimensions, err := png.DecodeConfig(bytes.NewReader(image))
	if err != nil {
		return receipt, errors.New("native image response is not a PNG")
	}
	if int64(dimensions.Width)*int64(dimensions.Height) > 32<<20 {
		return receipt, errors.New("native image dimensions exceed size limit")
	}
	if _, err := png.Decode(bytes.NewReader(image)); err != nil {
		return receipt, errors.New("native image response contains an incomplete PNG")
	}
	if _, err := file.Write(image); err != nil {
		return receipt, err
	}
	if err := file.Sync(); err != nil {
		return receipt, err
	}
	if err := file.Close(); err != nil {
		return receipt, err
	}
	// A concurrent creator or symlink cannot turn this into an overwrite.
	if err := os.Link(temporary, output); err != nil {
		keep = true
		return receipt, fmt.Errorf("generated image retained at %s; could not publish %s: %w", temporary, output, err)
	}
	receipt = Receipt{Path: output, Prompt: request.Prompt, RevisedPrompt: result.Data[0].RevisedPrompt, Model: Model, Background: result.Background, GenerationID: result.Data[0].GenerationID, RequestID: requestID, Width: dimensions.Width, Height: dimensions.Height}
	return receipt, nil
}

func readBoundedFile(path string, limit int64) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("reference must be a regular file no larger than 20 MiB")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("reference must be a regular file no larger than 20 MiB")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err == nil && int64(len(data)) > limit {
		err = errors.New("reference image grew beyond size limit")
	}
	return data, err
}
