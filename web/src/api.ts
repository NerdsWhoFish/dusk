export type Entity = {
  ref: string;
  kind: string;
  namespace: string;
  name: string;
  title?: string;
  description?: string;
  attributes?: Record<string, unknown>;
  provenance?: { source?: string; version?: string; observed_at?: string };
};

export type Relation = { type: string; from: string; to: string };

export type GraphNode = {
  ref: string;
  kind: string;
  title: string;
  notes: Note[];
};

export type EstateGraph = {
  nodes: GraphNode[];
  relations: Relation[];
};

export type Note = {
  id: string;
  kind: string;
  body: string;
  pinned?: boolean;
  provenance?: { source?: string; version?: string; observed_at?: string };

  // status closes a note that is work: open, done or dropped. Absent means
  // open, which is what a note written before there was a status is.
  status?: "open" | "done" | "dropped";
};

// Closed is the answer to closing a note. A Dusk that may not write answers
// with the change it would have made instead of a commit (ADR-0052).
export type Closed = {
  note: string;
  status: string;
  commit?: string;
  url?: string;
  proposed?: boolean;
  repository?: string;
  path?: string;
  diff?: string;
};

export type SearchResult = {
  Type: string;
  Ref: string;
  Kind: string;
  Title: string;
  Snippet: string;
  MatchedBy?: "exact" | "keyword" | "semantic" | "hybrid";
};

export type AIConfig = {
  enabled: boolean;
  models: string[];
  default_model?: string;
  provider?: string;
};

export type AISource = {
  type: "entity" | "note";
  ref: string;
  kind: string;
  title: string;
};

export type AIAnswer = {
  answer: string;
  model: string;
  searches: string[];
  sources: AISource[];
};

// ViewField is one thing a declared view shows. Source names an entity field
// or an attribute key; format is how it is drawn.
export type ViewField = {
  source: string;
  label?: string;
  format?: "text" | "code" | "badge" | "link" | "timestamp";
};

// ViewSpec is a view Dusk renders itself, from a description rather than from a
// plugin's JavaScript. It is the tier of ADR-0020 that needs no trust decision.
export type ViewSpec = {
  layout: "table" | "list" | "badges";
  fields: ViewField[];
  empty?: string;
};

// PluginView is a view a plugin contributes: either declared, and rendered
// here, or drawn by the plugin as a custom element. Never a React component
// from a plugin: no shared runtime, so React stays upgradable.
export type PluginView = {
  plugin: string;
  element?: string;
  title?: string;
  source?: string;
  spec?: ViewSpec;

  // problem is why this contribution cannot render where it mounts, shown in
  // its place so a view that was never going to work is not read as an empty
  // answer (ADR-0064).
  problem?: string;
};

// Action is one thing that can be done to something. The same declaration is a
// button here and an invocable capability over MCP (ADR-0041).
export type Action = {
  plugin: string;
  name: string;
  description: string;
  class: "read_only" | "mutating" | "destructive";
  params?: Record<string, unknown>;
  proof_from?: string;
  async: boolean;
  kinds?: string[];
  then?: string[];
  enabled: boolean;
  approval: "automatic" | "confirm";
};

export type Outcome = {
  event: string;
  chain: string;
  plugin: string;
  action: string;
  ref?: string;
  class: string;
  done: boolean;
  ok: boolean;
  unknown?: boolean;
  handle?: string;
  message: string;
  detail?: Record<string, unknown>;
  preview?: string;
  previewed: boolean;
  changed?: string[];
  steps?: Outcome[];
  ask?: Ask;
};

// Ask is a question the plugin returned instead of a result. The action has not
// run; answering it and invoking again is what continues it.
export type Ask = {
  prompt: string;
  schema?: Record<string, unknown>;
  token?: string;
};

export type Answered = {
  outcome: "accept" | "decline" | "cancel";
  values?: Record<string, unknown>;
  token?: string;
};

export type EntityDetail = {
  entity: Entity;
  relations: Relation[];
  notes: Note[];
  views?: PluginView[];
  actions?: Action[];
  sources?: EntitySource[];
  dependents?: Dependent[];
  events?: Event[];

  // proof is the token an action presents, from this very read. The browser
  // meets the same read-before-write contract an agent does (ADR-0009).
  proof?: string;
};

export type NoteDetail = {
  note: Note;
  proof?: string;
};

export type Dependent = { Ref: string; Depth: number };

export type EntitySource = {
  Repository: string;
  Path: string;
  Source: string;
  Version: string;
  Observed: boolean;
};

