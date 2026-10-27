---
name: imagegen
description: Generate or edit raster images with Codex's native image generation through the existing claudex subscription sign-in. Use for photos, illustrations, textures, sprites, mockups, or transparent cutouts. Prefer existing SVG/vector/code assets when those are the appropriate editable format.
argument-hint: "<image request>"
---

# Native image generation

Use the bundled `"$CLAUDEX_BINARY" ctl imagegen` command with Bash. This invokes
the same subscription image endpoints and `gpt-image-2` model as Codex's native
`image_gen` tool. No `OPENAI_API_KEY`, Python, Node, or native Codex CLI is needed.
The command uses claudex's configured sign-in; never read or print credentials.

1. Identify generation versus editing, and whether the image is a preview or a
   project asset. Preserve a detailed user prompt; add only useful composition,
   style, lighting, and intended-use details to a generic prompt. Preserve exact
   requested text. Do not invent brands, objects, palettes, or narrative details.
2. For every input image, identify its role: edit target, style/reference image,
   or supporting insert. Inspect local inputs with Read before editing. Use
   `--image` once per input, up to five images. Explicitly label their order and
   roles in the prompt. For pathless attachments, obtain a local copy or ask for
   a usable file path; the bridge cannot access Codex's conversation image cache.
3. For edits, state exactly what changes and what must remain unchanged. Preserve
   existing transparency unless the user asks to change it. Use `--transparent`
   for transparent backgrounds and cutouts; retain the output PNG's alpha.
4. Run one command per requested asset or variant, with a Bash timeout of 600000
   milliseconds. Provide a new `.png` path using `--out` when the destination is
   known. Otherwise the command saves a unique file under `output/imagegen/` in
   the current workspace. Existing files are refused before generation.

```sh
"$CLAUDEX_BINARY" ctl imagegen --prompt 'The complete image specification' --out 'assets/hero.png' --json
"$CLAUDEX_BINARY" ctl imagegen --prompt 'Change only the background; preserve the product and its edges' --image 'assets/product.png' --transparent --out 'assets/product-cutout.png' --json
```

5. Read the returned `path` with Read to inspect the actual image. Verify subject,
   composition, text, and edit invariants. Iterate with one targeted change and a
   new output filename. For project assets, use the saved workspace file and
   update consuming references when requested. Report the final path, final
   prompt, and that native Codex subscription image generation was used.

The native model, quality (`auto`), and size (`auto`) match Codex's built-in tool.
Describe resolution and composition in the prompt; these are requests, not
guaranteed pixel dimensions. The receipt reports the actual dimensions.

If generation fails or reaches a usage limit, report the failure. Do not silently
retry an interrupted request, change models, draw a placeholder, or switch to a
paid API fallback. The API-key CLI fallback is a separate workflow requiring the
user's explicit choice. Never claim success without a saved image receipt.
