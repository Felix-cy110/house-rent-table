export interface RentalField {
  id: string;
  label: string;
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

export interface CodexAccount { loggedIn: boolean; authType?: string; email?: string; pending: boolean; error?: string }
export interface CodexLogin { type: 'chatgpt' | 'apiKey'; authUrl?: string }
