package api

import (
	"fmt"
	"postit/internal/models"
	"time"

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
	
	if len(workflow.Nodes) == 0 {
		return logs, nil
	}

	// Simple graph traversal starting from first node
	currentNode := &workflow.Nodes[0]
	visited := make(map[string]bool)

	for currentNode != nil {
		if visited[currentNode.ID] {
			break // Prevent infinite loops
		}
		visited[currentNode.ID] = true

		log := WorkflowLog{NodeID: currentNode.ID}
		outcome := "success"

		switch currentNode.Type {
		case "request":
			var targetReq *models.RequestInfo
			for _, r := range requests {
				if r.Path == currentNode.RequestPath {
					targetReq = &r
					break
				}
			}

			if targetReq == nil {
				log.Error = "Request not found"
				outcome = "failure"
			} else {
				c.Processor.RunScripts(targetReq.Events, "prerequest", nil, nil, targetReq.Request.Header)
				body, _, statusCode, statusText := c.ExecuteRequest(targetReq.Request)
				log.StatusCode = statusCode
				log.StatusText = statusText
				log.Body = body

				if statusCode >= 200 && statusCode < 300 {
					for _, ext := range currentNode.Extracts {
						val := gjson.Get(body, ext.SourcePath)
						if val.Exists() {
							c.Storage.VariableMap[ext.TargetVar] = val.String()
						}
					}
					c.Storage.SaveVariables()
				} else {
					log.Error = "Request failed"
					outcome = "failure"
				}
			}

		case "wait":
			time.Sleep(time.Duration(currentNode.WaitTime) * time.Millisecond)
			log.StatusText = fmt.Sprintf("Waited %dms", currentNode.WaitTime)

		case "condition":
			// Evaluate condition against last response body or variables
			// For simplicity, we check if condition (gjson path) exists in last body
			lastBody := ""
			if len(logs) > 0 {
				lastBody = logs[len(logs)-1].Body
			}
			val := gjson.Get(lastBody, currentNode.Condition)
			if val.Exists() && (val.Type != gjson.False && val.String() != "") {
				outcome = "success"
				log.StatusText = "Condition True"
			} else {
				outcome = "failure"
				log.StatusText = "Condition False"
			}
		}

		logs = append(logs, log)

		// Find next node based on outcome
		var nextNodeID string
		for _, edge := range workflow.Edges {
			if edge.FromNode == currentNode.ID {
				if edge.Type == outcome || edge.Type == "default" || edge.Type == "" {
					nextNodeID = edge.ToNode
					break
				}
			}
		}

		if nextNodeID == "" {
			currentNode = nil
		} else {
			for i := range workflow.Nodes {
				if workflow.Nodes[i].ID == nextNodeID {
					currentNode = &workflow.Nodes[i]
					break
				}
			}
		}
	}

	return logs, nil
}
