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

export interface Finding {
  id: string;
  severity: 'high' | 'medium' | 'low';
  title: string;
  description: string;
  evidenceFieldIds: string[];
  followUp: string;
}

export interface AnalysisResult {
  summary: string;
  findings: Finding[];
  missingInformation: { label: string; reason: string }[];
}
