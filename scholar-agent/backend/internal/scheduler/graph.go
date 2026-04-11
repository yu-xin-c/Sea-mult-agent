package scheduler

import "scholar-agent-backend/internal/models"

func getNodeByID(plan *models.PlanGraph, taskID string) *models.TaskNode {
	for _, node := range plan.Nodes {
		if node != nil && node.ID == taskID {
			return node
		}
	}
	return nil
}

func getOutgoingNodes(plan *models.PlanGraph, taskID string) []*models.TaskNode {
	nodes := []*models.TaskNode{}
	for _, edge := range plan.Edges {
		if edge != nil && (edge.Type == "control" || edge.Type == "data") && edge.From == taskID {
			if target := getNodeByID(plan, edge.To); target != nil {
				nodes = append(nodes, target)
			}
		}
	}
	return nodes
}

func allDependenciesCompleted(plan *models.PlanGraph, task *models.TaskNode) bool {
	for _, depID := range task.Dependencies {
		dep := getNodeByID(plan, depID)
		if dep == nil || dep.Status != models.StatusCompleted {
			return false
		}
	}
	return true
}

func artifactsSatisfied(plan *models.PlanGraph, task *models.TaskNode) bool {
	for _, key := range task.RequiredArtifacts {
		if _, ok := plan.Artifacts[key]; !ok {
			return false
		}
	}
	return true
}
