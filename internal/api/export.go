package api

import (
	"postit/internal/models"
	"strings"

	"github.com/google/uuid"
)

// ExportPostman filters requests by folderPath and reconstructs a Postman-compatible collection.
// If folderPath is empty, the entire collection is exported.
func ExportPostman(requests []models.RequestInfo, folderPath string) models.Collection {
	var filtered []models.RequestInfo
	name := "PostIt Export"

	if folderPath == "" {
		filtered = requests
	} else {
		name = folderPath
		prefix := folderPath + " > "
		for _, req := range requests {
			if req.Path == folderPath || strings.HasPrefix(req.Path, prefix) {
				// Strip prefix to root the export at the selected folder
				newReq := req
				if req.Path == folderPath {
					// This should technically be an empty string if we want it at root,
					// but ReconstructItems handles " > " separated parts.
					// Actually, if it's exactly the folder path, it's the request name itself.
					newReq.Path = strings.TrimPrefix(req.Path, folderPath)
				} else {
					newReq.Path = strings.TrimPrefix(req.Path, prefix)
				}
				filtered = append(filtered, newReq)
			}
		}
	}

	return models.Collection{
		Info: models.Info{
			PostmanID: uuid.New().String(),
			Name:      name,
			Schema:    "https://schema.getpostman.com/json/collection/v2.1.0/collection.json",
		},
		Item: models.ReconstructItems(filtered),
	}
}
