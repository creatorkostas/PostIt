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

func (c *Client) RunWorkflow(workflow *models.Workflow, requests []models.RequestInfo, startNodeID string) ([]WorkflowLog, error) {
	logs := []WorkflowLog{}
	
	if len(workflow.Nodes) == 0 {
		return logs, nil
	}

	var currentNode *models.WorkflowNode
	if startNodeID == "" {
		currentNode = &workflow.Nodes[0]
	} else {
		for i := range workflow.Nodes {
			if workflow.Nodes[i].ID == startNodeID {
				currentNode = &workflow.Nodes[i]
				break
			}
		}
	}

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

		case "loop":
			lastBody := ""
			if len(logs) > 0 {
				lastBody = logs[len(logs)-1].Body
			}
			array := gjson.Get(lastBody, currentNode.LoopPath)
			if !array.IsArray() {
				log.Error = "Loop path is not an array"
				outcome = "failure"
			} else {
				items := array.Array()
				max := currentNode.MaxIterations
				if max <= 0 || max > len(items) { max = len(items) }
				
				log.StatusText = fmt.Sprintf("Looping %d items", max)
				
				var loopEntryID string
				for _, e := range workflow.Edges {
					if e.FromNode == currentNode.ID && e.Type == "loop_item" {
						loopEntryID = e.ToNode
						break
					}
				}

				if loopEntryID != "" {
					for i := 0; i < max; i++ {
						c.Storage.VariableMap["$item"] = items[i].Raw
						subLogs, _ := c.RunWorkflow(workflow, requests, loopEntryID)
						for _, sl := range subLogs {
							sl.NodeID = fmt.Sprintf("%s [Iter %d] > %s", currentNode.ID, i, sl.NodeID)
							logs = append(logs, sl)
						}
					}
				}
				outcome = "success"
			}
		}

		logs = append(logs, log)

		var nextNodeID string
		for _, edge := range workflow.Edges {
			if edge.FromNode == currentNode.ID {
				// For loop node, "default" edge is followed AFTER the loop completes
				if currentNode.Type == "loop" && (edge.Type == "default" || edge.Type == "success") {
					nextNodeID = edge.ToNode
					break
				}
				if currentNode.Type != "loop" && (edge.Type == outcome || edge.Type == "default" || edge.Type == "") {
					nextNodeID = edge.ToNode
					break
				}
			}
		}

		if nextNodeID == "" {
			currentNode = nil
		} else {
			found := false
			for i := range workflow.Nodes {
				if workflow.Nodes[i].ID == nextNodeID {
					currentNode = &workflow.Nodes[i]
					found = true
					break
				}
			}
			if !found { currentNode = nil }
		}
	}

	return logs, nil
}
