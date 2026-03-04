package models

type Workflow struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Nodes []WorkflowNode `json:"nodes"`
	Edges []WorkflowEdge `json:"edges"`
}

type WorkflowNode struct {
	ID        string `json:"id"`
	RequestPath string `json:"requestPath"` // Path to the request in PostIt
	X         float64 `json:"x"`
	Y         float64 `json:"y"`
	Extracts  []Extract `json:"extracts"` // Data to extract from response
}

type WorkflowEdge struct {
	FromNode string `json:"fromNode"`
	ToNode   string `json:"toNode"`
}

type Extract struct {
	SourcePath string `json:"sourcePath"` // gjson path
	TargetVar  string `json:"targetVar"`  // Variable name to set
}
