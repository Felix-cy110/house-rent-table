import type { AnalysisResult, ImportResult, RentalDocument } from './types';

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
    const error = (body as { error?: { code?: string; message?: string } }).error;
    throw new APIError(error?.code ?? 'request_failed', error?.message ?? '请求未完成，请重试。');
  }
  return body as T;
}

export async function importExcel(file: File, signal: AbortSignal): Promise<ImportResult> {
  const body = new FormData();
  body.append('file', file);
  return readResponse<ImportResult>(await fetch('/api/import', { method: 'POST', body, signal }));
}

export async function analyze(document: RentalDocument, signal: AbortSignal): Promise<AnalysisResult> {
  return readResponse<AnalysisResult>(await fetch('/api/analyze', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(document),
    signal,
  }));
}
