export type Row = {
  key: string;
  title: string;
  sub?: string;
  tag?: string;
  tagKind?: "rel";
  mono?: boolean;
  onIntent?: () => void;
  onOpen: () => void;
};

export function Rows({ items, empty }: { items: Row[]; empty: string }) {
  if (items.length === 0) {
    return <p className="empty">{empty}</p>;
  }

  return (
    <div className="rows">
      {items.map((item) => (
        <button
          key={item.key}
          className="row"
          type="button"
          onMouseEnter={item.onIntent}
          onFocus={item.onIntent}
          onTouchStart={item.onIntent}
          onClick={item.onOpen}
        >
          <span className="row-main">
            <span className="row-title">{item.title}</span>
            {item.sub && (
              <span className={item.mono ? "row-sub ref" : "row-sub"}>{item.sub}</span>
            )}
          </span>
          {item.tag && (
            <span
              className={item.tagKind === "rel" ? "tag rel" : `tag kind-${item.tag}`}
            >
              {item.tag}
            </span>
          )}
        </button>
      ))}
    </div>
  );
}
