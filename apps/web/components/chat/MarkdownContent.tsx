import type { ReactNode } from "react";
import { lexer } from "marked";
import type { Token, Tokens } from "marked";

import type { Message } from "../../lib/api";

export function renderMarkdown(content: string) {
  return <div className="markdown">{renderMarkdownTokens(lexer(content))}</div>;
}

export function MessageCitations({ citations }: { citations?: Message["citations"] }) {
  if (!citations || citations.length === 0) {
    return null;
  }
  return (
    <section className="message-citations" aria-label="Source details">
      <div className="message-citations-title">Source details</div>
      <ol>
        {citations.map((citation) => {
          const location = citation.section_path?.filter(Boolean).join(" / ");
          const sourceCount = citation.source_chunk_ids?.length ?? 0;
          return (
            <li key={citation.source_id}>
              <span className="citation-source-id">[{citation.source_id}]</span>
              <span>{citation.document_title || citation.document_id}</span>
              {location ? <span className="citation-location">{location}</span> : null}
              {sourceCount > 1 ? <span className="citation-location">{sourceCount} chunks</span> : null}
            </li>
          );
        })}
      </ol>
    </section>
  );
}

export function renderMarkdownTokens(tokens: Token[]) {
  return tokens.map((token, index) => renderMarkdownToken(token, `md-${index}`));
}

function renderMarkdownToken(token: Token, key: string): ReactNode {
  switch (token.type) {
    case "space":
    case "def":
      return null;
    case "heading":
      return renderMarkdownHeading(token.depth, tokenChildren(token), key);
    case "paragraph":
      return <p key={key}>{renderMarkdownTokens(tokenChildren(token))}</p>;
    case "text":
      if ("tokens" in token && token.tokens) {
        return <span key={key}>{renderMarkdownTokens(token.tokens)}</span>;
      }
      return <span key={key}>{token.text}</span>;
    case "strong":
      return <strong key={key}>{renderMarkdownTokens(tokenChildren(token))}</strong>;
    case "em":
      return <em key={key}>{renderMarkdownTokens(tokenChildren(token))}</em>;
    case "del":
      return <del key={key}>{renderMarkdownTokens(tokenChildren(token))}</del>;
    case "codespan":
      return <code key={key}>{token.text}</code>;
    case "br":
      return <br key={key} />;
    case "code":
      return (
        <pre className="markdown-code" key={key}>
          <code data-language={token.lang || undefined}>{token.text}</code>
        </pre>
      );
    case "blockquote":
      return <blockquote key={key}>{renderMarkdownTokens(tokenChildren(token))}</blockquote>;
    case "list":
      return renderMarkdownList(token as Tokens.List, key);
    case "list_item":
      return <li key={key}>{renderMarkdownTokens(tokenChildren(token))}</li>;
    case "link":
      return renderMarkdownLink(token as Tokens.Link, key);
    case "image":
      return renderMarkdownImage(token as Tokens.Image, key);
    case "hr":
      return <hr key={key} />;
    case "table":
      return renderMarkdownTable(token as Tokens.Table, key);
    case "html":
      return null;
    case "escape":
      return <span key={key}>{token.text}</span>;
    default:
      return null;
  }
}

function tokenChildren(token: Token) {
  return "tokens" in token && Array.isArray(token.tokens) ? token.tokens : [];
}

function renderMarkdownHeading(level: number, tokens: Token[], key: string) {
  const content = renderMarkdownTokens(tokens);
  if (level === 1) return <h1 key={key}>{content}</h1>;
  if (level === 2) return <h2 key={key}>{content}</h2>;
  if (level === 3) return <h3 key={key}>{content}</h3>;
  if (level === 4) return <h4 key={key}>{content}</h4>;
  if (level === 5) return <h5 key={key}>{content}</h5>;
  return <h6 key={key}>{content}</h6>;
}

function renderMarkdownList(token: Tokens.List, key: string) {
  const items = token.items.map((item, index) => (
    <li key={`${key}-${index}`}>{renderMarkdownTokens(item.tokens)}</li>
  ));
  if (token.ordered) {
    return (
      <ol key={key} start={typeof token.start === "number" ? token.start : undefined}>
        {items}
      </ol>
    );
  }
  return <ul key={key}>{items}</ul>;
}

function renderMarkdownLink(token: Tokens.Link, key: string) {
  const href = sanitizeMarkdownHref(token.href);
  if (!href) {
    return <span key={key}>{renderMarkdownTokens(token.tokens)}</span>;
  }
  return (
    <a href={href} key={key} rel="noreferrer" target="_blank" title={token.title || undefined}>
      {renderMarkdownTokens(token.tokens)}
    </a>
  );
}

function renderMarkdownImage(token: Tokens.Image, key: string) {
  const href = sanitizeMarkdownHref(token.href);
  if (!href) {
    return <span key={key}>{token.text}</span>;
  }
  // Markdown images may use arbitrary remote or data URLs that Next Image cannot preconfigure.
  // eslint-disable-next-line @next/next/no-img-element
  return <img alt={token.text} key={key} src={href} title={token.title || undefined} />;
}

function renderMarkdownTable(token: Tokens.Table, key: string) {
  return (
    <div className="markdown-table-wrap" key={key}>
      <table>
        <thead>
          <tr>
            {token.header.map((cell, index) => (
              <th key={`${key}-h-${index}`}>{renderMarkdownTokens(cell.tokens)}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {token.rows.map((row, rowIndex) => (
            <tr key={`${key}-r-${rowIndex}`}>
              {row.map((cell, cellIndex) => (
                <td key={`${key}-r-${rowIndex}-${cellIndex}`}>{renderMarkdownTokens(cell.tokens)}</td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function sanitizeMarkdownHref(value: string) {
  const trimmed = value.trim();
  if (/^(https?:|mailto:)/i.test(trimmed)) {
    return trimmed;
  }
  return "";
}
