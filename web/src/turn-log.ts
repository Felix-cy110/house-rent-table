// Each link captures one immutable turn. Keep serialized JSON intact, including
// integer IDs that would lose precision if parsed and stringified in JavaScript.
export function createTurnLogLink(json: string): HTMLAnchorElement {
  const link = document.createElement('a');
  link.className = 'choose-file';
  link.textContent = '查看日志';
  link.target = '_blank';
  link.rel = 'noopener noreferrer';
  link.title = '在新标签页查看完整 turn JSON';
  // application/json is rendered as text by the browser, never as active HTML.
  link.href = URL.createObjectURL(new Blob([json], { type: 'application/json;charset=utf-8' }));
  return link;
}

export function releaseTurnLog(container: HTMLElement) {
  for (const link of container.querySelectorAll<HTMLAnchorElement>('a[href^="blob:"]')) {
    URL.revokeObjectURL(link.href);
  }
}