export type Event = {
  id: string;
  chain?: string;
  plugin?: string;
  ref?: string;
  action: string;
  actor?: string;
  status: "started" | "succeeded" | "failed" | "denied" | "waiting" | "unknown";
  started_at?: string;
  finished_at?: string;
  message?: string;
  detail?: Record<string, unknown>;
};

export type OutputLine = { at: string; stream: string; text: string };

// NeedsApproval is a question rather than a failure: the caller is being asked
// to agree to this particular run, and can offer that instead of an error.
export class NeedsApproval extends Error {}

export type RepositoryStatus = {
  Repository: string;
  GitRef: string;
  Commit: string;
  Entities: number;
  Relations: number;
  Error: string;
  Participating: boolean;
};

// Unauthorized means the session lapsed rather than the request being wrong,
// so it is a distinct type: the app sends the person to sign in again instead
// of showing them an error they cannot act on.
export class Unauthorized extends Error {}

// json refuses a response that is not JSON. A request redirected to the login
// page arrives as HTML with ok: true, and parsing it blames a character offset
// rather than the session that lapsed.
async function json<T>(response: Response): Promise<T> {
  if (response.redirected || !response.headers.get("content-type")?.includes("json")) {
    throw new Unauthorized("session expired");
  }
  return response.json() as Promise<T>;
}

async function get<T>(path: string): Promise<T> {
  const response = await fetch(`/api${path}`, {
    headers: { Accept: "application/json" },
  });

  if (response.status === 401) {
    throw new Unauthorized("session expired");
  }
  if (!response.ok) {
    const body = await response.json().catch(() => ({}));
    throw new Error(body.error ?? `the catalog returned ${response.status}`);
  }
  return json<T>(response);
}

type Refresh<T> = {
  onFresh?: (data: T) => void;
  onError?: (error: unknown) => void;
};

const responseCache = new Map<string, unknown>();
const refreshing = new Map<string, Promise<unknown>>();
let cacheScope: string | undefined;

// cachedGet paints the last answer from this browser session immediately and
// always revalidates it. The cache shortens navigation without turning stale
// data into a silent success: a changed answer replaces what is on screen, and
// a failed refresh reaches the caller's ordinary error state.
function cachedGet<T>(path: string, refresh: Refresh<T> = {}): Promise<T> {
  const cached = cachedResponse<T>(path);
  const fresh = refreshResponse<T>(path);

  if (cached === undefined) {
    return fresh;
  }

  void fresh
    .then((data) => refresh.onFresh?.(data))
    .catch((error) => refresh.onError?.(error));
  return Promise.resolve(cached);
}

function refreshResponse<T>(path: string): Promise<T> {
  const underway = refreshing.get(path) as Promise<T> | undefined;
  if (underway) {
    return underway;
  }

  const request = get<T>(path)
    .then((data) => {
      responseCache.set(path, data);
      try {
        sessionStorage.setItem(cacheKey(path), JSON.stringify(data));
      } catch {
        // Storage may be disabled or full. The in-memory cache still works,
        // and a cache failure must never become a catalog failure.
      }
      return data;
    })
    .finally(() => refreshing.delete(path));
  refreshing.set(path, request);
  return request;
}

function cachedResponse<T>(path: string): T | undefined {
  if (responseCache.has(path)) {
    return responseCache.get(path) as T;
  }
  if (!cacheScope) {
    return undefined;
  }
  try {
    const stored = sessionStorage.getItem(cacheKey(path));
    if (!stored) {
      return undefined;
    }
    const data = JSON.parse(stored) as T;
    responseCache.set(path, data);
    return data;
  } catch {
    return undefined;
  }
}

function invalidate(path: string) {
  responseCache.delete(path);
  try {
    sessionStorage.removeItem(cacheKey(path));
  } catch {
    // See cachedGet: storage is an acceleration, never a requirement.
  }
}

function cacheKey(path: string): string {
  return `dusk:api:${cacheScope}:${path}`;
}

async function invalidating<T>(request: Promise<T>, paths: string[]): Promise<T> {
  const answer = await request;
  for (const path of paths) {
    invalidate(path);
  }
  return answer;
}

export type KindCount = { Kind: string; Count: number };

// KindInfo is what a kind is for and what else it is called. The role is
// resolved server side, so a kind nobody minted still carries one and the
// browser never has to know what the default is (ADR-0048).
export type KindInfo = {
  namespace: string;
  kind: string;
  role: string;
  count?: number;
  aliases?: string[];

  // alias_of names the kind this spelling resolves to. Both halves are said,
  // because a catalog carrying `service` and `svc` has two chips and one kind
  // and that split is the thing worth seeing.
  alias_of?: string;
  minted?: boolean;
};

