# AI search

Dusk can optionally answer natural-language questions from the catalog through an OpenAI-compatible Chat Completions endpoint.
Ordinary search remains the default and never calls that provider.

## What the provider receives

Submitting in **Ask AI** mode gives the configured model two read-only tools.
`search_estate` finds visible entities and notes, and `read_document` opens one exact visible record.
The model may search repeatedly with different vocabulary and follow entity relations or attached-note handles before answering.

The UI names the provider host before a question is submitted.
Search results contain handles and titles rather than document bodies, so the model must open a record before using it as evidence.
The server caps the conversation at eight provider rounds, sixteen total tool calls, six searches, twelve results per search, twelve document reads, 8 KiB per document, and 64 KiB across tool results.
It resolves every search result and document read against the current viewer's visible graph.
The provider receives no Dusk credential, proof token, mutation, plugin action, network access, or general MCP tool.

Catalog content is treated as untrusted data rather than instructions, but a model can still be wrong.
Each answer shows every search query and links every entity and note the agent opened.
Those documents are the complete evidence trail supplied through the read tool and should be checked before acting.

## Configure it

AI search is disabled unless all required variables are set.

| Variable | Required | Purpose |
| --- | --- | --- |
| `DUSK_AI_BASE_URL` | yes | OpenAI-compatible API base URL including `/v1` |
| `DUSK_AI_API_KEY` | yes | Provider bearer token, retained only by the server |
| `DUSK_AI_MODELS` | yes | Comma-separated model allowlist shown in search |
| `DUSK_AI_DEFAULT_MODEL` | no | Deployment default; the first allowed model when unset |

For OpenCode Go:

```text
DUSK_AI_BASE_URL=https://opencode.ai/zen/go/v1
DUSK_AI_API_KEY=...
DUSK_AI_MODELS=qwen3.8-max,deepseek-v4-flash,glm-5.2,mimo-v2.5,kimi-k3
DUSK_AI_DEFAULT_MODEL=qwen3.8-max
```

The server calls `POST {DUSK_AI_BASE_URL}/chat/completions` with a bearer token, `model`, `messages`, the two function tools, and a 4,096-token completion ceiling.
That ceiling leaves room for providers whose reasoning tokens count against `max_completion_tokens` before visible answer text is emitted.
The endpoint is deliberately not queried for a live model catalog because that part of OpenAI compatibility is inconsistent and would let provider-side changes silently alter Dusk's UI.

## Model defaults

The deployment default is selected on a browser's first visit.
Choosing another model affects the current question only until **Set as default** is pressed.
That preference is a model name in browser local storage, never a credential, and is ignored if the deployment no longer allows it.

The preference does not synchronize between browsers.
There is no server-side user-settings store to keep in step, in the same single-operator posture as the read checkpoint in [ADR-0072](../adr/0072-a-read-checkpoint-belongs-to-the-browser.md).

## Failure behavior

An unavailable provider affects only **Ask AI** mode.
Ordinary search and every other catalog read continue to use the local index.

Provider calls have a 60-second HTTP timeout and a 2 MiB response limit.
Question bodies are capped at 8 KiB and questions at 2,000 characters.
The browser displays a generic provider failure while the server logs the bounded provider error without the API key or catalog prompt.

The opt-in and provider boundary are in [ADR-0081](../adr/0081-ai-search-is-grounded-and-opt-in.md).
The bounded estate-agent decision is in [ADR-0082](../adr/0082-ai-search-uses-a-bounded-read-only-estate-agent.md).
