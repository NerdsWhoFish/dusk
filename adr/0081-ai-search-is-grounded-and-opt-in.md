# 81. AI search is grounded in a visible catalog slice and opt-in

Date: 2026-08-25

## Status

Accepted

## Context and Problem Statement

Dusk's full-text search answers which entity or note contains some words.
It does not answer the operator's actual questions such as "where does this run", "what is this", or "what do we know about it" without making the operator open and connect several results.

An OpenAI-compatible chat endpoint can synthesize that answer, but adding one creates three boundaries that ordinary search does not have.
Catalog content leaves Dusk for an external provider, the provider must never receive a hidden part of a restricted viewer's catalog, and text from notes and attributes is untrusted input rather than instructions.

Model APIs also fail, cost money, and change independently of Dusk.
Making the existing search path depend on one would turn a fast local index read into a network feature that can disappear.

## Considered Options

1. Call the provider directly from the browser and keep the server out of the path.
2. Add an optional server-side question path grounded in a bounded viewer-visible catalog slice, while ordinary search remains local.
3. Give the model Dusk's MCP tools and let it search, traverse, and act in a tool loop.
4. Add embeddings and a vector index as a second retrieval system.

## Decision Outcome

Chosen: **option 2**.

### Ordinary search remains the default and makes no provider call

The search UI has two explicit modes.
`Search` remains the default, keeps its existing live FTS results, and has no dependency on AI configuration or provider availability.
`Ask AI` is shown only when the deployment has complete AI configuration, and submitting it calls a separate endpoint.

There is no implicit classification that guesses whether text is a query or a question.
The operator chooses whether catalog content may leave Dusk for that request.

### The server owns the provider credential and model allowlist

An OpenAI-compatible Chat Completions base URL, API key, allowed models, and deployment default model are boot configuration.
The browser receives only the provider host, allowlisted model names, and default model.
It never receives the API key or full endpoint.

The allowlist is configuration rather than a live `/models` discovery call.
"OpenAI-compatible" is reliable at the Chat Completions boundary and inconsistent everywhere around it, and a provider adding a model must not silently add an option to Dusk.

The deployment default is the first allowed model unless named explicitly.
The operator may make another allowed model the default in the search UI; that non-sensitive preference lives in browser local storage and is validated against the current allowlist on every load.
This follows [ADR-0072](0072-a-read-checkpoint-belongs-to-the-browser.md): one operator's browser preference does not justify a second durable account-setting store.

### Retrieval is bounded and visibility is applied before prompting

Dusk uses the existing FTS search to identify likely entities and notes, removes common question words, and then resolves those hits against `index.Graph` with the request's `index.Visibility`.
Any search hit absent from that visible graph is discarded before prompt construction.

The prompt includes at most six direct entities and expands one relation hop to at most twelve visible entities.
Descriptions, attributes, attached notes, and relations are individually bounded, and the whole context is capped at 48 KiB.
Every included entity and note receives a source marker returned beside the answer, so the UI can open the exact catalog records the model saw.

Catalog content is labelled as untrusted data in the system prompt.
The model receives no tools, cannot invoke actions, and is told to answer only from the supplied context and to name missing evidence rather than invent it.

### One question is one fresh answer

The first version is not a chat session and does not stream.
Each submission performs a fresh retrieval and one bounded completion, which prevents stale conversation state from silently surviving a catalog change and keeps the provider contract at the widely implemented non-streaming Chat Completions endpoint.

The UI says which provider host receives relevant excerpts and reminds the operator to open the cited sources before acting.

## Consequences

### Good

- Ordinary catalog search stays fast, local, and available when the provider is not.
- The API key never enters browser code, browser storage, catalog git, logs, or an API response.
- A restricted viewer cannot use AI search to summarize refs, notes, relations, or attributes outside their visible sub-catalog.
- A configured model list makes cost and capability changes deliberate rather than provider-controlled.
- Source markers turn an answer into a navigable starting point instead of an uncited blob of prose.
- The same code works with OpenCode Go and any endpoint implementing the used OpenAI Chat Completions subset.

### Bad

- Relevant catalog excerpts are sent to an external provider.
  This is an explicit operator choice in the UI, not a property encryption can mitigate.
- Grounding reduces hallucination and does not eliminate it.
  The answer is not a new source of truth, which is why the UI keeps the source links and warning.
- FTS plus one graph hop is not semantic retrieval.
  A question whose important term appears nowhere in the catalog can produce no useful context even when a human could infer an answer from a broader read.
- A 48 KiB prompt can still carry more context and provider cost than the final answer appears to justify.
- Browser-local defaults do not follow the operator to another browser.
- Non-streaming completions leave a visible wait before the answer appears.

### Rejected because

- **Direct browser calls** were rejected because they expose the provider credential, make the browser reproduce visibility and retrieval rules, and hand CORS configuration control over whether the feature works.
- **An MCP tool loop** was rejected because answering a read-only question does not justify giving generated text a route to proofs and enabled actions.
  It also makes latency, cost, and the exact catalog data disclosed unbounded.
- **A vector index** was rejected because it is a second materialized search system with its own model, rebuild, staleness, and visibility semantics.
  The existing FTS and graph can answer the target questions without creating that parallel source of truth.