// Vocabulary carries the roles in rank order, so grouping by them needs no
// copy here of what they are or which comes first.
export type Vocabulary = { roles: string[]; kinds: KindInfo[] };

// Overview is the portal's whole payload. One shape, one request: the landing
// page is the first thing loaded and a waterfall there is what people feel.
export type Overview = {
  kinds: KindCount[];
  total: number;
  declaring: number;
  notes: Note[];
  repositories: RepositoryStatus[];
};

export type Drift = {
  Kind: "declared_not_observed" | "observed_not_declared" | "note_ref_missing";
  Ref: string;
  Title: string;
  Declared: string;
  Observed: string;
  Detail: string;
};

export type Problem = {
  Kind: string;
  Ref: string;
  Detail: string;
  Where: string[];
};

export type Read = {
  repository: string;
  entities: number;
  error?: string;
  observed?: boolean;
};

// ResolvedBlock is one declared query with its result. The server runs the
// query (ADR-0035), so the browser only decides how a result looks.
export type ResolvedBlock = {
  type:
    | "entities"
    | "recent-notes"
    | "drift"
    | "integrity"
    | "kinds"
    | "reads"
    | "graph"
    | "view";
  plugin?: string;
  element?: string;
  ref?: string;
  source?: string;
  spec?: ViewSpec;
  title: string;
  wide?: boolean;
  entities?: Entity[];
  notes?: Note[];
  drift?: Drift[];
  problems?: Problem[];
  kinds?: KindCount[];
  reads?: Read[];
  truncated?: boolean;
  error?: string;
};

export type Home = {
  title: string;
  prose: string;
  search?: boolean;
  blocks: ResolvedBlock[];
  repositories?: RepositoryStatus[];
  problem?: string;

  // proof is the token from this read, which is what closing a note the page
  // showed needs (ADR-0009).
  proof?: string;
};

export type Viewer = {
  signed_in: boolean;
  login?: string;
  restricted: boolean;
  readable?: number;
  github: boolean;
  cache_scope: string;
};

export type PluginField = {
  name: string;
  label: string;
  help?: string;
  type: string;
  required: boolean;
  sensitive: boolean;
};

export type PluginConfig = Record<string, unknown>;

export type PluginHealth = {
  instance?: string;
  entities: number;
  relations: number;
  at: string;
  problem?: string;
};

// PluginProcess is what the supervisor knows about the plugin's own process,
// as against health, which is what its observations did.
export type PluginProcess = {
  phase: "running" | "restarting" | "failed" | "stopped";
  restarts: number;
  attempts?: number;
  exit?: string;

  // Zero when there is nothing to time: no process is running, none has died,
  // or none is due to start.
  since: string;
  exit_at: string;
  next: string;
};

export type PluginOffer = {
  id: string;
  org: string;
  repository: string;
  description: string;
  url: string;
  version?: string;
  installed: boolean;
  installed_version?: string;
  update_available: boolean;
  running: boolean;
  problem?: string;
  process?: PluginProcess;
  fields?: PluginField[];
  actions?: Action[];
  views?: PluginView[];
  config?: PluginConfig;
  instances?: Record<string, PluginConfig>;
  health?: PluginHealth[];

  // set names which sensitive fields hold a value, by instance, with "" being
  // the plugin's own. The names, never the values.
  set?: Record<string, string[]>;
  config_versions?: Record<string, string>;
  config_proofs?: Record<string, string>;
};

