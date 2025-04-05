package utils

// MANIFEST STRUCT, REDUNDANT ATM
type ManifestMetadata struct {
	SchemaVersion int    `json:"schemaVersion"`
	MediaType     string `json:"mediaType"`
	Config        struct {
		MediaType string `json:"mediaType"`
		Size      int64  `json:"size"`
		Digest    string `json:"digest"`
	} `json:"config"`
	Layers []struct {
		MediaType string `json:"mediaType"`
		Size      int64  `json:"size"`
		Digest    string `json:"digest"`
	} `json:"layers"`
}

// GET MANIFEST SIZE
func CalculateTotalSize(manifest ManifestMetadata) int64 {
	var totalSize int64 = manifest.Config.Size
	for _, layer := range manifest.Layers {
		totalSize += layer.Size
	}
	return totalSize
}
