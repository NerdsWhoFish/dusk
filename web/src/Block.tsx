import type { ReactNode } from "react";

// Block is the portal's unit of composition (ADR-0013): a titled panel holding
// the result of one query. Plugin views mount into this same shell, so a
// plugin block and a built-in one are indistinguishable to a reader.
export function Block({
  title,
  action,
  wide,
  children,
}: {
  title: string;
  action?: ReactNode;
  wide?: boolean;
  children: ReactNode;
}) {
  return (
    <section className={wide ? "block block-wide" : "block"}>
      <header className="block-head">
        <h2>{title}</h2>
        {action}
      </header>
      {children}
    </section>
  );
}