async function post<T>(path: string, body?: unknown): Promise<T> {
  const response = await fetch(`/api${path}`, {
    method: "POST",
    headers: { Accept: "application/json", "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });

  if (response.status === 401) {
    throw new Unauthorized("session expired");
  }
  if (!response.ok) {
    const failure = await response.json().catch(() => ({}));
    const message = failure.error ?? `the catalog returned ${response.status}`;
    throw response.status === 409
      ? new NeedsApproval(message)
      : new Error(message);
  }
  return json<T>(response);
}

export const api = {
  viewer: () =>
    get<Viewer>("/viewer").then((viewer) => {
      cacheScope = viewer.cache_scope;
      responseCache.clear();
      return viewer;
    }),
  plugins: () =>
    get<{ plugins: PluginOffer[]; problem?: string; checked?: string }>("/plugins"),
  refreshPlugins: () =>
    post<{ plugins: PluginOffer[]; checked?: string }>("/plugins/refresh"),
  install: (id: string) =>
    invalidating(
      post<{ id: string; version: string }>(`/plugins/${encodeURIComponent(id)}/install`),
      ["/home", "/graph"],
    ),
  uninstall: (id: string) =>
    invalidating(
      post<{ uninstalled: string }>(`/plugins/${encodeURIComponent(id)}/uninstall`),
      ["/home", "/graph"],
    ),
  restartPlugin: (id: string) =>
    invalidating(
      post<{ restarted: string }>(`/plugins/${encodeURIComponent(id)}/restart`),
      ["/home", "/graph"],
    ),
  forget: (scope: string) =>
    invalidating(post<{ forgot: string }>("/observations/forget", { scope }), [
      "/home",
      "/graph",
    ]),
  configure: (
    id: string,
    config: PluginConfig,
    version: string,
    proof: string,
    instance?: string,
  ) =>
    invalidating(
      post<{ configured: string }>(
        instance
          ? `/plugins/${encodeURIComponent(id)}/config/${encodeURIComponent(instance)}`
          : `/plugins/${encodeURIComponent(id)}/config`,
        { settings: config, version, proof },
      ),
      ["/home", "/graph"],
    ),
  home: (refresh?: Refresh<Home>) => cachedGet<Home>("/home", refresh),
  graph: (refresh?: Refresh<EstateGraph>) => cachedGet<EstateGraph>("/graph", refresh),
  drift: () => get<{ drift: Drift[] }>("/drift"),
  overview: () => get<Overview>("/overview"),
  kinds: (refresh?: Refresh<Vocabulary>) => cachedGet<Vocabulary>("/kinds", refresh),
  entities: (kind?: string) =>
    get<{ entities: Entity[] }>(
      kind ? `/entities?kind=${encodeURIComponent(kind)}` : "/entities",
    ),
  // total is what matched, which the limit then cut. Nothing renders it yet.
  search: (query: string) =>
    get<{ results: SearchResult[]; total: number }>(
      `/search?q=${encodeURIComponent(query)}`,
    ),
  ai: () => get<AIConfig>("/ai"),
  ask: (question: string, model: string) =>
    post<AIAnswer>("/ai/ask", { question, model }),
  entity: (ref: string, refresh?: Refresh<EntityDetail>) =>
    cachedGet<EntityDetail>(`/entities/${encodeURIComponent(ref)}`, refresh),
  note: (id: string, refresh?: Refresh<NoteDetail>) =>
    cachedGet<NoteDetail>(`/notes/${encodeURIComponent(id)}`, refresh),
  prefetchEntity: (ref: string) =>
    refreshResponse<EntityDetail>(`/entities/${encodeURIComponent(ref)}`).then(() => undefined),
  prefetchNote: (id: string) =>
    refreshResponse<NoteDetail>(`/notes/${encodeURIComponent(id)}`).then(() => undefined),
  status: () => get<{ repositories: RepositoryStatus[] }>("/status"),

  invoke: (ref: string, action: string, body: Invocation) =>
    invalidating(
      post<Outcome>(
        `/entities/${encodeURIComponent(ref)}/actions/${encodeURIComponent(action)}`,
        body,
      ),
      ["/home", "/graph", `/entities/${encodeURIComponent(ref)}`],
    ),
  invokePlugin: (id: string, action: string, body: Invocation) =>
    invalidating(
      post<Outcome>(
        `/plugins/${encodeURIComponent(id)}/actions/${encodeURIComponent(action)}`,
        body,
      ),
      ["/home", "/graph"],
    ),
  enableAction: (id: string, action: string, enabled: boolean) =>
    post<{ enabled: boolean }>(
      `/plugins/${encodeURIComponent(id)}/actions/${encodeURIComponent(action)}/enabled`,
      { enabled },
    ),
  handle: (id: string, handle: string) =>
    get<Outcome>(
      `/plugins/${encodeURIComponent(id)}/handles/${encodeURIComponent(handle)}`,
    ),
  events: (limit = 50, ref?: string) =>
    get<{ events: Event[] }>(
      `/events?limit=${limit}${ref ? `&ref=${encodeURIComponent(ref)}` : ""}`,
    ),
  closeNote: (id: string, status: "done" | "dropped", proof?: string) =>
    invalidating(post<Closed>("/notes/status", { id, status, proof }), [
      "/home",
      "/graph",
      `/notes/${encodeURIComponent(id)}`,
    ]),
  output: (id: string) =>
    get<{ output: OutputLine[] }>(`/plugins/${encodeURIComponent(id)}/output`),
};

export type Invocation = {
  params?: Record<string, unknown>;
  proof?: string;
  plugin?: string;
  confirm?: boolean;
  preview?: boolean;
  elicited?: Answered;
  idempotency_key?: string;
};
