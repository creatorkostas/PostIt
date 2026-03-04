package models

type Workflow struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Nodes []WorkflowNode `json:"nodes"`
	Edges []WorkflowEdge `json:"edges"`
}

type WorkflowNode struct {
	ID          string    `json:"id"`
	Type        string    `json:"type"` // "request", "wait", "condition"
	RequestPath string    `json:"requestPath,omitempty"`
	WaitTime    int       `json:"waitTime,omitempty"`    // in ms
	Condition   string    `json:"condition,omitempty"`   // gjson path or expression
	X           float64   `json:"x"`
	Y           float64   `json:"y"`
	Extracts    []Extract `json:"extracts,omitempty"`
}

type WorkflowEdge struct {
	FromNode string `json:"fromNode"`
	ToNode   string `json:"toNode"`
	Type     string `json:"type,omitempty"` // "success", "failure", "default"
}

type Extract struct {
	SourcePath string `json:"sourcePath"`
	TargetVar  string `json:"targetVar"`
}
