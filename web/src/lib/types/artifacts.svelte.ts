export interface ArtifactRepository {
  id: number;
  name: string;
  description: string;
  owner: string;
  private: boolean;
  created_at: string;
  updated_at: string;
}

export interface Artifact {
  id: string;
  repo_id: number;
  name: string;
  path: string;
  version: string;
  size: number;
  mime_type: string;
  metadata: string;
  properties: Record<string, string>|null;
  created_at: string;
  updated_at: string;
}
