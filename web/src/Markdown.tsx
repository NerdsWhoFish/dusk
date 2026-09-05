import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

// Catalog prose is markdown by definition (ADR-0026). react-markdown builds
// React elements rather than setting innerHTML, so a compromised allowlisted
// account cannot inject markup through a description.
export function Markdown({ children, excerpt = false }: { children: string; excerpt?: boolean }) {
  const heading = ({ children }: { children?: React.ReactNode }) => <p><strong>{children}</strong></p>;
  return (
    <div className="prose">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          ...(excerpt ? { h1: heading, h2: heading, h3: heading, h4: heading, h5: heading, h6: heading } : {}),
          a: ({ href, children }) => (
            <a href={href} rel="noreferrer" target="_blank">
              {children}
            </a>
          ),
        }}
      >
        {children}
      </ReactMarkdown>
    </div>
  );
}
