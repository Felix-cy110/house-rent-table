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
}

export interface CodexAccount { loggedIn: boolean; authType?: string; email?: string; pending: boolean; error?: string; provider?: string; model?: string; source?: 'manual' | 'ccswitch' }
export interface CodexLogin { type: 'chatgpt' | 'apiKey'; authUrl?: string }
export interface CodexConnection { type: 'chatgpt' | 'apiKey'; apiKey?: string; baseUrl?: string; model?: string; source?: 'ccswitch'; configDir?: string }
