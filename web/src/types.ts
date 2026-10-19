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
}

export interface Citation { title: string; url: string; startIndex: number; endIndex: number }

export interface BrokerFeedback { brokerName: string; feedbackCount: number }

export interface PersistenceReceipt { status: 'queued' | 'failed'; taskId: string | null }
export interface PersistenceStatus { taskId: string; stage: 'EXTRACT' | 'PERSIST'; status: 'PENDING' | 'RUNNING' | 'RETRY_WAIT' | 'SUCCEEDED' | 'FAILED'; message?: string }

export interface APIAccount { connected: boolean; baseUrl: string; model: string }
export interface APIConnection { apiKey: string; baseUrl: string; model: string }
