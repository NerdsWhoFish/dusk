import type { Entity, ViewField, ViewSpec } from "./api";

// DeclaredView renders a plugin's view from a description rather than from its
// JavaScript. It is ADR-0020's first tier: no asset, no shadow DOM, and no
// trust decision, which is what most plugins should be using.
export function DeclaredView({
  spec,
  entities,
  onOpen,
}: {
  spec: ViewSpec;
  entities: Entity[];
  onOpen?: (ref: string) => void;
}) {
  if (entities.length === 0) {
    return <p className="quiet">{spec.empty || "Nothing to show."}</p>;
  }

  switch (spec.layout) {
    case "table":
      return <Table spec={spec} entities={entities} onOpen={onOpen} />;
    case "badges":
      return <Badges spec={spec} entities={entities} />;
    default:
      return <List spec={spec} entities={entities} onOpen={onOpen} />;
  }
}

function Table({
  spec,
  entities,
  onOpen,
}: {
  spec: ViewSpec;
  entities: Entity[];
  onOpen?: (ref: string) => void;
}) {
  return (
    // Wide content scrolls inside its own container rather than widening the
    // page, which is what ADR-0025 asks of anything that can be long.
    <div className="scroller">
      <table className="declared">
        <thead>
          <tr>
            {spec.fields.map((field) => (
              <th key={field.source}>{label(field)}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {entities.map((entity) => (
            <tr key={entity.ref}>
              {spec.fields.map((field) => (
                <td key={field.source}>
                  {/* The label below the breakpoint, where the header row is
                      gone. Real text rather than generated content, so it is
                      read out and selected like everything else. */}
                  <span className="cell-label">{label(field)}</span>
                  <Value field={field} entity={entity} onOpen={onOpen} />
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

// One label for the header and the cell, so a table and the cards it becomes
// cannot end up naming the same column two different things.
function label(field: ViewField): string {
  return field.label || field.source;
}

function List({
  spec,
  entities,
  onOpen,
}: {
  spec: ViewSpec;
  entities: Entity[];
  onOpen?: (ref: string) => void;
}) {
  return (
    <ul className="declared-list">
      {entities.map((entity) => (
        <li key={entity.ref}>
          {spec.fields.map((field) => (
            <span key={field.source}>
              <Value field={field} entity={entity} onOpen={onOpen} />
            </span>
          ))}
        </li>
      ))}
    </ul>
  );
}

function Badges({ spec, entities }: { spec: ViewSpec; entities: Entity[] }) {
  return (
    <div className="declared-badges">
      {entities.map((entity) => (
        <div className="badge-row" key={entity.ref}>
          {spec.fields.map((field) => (
            <span className="badge" key={field.source}>
              <span className="badge-label">{field.label || field.source}</span>
              <Value field={field} entity={entity} />
            </span>
          ))}
        </div>
      ))}
    </div>
  );
}

function Value({
  field,
  entity,
  onOpen,
}: {
  field: ViewField;
  entity: Entity;
  onOpen?: (ref: string) => void;
}) {
  const raw = read(entity, field.source);
  if (raw === undefined || raw === null || raw === "") {
    return <span className="quiet">not set</span>;
  }

  const text = String(raw);
  switch (field.format) {
    case "code":
      return <code>{text}</code>;
    case "badge":
      return <span className="tag">{text}</span>;
    case "timestamp":
      return <time dateTime={text}>{when(text)}</time>;
    case "link":
      return onOpen ? (
        <a
          href={`/entity/${encodeURIComponent(text)}`}
          onClick={(e) => {
            e.preventDefault();
            onOpen(text);
          }}
        >
          {text}
        </a>
      ) : (
        <code>{text}</code>
      );
    default:
      return <>{text}</>;
  }
}

// read resolves a field source. The entity's own fields are reserved names;
// anything else is an attribute key, which is where a plugin puts what this
// schema does not model.
function read(entity: Entity, source: string): unknown {
  switch (source) {
    case "ref":
      return entity.ref;
    case "kind":
      return entity.kind;
    case "namespace":
      return entity.namespace;
    case "name":
      return entity.name;
    case "title":
      return entity.title;
    case "description":
      return entity.description;
    default:
      return entity.attributes?.[source];
  }
}

function when(value: string): string {
  const at = new Date(value);
  return Number.isNaN(at.getTime()) ? value : at.toLocaleString();
}
