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
  version: string;
  size: number;
  mime_type: string;
  metadata: string;
  created_at: string;
  updated_at: string;
}
