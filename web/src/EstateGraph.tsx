import type { Core, ElementDefinition } from "cytoscape";
import { useEffect, useMemo, useRef, useState } from "react";

import { api, type EstateGraph as GraphData, type GraphNode } from "./api";
import { Block } from "./Block";
import { Notes } from "./Notes";
import { Rows } from "./Rows";

const firstWave = 160;
const waveSize = 160;

type GraphPosition = { x: number; y: number };

// The route is intentionally smaller than a router, so the homepage unmounts
// while an entity is open. Keep the explorer's working state outside that
// component: opening a node is a drill-down, not a request to start over.
const explorerMemory: {
  query: string;
  limit: number;
  selected?: string;
  positions: Map<string, GraphPosition>;
  pan?: GraphPosition;
  zoom?: number;
  scrollY: number;
  restoreScroll: boolean;
} = {
  query: "",
  limit: firstWave,
  positions: new Map(),
  scrollY: 0,
  restoreScroll: false,
};

export function EstateGraph({
  title,
  onOpen,
}: {
  title: string;
  onOpen: (ref: string) => void;
}) {
  const [graph, setGraph] = useState<GraphData | null>(null);
  const [problem, setProblem] = useState<string>();
  const [selected, setSelected] = useState<string | undefined>(() => explorerMemory.selected);
  const [query, setQuery] = useState(() => explorerMemory.query);
  const [limit, setLimit] = useState(() => explorerMemory.limit);

  useEffect(() => {
    let live = true;
    const scheduler = window as Window & {
      requestIdleCallback?: (callback: () => void, options?: { timeout: number }) => number;
      cancelIdleCallback?: (handle: number) => void;
    };
    const start = () => {
      api
        .graph({
          onFresh: (data) => live && setGraph(data),
          onError: (error) =>
            live && setProblem(error instanceof Error ? error.message : String(error)),
        })
        .then((data) => live && setGraph(data))
        .catch((error) => live && setProblem(error instanceof Error ? error.message : String(error)));
    };
    const idle = scheduler.requestIdleCallback
      ? scheduler.requestIdleCallback(start, { timeout: 500 })
      : setTimeout(start, 0);
    return () => {
      live = false;
      if (scheduler.cancelIdleCallback) {
        scheduler.cancelIdleCallback(idle);
      } else {
        clearTimeout(idle);
      }
    };
  }, []);

  const view = useMemo(
    () => graphView(graph, limit, query, selected),
    [graph, limit, query, selected],
  );
  const chosen = graph?.nodes.find((node) => node.ref === selected);

  useEffect(() => {
    explorerMemory.query = query;
    explorerMemory.limit = limit;
    explorerMemory.selected = selected;
  }, [query, limit, selected]);

  useEffect(() => {
    if (selected) {
      void api.prefetchEntity(selected).catch(() => undefined);
    }
  }, [selected]);

  useEffect(() => {
    if (!graph || !explorerMemory.restoreScroll) {
      return;
    }
    const scrollY = explorerMemory.scrollY;
    explorerMemory.restoreScroll = false;
    requestAnimationFrame(() => scrollTo({ top: scrollY }));
  }, [graph]);

  const openEntity = (ref: string) => {
    explorerMemory.scrollY = scrollY;
    explorerMemory.restoreScroll = true;
    onOpen(ref);
  };

  return (
    <Block
      title={title}
      wide
      action={
        graph ? (
          <span className="graph-count">
            {view.nodes.length} of {graph.nodes.length}
          </span>
        ) : undefined
      }
    >
      <div className="estate-explorer">
        <div className="graph-tools">
          <label className="visually-hidden" htmlFor="graph-search">
            Find an entity in the estate map
          </label>
          <input
            id="graph-search"
            type="search"
            value={query}
            spellCheck={false}
            placeholder="Find anything in the estate"
            onChange={(event) => setQuery(event.target.value)}
          />
          {graph && limit < graph.nodes.length && !query.trim() && (
            <button
              type="button"
              className="btn secondary"
              onClick={() => setLimit((current) => Math.min(current + waveSize, graph.nodes.length))}
            >
              Expand map
            </button>
          )}
        </div>

        {problem ? (
          <p className="problem">The estate map could not load: {problem}</p>
        ) : !graph ? (
          <div className="graph-skeleton skeleton" aria-label="Loading the estate map" />
        ) : graph.nodes.length === 0 ? (
          <p className="quiet">Nothing is in the estate yet.</p>
        ) : (
          <>
            <GraphCanvas view={view} selected={selected} onSelect={setSelected} />
            <GraphList view={view} onSelect={setSelected} />
            <p className="graph-help">
              Drag nodes to untangle a cluster. Scroll to zoom. Search reaches the whole estate,
              including entities outside the current wave.
            </p>
          </>
        )}

        {chosen && (
          <aside className="graph-detail" aria-live="polite">
            <div className="graph-detail-head">
              <div>
                <span className={`tag kind-${chosen.kind}`}>{chosen.kind}</span>
                <h3>{chosen.title || chosen.ref}</h3>
                <p className="ref">{chosen.ref}</p>
              </div>
              <button type="button" className="btn" onClick={() => openEntity(chosen.ref)}>
                Open entity
              </button>
            </div>
            {chosen.notes.length > 0 ? (
              <div className="graph-knowledge">
                <h4>Attached knowledge</h4>
                <Notes notes={chosen.notes} compact />
              </div>
            ) : (
              <p className="quiet">No notes are attached to this entity.</p>
            )}
          </aside>
        )}
      </div>
    </Block>
  );
}

