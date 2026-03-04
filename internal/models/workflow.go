package models

type Workflow struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Nodes      []WorkflowNode `json:"nodes"`
	Edges      []WorkflowEdge `json:"edges"`
	Status     string         `json:"status,omitempty"`     // "running", "paused", "completed", "failed"
	WaitingFor string         `json:"waitingFor,omitempty"` // Variable name if paused
	CurrentNode string        `json:"currentNode,omitempty"`
}

type WorkflowNode struct {
	ID            string    `json:"id"`
	Type          string    `json:"type"` // "request", "wait", "condition", "loop", "script", "input"
	RequestPath   string    `json:"requestPath,omitempty"`
	WaitTime      int       `json:"waitTime,omitempty"`    // in ms
	Condition     string    `json:"condition,omitempty"`   // gjson path
	LoopPath      string    `json:"loopPath,omitempty"`    // gjson path to array
	MaxIterations int       `json:"maxIterations,omitempty"`
	Script        string    `json:"script,omitempty"`      // JS script for "script" node
	VariableName  string    `json:"variableName,omitempty"` // for "input" node
	X             float64   `json:"x"`
	Y             float64   `json:"y"`
	Extracts      []Extract `json:"extracts,omitempty"`
}

type WorkflowEdge struct {
	FromNode string `json:"fromNode"`
	ToNode   string `json:"toNode"`
	Type     string `json:"type,omitempty"` // "success", "failure", "default", "error", "loop_item"
}

type Extract struct {
	SourcePath string `json:"sourcePath"`
	TargetVar  string `json:"targetVar"`
}
