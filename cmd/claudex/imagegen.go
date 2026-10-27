package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/marenzo/claudex/internal/auth"
	"github.com/marenzo/claudex/internal/config"
	"github.com/marenzo/claudex/internal/imagegen"
)

type imagePaths []string

func (p *imagePaths) String() string         { return fmt.Sprint([]string(*p)) }
func (p *imagePaths) Set(value string) error { *p = append(*p, value); return nil }

func cmdImagegen(args []string) error {
	set := flag.NewFlagSet("imagegen", flag.ContinueOnError)
	path := set.String("config", config.DefaultPath(), "claudex configuration path")
	prompt := set.String("prompt", "", "Image request; use - to read the prompt from stdin")
	output := set.String("out", "", "New PNG path; defaults to output/imagegen/<unique-id>.png")
	transparent := set.Bool("transparent", false, "Generate an image with a transparent background")
	asJSON := set.Bool("json", false, "Print the saved image receipt as JSON")
	var images imagePaths
	set.Var(&images, "image", "Local reference or edit target; repeat for up to five images")
	if err := parseFlags(set, args, "usage: claudex ctl imagegen --prompt TEXT [--image PATH ...] [--transparent] [--out PATH] [--json] [--config PATH]"); err != nil {
		return err
	}
	if *prompt == "-" {
		data, err := io.ReadAll(io.LimitReader(os.Stdin, 128001))
		if err != nil {
			return err
		}
		if len(data) > 128000 {
			return errors.New("prompt exceeds size limit")
		}
		*prompt = string(data)
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	ctx, cancel := signalContext()
	defer cancel()
	receipt, err := imagegen.Generate(ctx, imagegen.NewClient(auth.NewStore(cfg.AuthFile)), imagegen.Request{Prompt: *prompt, OutputPath: *output, ReferencedImagePaths: images, TransparentBackground: *transparent})
	if err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(receipt)
	}
	fmt.Printf("Saved native Codex image: %s (%dx%d)\nPrompt: %s\n", receipt.Path, receipt.Width, receipt.Height, receipt.Prompt)
	return nil
}