type GraphView = {
  nodes: GraphNode[];
  relations: GraphData["relations"];
};

function graphView(
  graph: GraphData | null,
  limit: number,
  query: string,
  selected?: string,
): GraphView {
  if (!graph) {
    return { nodes: [], relations: [] };
  }

  const degree = new Map<string, number>();
  const neighbors = new Map<string, Set<string>>();
  for (const relation of graph.relations) {
    degree.set(relation.from, (degree.get(relation.from) ?? 0) + 1);
    degree.set(relation.to, (degree.get(relation.to) ?? 0) + 1);
    addNeighbor(neighbors, relation.from, relation.to);
    addNeighbor(neighbors, relation.to, relation.from);
  }

  const term = query.trim().toLowerCase();
  const ordered = [...graph.nodes].sort((left, right) => {
    const byDegree = (degree.get(right.ref) ?? 0) - (degree.get(left.ref) ?? 0);
    return byDegree || left.title.localeCompare(right.title) || left.ref.localeCompare(right.ref);
  });
  const shown = new Set(
    (term
      ? ordered.filter((node) => `${node.title} ${node.ref} ${node.kind}`.toLowerCase().includes(term))
      : ordered.slice(0, limit)
    ).map((node) => node.ref),
  );

  if (selected) {
    shown.add(selected);
    for (const neighbor of neighbors.get(selected) ?? []) {
      shown.add(neighbor);
    }
  }

  return {
    nodes: graph.nodes.filter((node) => shown.has(node.ref)),
    relations: graph.relations.filter(
      (relation) => shown.has(relation.from) && shown.has(relation.to),
    ),
  };
}

function addNeighbor(neighbors: Map<string, Set<string>>, from: string, to: string) {
  const existing = neighbors.get(from) ?? new Set<string>();
  existing.add(to);
  neighbors.set(from, existing);
}

