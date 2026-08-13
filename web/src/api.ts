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

export type Note = {
  id: string;
  kind: string;
  body: string;
  pinned?: boolean;

  // status closes a note that is work: open, done or dropped. Absent means
  // open, which is what a note written before there was a status is.
  status?: "open" | "done" | "dropped";
};

export type SearchResult = {
  Type: string;
  Ref: string;
  Kind: string;
  Title: string;
  Snippet: string;
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
  handle?: string;
  message: string;
  detail?: Record<string, unknown>;
  preview?: string;
  previewed: boolean;
  changed?: string[];
  steps?: Outcome[];
};

export type EntityDetail = {
  entity: Entity;
  relations: Relation[];
  notes: Note[];
  views?: PluginView[];
  actions?: Action[];

  // proof is the token an action presents, from this very read. The browser
  // meets the same read-before-write contract an agent does (ADR-0009).
  proof?: string;
};

export type Event = {
  id: string;
  chain?: string;
  plugin?: string;
  ref?: string;
  action: string;
  actor?: string;
  status: "started" | "succeeded" | "failed" | "denied" | "unknown";
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

export type KindCount = { Kind: string; Count: number };

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
  Kind: "declared_not_observed" | "observed_not_declared";
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
    | "view";
  plugin?: string;
  element?: string;
  ref?: string;
  source?: string;
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
  fields?: PluginField[];
  actions?: Action[];
  views?: PluginView[];
  config?: PluginConfig;
  instances?: Record<string, PluginConfig>;
  health?: PluginHealth[];

  // set names which sensitive fields hold a value, by instance, with "" being
  // the plugin's own. The names, never the values.
  set?: Record<string, string[]>;
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
  viewer: () => get<Viewer>("/viewer"),
  plugins: () =>
    get<{ plugins: PluginOffer[]; problem?: string; checked?: string }>("/plugins"),
  refreshPlugins: () =>
    post<{ plugins: PluginOffer[]; checked?: string }>("/plugins/refresh"),
  install: (id: string) =>
    post<{ id: string; version: string }>(
      `/plugins/${encodeURIComponent(id)}/install`,
    ),
  uninstall: (id: string) =>
    post<{ uninstalled: string }>(`/plugins/${encodeURIComponent(id)}/uninstall`),
  forget: (scope: string) =>
    post<{ forgot: string }>("/observations/forget", { scope }),
  configure: (id: string, config: PluginConfig, instance?: string) =>
    post<{ configured: string }>(
      instance
        ? `/plugins/${encodeURIComponent(id)}/config/${encodeURIComponent(instance)}`
        : `/plugins/${encodeURIComponent(id)}/config`,
      config,
    ),
  home: () => get<Home>("/home"),
  drift: () => get<{ drift: Drift[] }>("/drift"),
  overview: () => get<Overview>("/overview"),
  entities: (kind?: string) =>
    get<{ entities: Entity[] }>(
      kind ? `/entities?kind=${encodeURIComponent(kind)}` : "/entities",
    ),
  search: (query: string) =>
    get<{ results: SearchResult[] }>(`/search?q=${encodeURIComponent(query)}`),
  entity: (ref: string) =>
    get<EntityDetail>(`/entities/${encodeURIComponent(ref)}`),
  status: () => get<{ repositories: RepositoryStatus[] }>("/status"),

  invoke: (ref: string, action: string, body: Invocation) =>
    post<Outcome>(
      `/entities/${encodeURIComponent(ref)}/actions/${encodeURIComponent(action)}`,
      body,
    ),
  invokePlugin: (id: string, action: string, body: Invocation) =>
    post<Outcome>(
      `/plugins/${encodeURIComponent(id)}/actions/${encodeURIComponent(action)}`,
      body,
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
  events: (limit = 50) => get<{ events: Event[] }>(`/events?limit=${limit}`),
  closeNote: (id: string, status: "done" | "dropped", proof?: string) =>
    post<{ note: string }>("/notes/status", { id, status, proof }),
  output: (id: string) =>
    get<{ output: OutputLine[] }>(`/plugins/${encodeURIComponent(id)}/output`),
};

export type Invocation = {
  params?: Record<string, unknown>;
  proof?: string;
  plugin?: string;
  confirm?: boolean;
  preview?: boolean;
};
