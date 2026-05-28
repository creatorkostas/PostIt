package processor

import (
	"encoding/json"
	"fmt"
	"postit/internal/models"
	"strings"
)

func ParseOpenAPI(data []byte) ([]models.RequestInfo, error) {
	var spec models.OpenAPISpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %v", err)
	}

	var requests []models.RequestInfo
	folder := spec.Info.Title
	if folder == "" {
		folder = "Imported OpenAPI"
	}

	for path, methods := range spec.Paths {
		for method, op := range methods {
			name := op.Summary
			if name == "" {
				name = fmt.Sprintf("%s %s", strings.ToUpper(method), path)
			}

			fullPath := fmt.Sprintf("%s > %s", folder, name)
			
			req := models.RequestInfo{
				Path: fullPath,
				Request: &models.Request{
					Method: strings.ToUpper(method),
					URL: models.URL{
						Raw: path,
					},
					Header: []models.Header{},
				},
			}

			// Add Content-Type if requestBody exists
			if op.RequestBody != nil {
				req.Request.Body = &models.Body{
					Mode: "raw",
					Raw:  "{\n  \n}", // Placeholder
				}
				for ct := range op.RequestBody.Content {
					req.Request.Header = append(req.Request.Header, models.Header{
						Key:   "Content-Type",
						Value: ct,
					})
					break
				}
			}

			requests = append(requests, req)
		}
	}

	return requests, nil
}
