package models

type OpenAPISpec struct {
	OpenAPI string                          `json:"openapi"`
	Info    OpenAPIInfo                     `json:"info"`
	Paths   map[string]map[string]OpenAPIOp `json:"paths"`
}

type OpenAPIInfo struct {
	Title   string `json:"title"`
	Version string `json:"version"`
}

type OpenAPIOp struct {
	Summary     string            `json:"summary"`
	Description string            `json:"description"`
	OperationID string            `json:"operationId"`
	Parameters  []OpenAPIParam    `json:"parameters"`
	RequestBody *OpenAPIReqBody   `json:"requestBody"`
	Responses   map[string]interface{} `json:"responses"`
}

type OpenAPIParam struct {
	Name     string `json:"name"`
	In       string `json:"in"`
	Required bool   `json:"required"`
	Schema   struct {
		Type string `json:"type"`
	} `json:"schema"`
}

type OpenAPIReqBody struct {
	Content map[string]struct {
		Schema interface{} `json:"schema"`
	} `json:"content"`
}
