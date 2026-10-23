# Response formatting

Claude Code renders ordinary Markdown. GPT can also return source-reference
markers that require its provider's renderer, such as
`citeturn0view0`. Claudex translates those markers using the URL annotations
returned with the response.

- A positioned source becomes an inline Markdown link beside its claim. Multiple
  sources in one marker remain together, even when their annotations arrive in
  separate streaming events.
- Native Markdown links remain unchanged. Repeated annotation and completion
  events do not duplicate their links or the answer text.
- Sources without usable positions appear in a separate **Sources** list. Source
  titles and link targets are escaped for Markdown, and only HTTP(S) annotations
  become links.
- A reference without usable source metadata becomes `[source unavailable]`.
  Internal reference IDs are never used to guess URLs.
- Literal markers inside fenced code or inline code remain code examples.

Streaming text before a citation is delivered normally. A partial citation and
following text wait until its source annotations arrive or the content part ends,
so internal IDs do not flash on screen. Complete JSON responses use the same
conversion. Plain text, tool arguments, and encrypted reasoning replay state are
otherwise preserved.

The [OpenAI web-search documentation](https://developers.openai.com/api/docs/guides/tools-web-search)
describes URL annotations with source URLs, titles, and citation locations.
Claudex renders those as Markdown rather than inventing Anthropic-issued
encrypted citation indexes or search-result credentials.