function GraphCanvas({
  view,
  selected,
  onSelect,
}: {
  view: GraphView;
  selected?: string;
  onSelect: (ref: string) => void;
}) {
  const container = useRef<HTMLDivElement>(null);
  const instance = useRef<Core | null>(null);

  useEffect(() => {
    if (!container.current || matchMedia("(pointer: coarse)").matches) {
      return;
    }

    let disposed = false;
    const target = container.current;

    const elements: ElementDefinition[] = [
      ...view.nodes.map((node) => ({
        data: {
          id: node.ref,
          label: node.title || node.ref,
          kind: node.kind,
          knowledge: node.notes.length,
        },
      })),
      ...view.relations.map((relation, index) => ({
        data: {
          id: `edge-${index}-${relation.from}-${relation.to}`,
          source: relation.from,
          target: relation.to,
          label: relation.type,
        },
      })),
    ];

    void import("cytoscape").then(({ default: cytoscape }) => {
      if (disposed) {
        return;
      }
      const cy = cytoscape({
        container: target,
        elements,
        minZoom: 0.08,
        maxZoom: 2.5,
        wheelSensitivity: 0.18,
        style: [
        {
          selector: "node",
          style: {
            "background-color": "#bd93f9",
            "border-color": "#3b3549",
            "border-width": 2,
            color: "#f8f8f2",
            label: "data(label)",
            "font-family": "ui-sans-serif, system-ui, sans-serif",
            "font-size": 10,
            "min-zoomed-font-size": 7,
            "text-background-color": "#22212c",
            "text-background-opacity": 0.86,
            "text-background-padding": "3px",
            "text-margin-y": 12,
            width: "mapData(knowledge, 0, 8, 18, 34)",
            height: "mapData(knowledge, 0, 8, 18, 34)",
          },
        },
        {
          selector: "node[knowledge > 0]",
          style: { "border-color": "#ffca80", "border-width": 3 },
        },
        {
          selector: "node:selected",
          style: {
            "background-color": "#50fa7b",
            "border-color": "#f8f8f2",
            "border-width": 4,
          },
        },
        {
          selector: "edge",
          style: {
            width: 1.25,
            "line-color": "#7970a9",
            "target-arrow-color": "#7970a9",
            "target-arrow-shape": "triangle",
            "curve-style": "bezier",
            opacity: 0.62,
          },
        },
        ],
        layout: {
          name: "cose",
          animate: false,
          fit: true,
          padding: 32,
          nodeRepulsion: () => 5200,
          idealEdgeLength: () => 72,
        },
      });
      cy.nodes().forEach((node) => {
        const position = explorerMemory.positions.get(node.id());
        if (position) {
          node.position(position);
        }
      });
      if (explorerMemory.pan && explorerMemory.zoom !== undefined) {
        cy.viewport({ pan: explorerMemory.pan, zoom: explorerMemory.zoom });
      }
      const rememberedSelection = explorerMemory.selected;
      if (rememberedSelection) {
        cy.getElementById(rememberedSelection).select();
      }
      cy.on("tap", "node", (event) => onSelect(event.target.id()));
      instance.current = cy;
    });
    return () => {
      disposed = true;
      instance.current?.nodes().forEach((node) => {
        explorerMemory.positions.set(node.id(), node.position());
      });
      if (instance.current) {
        explorerMemory.pan = instance.current.pan();
        explorerMemory.zoom = instance.current.zoom();
      }
      instance.current?.destroy();
      instance.current = null;
    };
  }, [view, onSelect]);

  useEffect(() => {
    if (!instance.current || !selected) {
      return;
    }
    instance.current.$(":selected").unselect();
    const node = instance.current.getElementById(selected);
    node.select();
    instance.current.animate({ center: { eles: node }, duration: 220 });
  }, [selected]);

  return <div className="graph-canvas" ref={container} aria-label="Interactive estate graph" />;
}

function GraphList({ view, onSelect }: { view: GraphView; onSelect: (ref: string) => void }) {
  return (
    <div className="graph-list">
      <Rows
        items={view.nodes.map((node) => ({
          key: node.ref,
          title: node.title || node.ref,
          sub: node.ref,
          mono: true,
          tag: node.notes.length > 0 ? `${node.notes.length} notes` : node.kind,
          onOpen: () => onSelect(node.ref),
        }))}
        empty="Nothing matches this map search."
      />
    </div>
  );
}
