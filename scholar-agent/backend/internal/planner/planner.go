package planner

import (
	"fmt"
	"scholar-agent-backend/internal/models"
	"time"

	"github.com/google/uuid"
)

// Planner generates DAGs based on user intent
type Planner struct {
	// In a real system, this would interact with an LLM to dynamically generate the DAG
}

func NewPlanner() *Planner {
	return &Planner{}
}

// GeneratePlan creates a mock DAG for a given intent (for demonstration)
func (p *Planner) GeneratePlan(intent string, intentType string) (*models.Plan, error) {
	planID := uuid.New().String()

	plan := &models.Plan{
		ID:         planID,
		UserIntent: intent,
		Status:     models.StatusPending,
		Tasks:      make(map[string]*models.Task),
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	// Mock logic based on a theoretical "IntentType" from BERT
	if intentType == "Framework_Evaluation" {
		t1 := createMockTask("Retrieve Documentation & Best Practices for Target Frameworks", "librarian_agent", nil, intent)
		t2 := createMockTask("Generate Environment Setup & Integration Code for Framework A", "coder_agent", []string{t1.ID}, intent)
		t3 := createMockTask("Generate Environment Setup & Integration Code for Framework B", "coder_agent", []string{t1.ID}, intent)
		t4 := createMockTask("Execute A/B Tests with User Data in Sandbox", "sandbox_agent", []string{t2.ID, t3.ID}, intent)
		t5 := createMockTask("Analyze Metrics & Generate Evaluation Report", "data_agent", []string{t4.ID}, intent)

		plan.Tasks[t1.ID] = t1
		plan.Tasks[t2.ID] = t2
		plan.Tasks[t3.ID] = t3
		plan.Tasks[t4.ID] = t4
		plan.Tasks[t5.ID] = t5
	} else if intentType == "Paper_Reproduction" {
		t1 := createMockTask("Parse Paper & Extract Algorithm Details", "librarian_agent", nil, intent)
		t2 := createMockTask("Find/Clone Open Source Repository", "coder_agent", []string{t1.ID}, intent)
		t3 := createMockTask("Setup Sandbox Environment (Install Dependencies)", "sandbox_agent", []string{t2.ID}, intent)
		t4 := createMockTask("Execute Baseline Code", "sandbox_agent", []string{t3.ID}, intent)
		t5 := createMockTask("Compare Results with Paper", "data_agent", []string{t4.ID}, intent)
		t6 := createMockTask("Debug/Refine Code if Results Mismatch", "coder_agent", []string{t5.ID}, intent)

		plan.Tasks[t1.ID] = t1
		plan.Tasks[t2.ID] = t2
		plan.Tasks[t3.ID] = t3
		plan.Tasks[t4.ID] = t4
		plan.Tasks[t5.ID] = t5
		plan.Tasks[t6.ID] = t6
	} else if intentType == "Code_Execution" {
		t1 := createMockTask("Generate & Run Code", "coder_agent", nil, intent)
		t2 := createMockTask("Verify Results", "data_agent", []string{t1.ID}, intent)

		plan.Tasks[t1.ID] = t1
		plan.Tasks[t2.ID] = t2
	} else {
		// Default simple plan
		t1 := createMockTask("Process Request", "general_agent", nil, intent)
		plan.Tasks[t1.ID] = t1
	}

	return plan, nil
}

// createMockTask 创建一个包含基础信息的模拟任务
func createMockTask(name, agent string, deps []string, context string) *models.Task {
	if deps == nil {
		deps = []string{}
	}
	return &models.Task{
		ID:           uuid.New().String(),
		Name:         name,
		Description:  fmt.Sprintf("任务目标: %s\n具体要求: %s", name, context),
		AssignedTo:   agent,
		Status:       models.StatusPending,
		Dependencies: deps,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
}
