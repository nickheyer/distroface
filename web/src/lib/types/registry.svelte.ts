export interface ImageTag {
  name: string;
  size: number;
  digest: string;
  created: string;
}

export interface ImageRepository {
  id: string;             // SHA/DIGEST
  name: string;           // REPO NAME
  tags: ImageTag[];
  updated_at: string;
  owner: string;
  private: boolean;
  size: number;          // TOTAL SIZE IN BYTES
}

// API REQUEST/RESPONSE TYPES
export interface VisibilityUpdateRequest {
  id: string;
  private: boolean;
}

export interface RegistryStats {
  total_images: number;
  total_size: number;
  images: ImageRepository[];
}

// MIGRATION TYPES
export interface ImageMigrationRequest {
  source_registry: string;
  images: string[];
  username?: string;
  password?: string;
}

export interface MigrationStats {
  total_layers: number;
  layers_skipped: number;
  bytes_skipped: number;
}

export interface MigrationTask {
  id: string;
  status: 'pending' | 'running' | 'completed' | 'failed';
  progress: number;
  error?: string;
  start_time: string;
  end_time?: string;
  stats: MigrationStats;
}
