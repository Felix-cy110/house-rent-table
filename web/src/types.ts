export interface RentalField {
  id: string;
  label: string;
  bold: boolean;
  value: string | null;
  sheet: string;
  cell: string;
}

export interface RentalDocument {
  schemaVersion: 1;
  fileName: string;
  fields: RentalField[];
}

export interface ImportResult {
  document: RentalDocument;
  warnings: string[];
}

export interface AnalysisResult {
  text: string;
  citations?: Citation[];
  sources?: Source[];
  warnings?: string[];
}

export interface Citation { title: string; url: string; startIndex: number; endIndex: number }

export interface BrokerFeedback { brokerName: string; feedbackCount: number }

export interface PersistenceReceipt { status: 'queued' | 'failed'; taskId: string | null }
export interface PersistenceStatus { taskId: string; stage: 'EXTRACT' | 'PERSIST'; status: 'PENDING' | 'RUNNING' | 'RETRY_WAIT' | 'SUCCEEDED' | 'FAILED'; message?: string }

export interface ModelProvider { id: string; name: string; model: string; versionNote?: string; searchNote: string; thinking: boolean }
export interface APIAccount { connected: boolean; provider?: string; name?: string; model?: string; needsReconnect?: boolean }
export interface APIConnection { apiKey: string; provider: string }
export interface Source { title: string; url: string; site?: string; publishedAt?: string }
