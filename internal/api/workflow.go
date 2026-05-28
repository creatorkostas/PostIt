package api

import (
	"context"
	"fmt"
	"postit/internal/models"
	"strings"
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

type workflowTask struct {
	nodeID      string
	itemPayload string // for loop items
}

const maxWorkflowTasks = 5000 // prevent infinite loops or excessive resource usage

func (c *Client) RunWorkflow(ctx context.Context, workflow *models.Workflow, requests []models.RequestInfo, startNodeID string) ([]WorkflowLog, error) {
	logs := []WorkflowLog{}
	
	if len(workflow.Nodes) == 0 {
		return logs, nil
	}

	tasks := []workflowTask{}
	if startNodeID == "" {
		tasks = append(tasks, workflowTask{nodeID: workflow.Nodes[0].ID})
	} else {
		tasks = append(tasks, workflowTask{nodeID: startNodeID})
	}

	visited := make(map[string]int)
	taskCount := 0
	workflowVars := make(map[string]string)

	for len(tasks) > 0 {
		select {
		case <-ctx.Done():
			return logs, ctx.Err()
		default:
		}

		if taskCount >= maxWorkflowTasks {
			return logs, fmt.Errorf("Workflow exceeded maximum task limit (%d)", maxWorkflowTasks)
		}
		taskCount++

		// Pop task (LIFO for loop items order)
		task := tasks[len(tasks)-1]
		tasks = tasks[:len(tasks)-1]

		var currentNode *models.WorkflowNode
		for i := range workflow.Nodes {
			if workflow.Nodes[i].ID == task.nodeID {
				currentNode = &workflow.Nodes[i]
				break
			}
		}
		if currentNode == nil { continue }

		// Item injection for loops - uses local workflowVars
		if task.itemPayload != "" {
			workflowVars["$item"] = task.itemPayload
		}

		// Cycle detection
		visited[currentNode.ID]++
		if visited[currentNode.ID] > 1000 {
			return logs, fmt.Errorf("Potential infinite loop detected at node %s", currentNode.ID)
		}

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
				c.Processor.RunScriptsWithLocal(targetReq.Events, "prerequest", nil, nil, targetReq.Request.Header, workflowVars)
				body, _, statusCode, statusText := c.ExecuteRequestWithLocal(ctx, targetReq.Request, workflowVars)
				log.StatusCode = statusCode
				log.StatusText = statusText
				log.Body = body

				if statusCode >= 200 && statusCode < 300 {
					for _, ext := range currentNode.Extracts {
						val := gjson.Get(body, ext.SourcePath)
						if val.Exists() {
							workflowVars[ext.TargetVar] = val.String()
						}
					}
					// Optional: Should we save extracted variables to global storage too?
					// The issue was about race conditions during execution.
					// Let's keep them local for the run, and maybe save at the end if needed.
				} else {
					log.Error = "Request failed"
					outcome = "failure"
				}
			}

		case "wait":
			select {
			case <-time.After(time.Duration(currentNode.WaitTime) * time.Millisecond):
			case <-ctx.Done():
				return logs, ctx.Err()
			}
			log.StatusText = fmt.Sprintf("Waited %dms", currentNode.WaitTime)

		case "condition":
			lastBody := ""
			if len(logs) > 0 {
				lastBody = logs[len(logs)-1].Body
			}
			// Resolve variables in condition if needed? gjson doesn't do that, but our cond might.
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
					// Add loop items to tasks in reverse so they execute in order when popped from LIFO stack
					for i := max - 1; i >= 0; i-- {
						tasks = append(tasks, workflowTask{
							nodeID:      loopEntryID,
							itemPayload: items[i].Raw,
						})
					}
				}
				outcome = "success"
			}

		case "script":
			c.Processor.RunScriptsWithLocal([]models.Event{{Listen: "test", Script: models.Script{Exec: strings.Split(currentNode.Script, "\n")}}}, "test", nil, nil, nil, workflowVars)
			log.StatusText = "Script Executed"
			outcome = "success"

		case "input":
			workflow.Status = "paused"
			workflow.WaitingFor = currentNode.VariableName
			workflow.CurrentNode = currentNode.ID
			log.StatusText = fmt.Sprintf("Paused, waiting for variable: %s", currentNode.VariableName)
			logs = append(logs, log)
			return logs, nil 
		}

		logs = append(logs, log)

		// Determine next standard node
		var nextNodeID string
		for _, edge := range workflow.Edges {
			if edge.FromNode == currentNode.ID {
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

		if nextNodeID != "" {
			tasks = append(tasks, workflowTask{nodeID: nextNodeID})
		}
	}

	return logs, nil
}
