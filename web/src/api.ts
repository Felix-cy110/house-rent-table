import type { AnalysisResult, ImportResult, CodexAccount, CodexLogin } from './types';

export class APIError extends Error {
  constructor(public code: string, message: string) {
    super(message);
  }
}

async function readResponse<T>(response: Response): Promise<T> {
  let body: unknown;
  try {
    body = await response.json();
  } catch {
    throw new APIError('invalid_response', '服务暂时不可用，请确认服务已启动后重试。');
  }
  if (!response.ok) {
    const error = (body as { error?: { code?: string; message?: string } } | null)?.error;
    throw new APIError(error?.code ?? 'request_failed', error?.message ?? '请求未完成，请重试。');
  }
  return body as T;
}

export async function importExcel(file: File, signal: AbortSignal): Promise<ImportResult> {
  const body = new FormData();
  body.append('file', file);
  return readResponse<ImportResult>(await fetch('/api/import', { method: 'POST', body, signal }));
}

export async function analyze(payload: string, signal: AbortSignal): Promise<AnalysisResult> {
  return readResponse<AnalysisResult>(await fetch('/api/analyze', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: payload,
    signal,
  }));
}

export async function account(): Promise<CodexAccount> {
  return readResponse<CodexAccount>(await fetch('/api/codex/account'));
}

export async function login(type: 'chatgpt' | 'apiKey', apiKey?: string): Promise<CodexLogin> {
  return readResponse<CodexLogin>(await fetch('/api/codex/login', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ type, apiKey }),
  }));
}

export async function logout(): Promise<void> {
  await readResponse(await fetch('/api/codex/logout', { method: 'POST' }));
}
