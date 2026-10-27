# Native image generation

Start or resume Claude Code through a claudex build containing this feature, then
enter an image request with the bundled skill:

```text
/claudex:imagegen Create a watercolor illustration of a fox for assets/fox.png
```

The skill calls claudex's image command, saves the PNG, and inspects it with Claude
Code's Read tool. It can also activate when a task calls for raster images. No
separate plugin installation, Codex CLI, Python, Node, or `OPENAI_API_KEY` is needed.
The command uses the existing claudex Codex subscription sign-in and consumes its
image allowance. Generation remains subject to normal Claude Code tool permissions.

## Terminal commands

```sh
claudex ctl imagegen --prompt 'A watercolor fox, no text' --out assets/fox.png
claudex ctl imagegen --prompt 'Remove only the background; preserve the fox' \
  --image assets/fox.png --transparent --out assets/fox-cutout.png --json
```

Use `--image` for an edit target or a reference image, repeating it for up to five
local inputs. Label their roles in the prompt. PNG, JPEG, WebP, and GIF inputs are
accepted, up to 20 MiB each. Pathless conversation images must first be saved
locally. A source image stays unchanged; choose a new destination for the result.

Use `--prompt -` to read a prompt from stdin, up to 32,000 characters. `--config`
selects a claudex config; `CLAUDEX_CONFIG` also works. Secrets stay in the configured
credential store, outside prompts and command arguments.

The default destination is a unique `output/imagegen/<id>.png` under the current
directory. `--out` selects a destination. Existing files and symlinks are refused;
the complete image is published atomically. If another process creates the target
during generation, the error reports where the generated image was retained.

`--json` returns the absolute path, final submitted prompt, actual dimensions,
model, backend background metadata, and available request/generation identifiers.
Base64 image data is decoded directly into the file and is never printed. PNG
bytes and alpha are preserved. Use `--transparent` when the output should have
transparency, including edits whose existing transparency should remain.

## Native contract and failures

This Go implementation follows Codex's native `image_gen` tool: subscription
`/codex/images/generations` and `/codex/images/edits`, `gpt-image-2`, `quality: auto`,
and `size: auto`. It does not route through the public API-key Images API. Describe
desired resolution in the prompt; the receipt reports the actual result rather
than promising an exact size. The subscription endpoints are not a stable public
API, so backend changes can require an update.

The request has a ten-minute deadline. Authentication rejection permits one token
refresh using claudex's credential lock; an account change aborts the retry.
Generation errors, quota limits, and interrupted requests are not automatically
retried. An interrupted request can have consumed allowance, so check before
retrying. Invalid responses and incomplete images are never published as results.
There is no automatic model change or paid API fallback.

Image commands run directly against the subscription image service; their calls
are separate from the gateway's text-request metrics in the dashboard.

The native behavior is grounded in OpenAI's
[image-generation tool](https://github.com/openai/codex/blob/main/codex-rs/ext/image-generation/src/tool.rs)
and [wire-contract tests](https://github.com/openai/codex/blob/main/codex-rs/app-server/tests/suite/v2/imagegen_extension.rs).
