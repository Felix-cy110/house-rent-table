import MarkdownIt from 'markdown-it';

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

export function renderReport(text: string): DocumentFragment {
  const fragment = document.createDocumentFragment();
  const report = document.createElement('div');
  report.className = 'review-report';
  report.innerHTML = markdown.render(text);
  for (const table of report.querySelectorAll('table')) {
    const scroll = document.createElement('div');
    scroll.className = 'report-table';
    scroll.tabIndex = 0;
    scroll.setAttribute('role', 'region');
    scroll.setAttribute('aria-label', '费用明细表，可横向滚动');
    table.replaceWith(scroll);
    scroll.append(table);
  }
  const details = document.createElement('details');
  details.className = 'raw-report';
  const summary = document.createElement('summary');
  summary.textContent = '查看 Codex 原文';
  const raw = document.createElement('div');
  raw.className = 'codex-response';
  raw.textContent = text;
  details.append(summary, raw);
  fragment.append(report, details);
  return fragment;
}
