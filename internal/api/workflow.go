package api

import (
	"fmt"
	"postit/internal/models"

	"github.com/tidwall/gjson"
)

type WorkflowLog struct {
	NodeID     string `json:"nodeId"`
	StatusCode int    `json:"statusCode"`
	StatusText string `json:"statusText"`
	Body       string `json:"body"`
	Error      string `json:"error"`
}

func (c *Client) RunWorkflow(workflow *models.Workflow, requests []models.RequestInfo) ([]WorkflowLog, error) {
	logs := []WorkflowLog{}
	
	// Simple sequential execution based on Node order for now
	// In a real graph, we'd do a topological sort or follow edges
	for _, node := range workflow.Nodes {
		var targetReq *models.RequestInfo
		for _, r := range requests {
			if r.Path == node.RequestPath {
				targetReq = &r
				break
			}
		}

		if targetReq == nil {
			logs = append(logs, WorkflowLog{NodeID: node.ID, Error: fmt.Sprintf("Request not found: %s", node.RequestPath)})
			continue
		}

		// Execute
		c.Processor.RunScripts(targetReq.Events, "prerequest", nil, nil, targetReq.Request.Header)
		body, _, statusCode, statusText := c.ExecuteRequest(targetReq.Request)
		
		log := WorkflowLog{
			NodeID:     node.ID,
			StatusCode: statusCode,
			StatusText: statusText,
			Body:       body,
		}

		if statusCode >= 200 && statusCode < 300 {
			// Extract variables
			for _, ext := range node.Extracts {
				val := gjson.Get(body, ext.SourcePath)
				if val.Exists() {
					c.Storage.VariableMap[ext.TargetVar] = val.String()
				}
			}
			c.Storage.SaveVariables()
		} else {
			log.Error = "Request failed"
		}
		
		logs = append(logs, log)
		
		if log.Error != "" {
			// Stop execution on first error for simple sequential chains
			break
		}
	}

	return logs, nil
}
