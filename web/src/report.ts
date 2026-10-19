import MarkdownIt from 'markdown-it';
import type { Citation } from './types';

// Reports come from a model and may quote untrusted cells or webpages. Render
// Markdown tables and HTTP(S) sources, never embedded HTML or remote images.
const markdown = new MarkdownIt({ html: false, linkify: true, breaks: true, typographer: false });
markdown.disable('image');
markdown.validateLink = value => {
  if (!/^https?:\/\//i.test(value)) return false;
  try {
    const url = new URL(value);
    return (url.protocol === 'https:' || url.protocol === 'http:') && !url.username && !url.password;
  } catch { return false; }
};
markdown.renderer.rules.link_open = (tokens, index, options, _env, renderer) => {
  tokens[index]!.attrSet('target', '_blank');
  tokens[index]!.attrSet('rel', 'noopener noreferrer');
  return renderer.renderToken(tokens, index, options);
};

function withCitations(text: string, citations: Citation[]): string {
  const points = Array.from(text);
  const valid = citations.filter(c => Number.isInteger(c.startIndex) && Number.isInteger(c.endIndex)
    && c.startIndex >= 0 && c.endIndex > c.startIndex && c.endIndex <= points.length && markdown.validateLink(c.url))
    .sort((a, b) => a.startIndex - b.startIndex || a.endIndex - b.endIndex);
  let rendered = '';
  let cursor = 0;
  let previousStart = -1;
  let previousEnd = -1;
  let number = 0;
  for (const citation of valid) {
    const sameRange = citation.startIndex === previousStart && citation.endIndex === previousEnd;
    if (citation.startIndex < cursor && !sameRange) continue;
    if (!sameRange) rendered += points.slice(cursor, citation.startIndex).join('');
    // Numbered labels prevent provider titles from introducing Markdown syntax.
    const href = new URL(citation.url).href.replace(/[<>]/g, value => encodeURIComponent(value));
    rendered += ` [${++number}](<${href}>) `;
    cursor = citation.endIndex;
    previousStart = citation.startIndex;
    previousEnd = citation.endIndex;
  }
  return rendered + points.slice(cursor).join('');
}

export function renderReport(text: string, citations: Citation[] = []): DocumentFragment {
  const fragment = document.createDocumentFragment();
  const report = document.createElement('div');
  report.className = 'review-report';
  report.innerHTML = markdown.render(withCitations(text, citations));
  for (const table of report.querySelectorAll('table')) {
    const scroll = document.createElement('div');
    const columns = table.rows[0]?.cells.length ?? 0;
    scroll.className = columns > 3 ? 'report-table report-table-wide' : 'report-table';
    scroll.tabIndex = 0;
    scroll.setAttribute('role', 'region');
    scroll.setAttribute('aria-label', columns > 3 ? '费用明细表，可横向滚动' : '费用明细表');
    table.replaceWith(scroll);
    scroll.append(table);
  }
  const details = document.createElement('details');
  details.className = 'raw-report';
  const summary = document.createElement('summary');
  summary.textContent = '查看 AI 原文';
  const raw = document.createElement('div');
  raw.className = 'ai-response';
  raw.textContent = text;
  details.append(summary, raw);
  fragment.append(report, details);
  return fragment;
}
