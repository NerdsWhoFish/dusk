import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

// Catalog prose is markdown by definition (ADR-0026). react-markdown builds
// React elements rather than setting innerHTML, so a compromised allowlisted
// account cannot inject markup through a description.
export function Markdown({ children }: { children: string }) {
  return (
    <div className="prose">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
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
