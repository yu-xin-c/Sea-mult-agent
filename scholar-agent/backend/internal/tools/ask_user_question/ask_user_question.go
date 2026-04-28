package askuserquestion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

var (
	ErrQuestionNotFound      = errors.New("question not found")
	ErrQuestionAlreadyClosed = errors.New("question is already answered")
	ErrEmptyQuestion         = errors.New("question must not be empty")
	ErrEmptyAnswer           = errors.New("answer must not be empty")
)

type QuestionStatus string

const (
	QuestionStatusPending  QuestionStatus = "pending"
	QuestionStatusAnswered QuestionStatus = "answered"
)

type SuggestedAnswer struct {
	Label       string `json:"label,omitempty"`
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
}

type UserQuestion struct {
	ID               string            `json:"id"`
	SessionID        string            `json:"session_id"`
	TaskID           string            `json:"task_id,omitempty"`
	Agent            string            `json:"agent,omitempty"`
	ToolName         string            `json:"tool_name"`
	Question         string            `json:"question"`
	Reason           string            `json:"reason,omitempty"`
	Context          string            `json:"context,omitempty"`
	SuggestedAnswers []SuggestedAnswer `json:"suggested_answers,omitempty"`
	Required         bool              `json:"required"`
	Status           QuestionStatus    `json:"status"`
	Answer           string            `json:"answer,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
	AnsweredAt       *time.Time        `json:"answered_at,omitempty"`
}

type AskUserQuestionInput struct {
	SessionID        string            `json:"session_id,omitempty"`
	TaskID           string            `json:"task_id,omitempty"`
	Agent            string            `json:"agent,omitempty"`
	Question         string            `json:"question"`
	Reason           string            `json:"reason,omitempty"`
	Context          string            `json:"context,omitempty"`
	SuggestedAnswers []SuggestedAnswer `json:"suggested_answers,omitempty"`
	Required         bool              `json:"required"`
	Metadata         map[string]string `json:"metadata,omitempty"`
}

type AnswerUserQuestionInput struct {
	Answer   string            `json:"answer"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type AskUserQuestionResume struct {
	QuestionID string            `json:"question_id,omitempty"`
	Answer     string            `json:"answer"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

type AskUserQuestionInterrupt struct {
	Type     string        `json:"type"`
	Question *UserQuestion `json:"question"`
}

type QuestionStore interface {
	Create(ctx context.Context, question *UserQuestion) (*UserQuestion, error)
	Get(ctx context.Context, id string) (*UserQuestion, error)
	Answer(ctx context.Context, id string, input AnswerUserQuestionInput) (*UserQuestion, error)
	ListPending(ctx context.Context, sessionID string) ([]*UserQuestion, error)
}

type InMemoryQuestionStore struct {
	mu        sync.RWMutex
	questions map[string]*UserQuestion
}

func NewInMemoryQuestionStore() *InMemoryQuestionStore {
	return &InMemoryQuestionStore{
		questions: make(map[string]*UserQuestion),
	}
}

func (s *InMemoryQuestionStore) Create(_ context.Context, question *UserQuestion) (*UserQuestion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cloned := cloneQuestion(question)
	s.questions[cloned.ID] = cloned
	return cloneQuestion(cloned), nil
}

func (s *InMemoryQuestionStore) Get(_ context.Context, id string) (*UserQuestion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	question, ok := s.questions[id]
	if !ok {
		return nil, ErrQuestionNotFound
	}

	return cloneQuestion(question), nil
}

func (s *InMemoryQuestionStore) Answer(_ context.Context, id string, input AnswerUserQuestionInput) (*UserQuestion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	question, ok := s.questions[id]
	if !ok {
		return nil, ErrQuestionNotFound
	}

	if question.Status != QuestionStatusPending {
		return nil, ErrQuestionAlreadyClosed
	}

	answer := strings.TrimSpace(input.Answer)
	if answer == "" {
		return nil, ErrEmptyAnswer
	}

	now := time.Now()
	question.Answer = answer
	question.Status = QuestionStatusAnswered
	question.AnsweredAt = &now
	question.UpdatedAt = now
	question.Metadata = mergeMetadata(question.Metadata, input.Metadata)

	return cloneQuestion(question), nil
}

func (s *InMemoryQuestionStore) ListPending(_ context.Context, sessionID string) ([]*UserQuestion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := make([]*UserQuestion, 0)
	for _, question := range s.questions {
		if question.Status != QuestionStatusPending {
			continue
		}
		if sessionID != "" && question.SessionID != sessionID {
			continue
		}
		items = append(items, cloneQuestion(question))
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})

	return items, nil
}

type AskUserQuestionManager struct {
	store QuestionStore
}

func NewAskUserQuestionManager(store QuestionStore) *AskUserQuestionManager {
	if store == nil {
		store = NewInMemoryQuestionStore()
	}

	return &AskUserQuestionManager{store: store}
}

func (m *AskUserQuestionManager) Ask(ctx context.Context, input AskUserQuestionInput) (*UserQuestion, error) {
	questionText := strings.TrimSpace(input.Question)
	if questionText == "" {
		return nil, ErrEmptyQuestion
	}

	now := time.Now()
	question := &UserQuestion{
		ID:               uuid.NewString(),
		SessionID:        defaultString(strings.TrimSpace(input.SessionID), uuid.NewString()),
		TaskID:           strings.TrimSpace(input.TaskID),
		Agent:            strings.TrimSpace(input.Agent),
		ToolName:         "ask_user_question",
		Question:         questionText,
		Reason:           strings.TrimSpace(input.Reason),
		Context:          strings.TrimSpace(input.Context),
		SuggestedAnswers: cloneSuggestedAnswers(input.SuggestedAnswers),
		Required:         input.Required,
		Status:           QuestionStatusPending,
		Metadata:         cloneMetadata(input.Metadata),
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	return m.store.Create(ctx, question)
}

func (m *AskUserQuestionManager) Get(ctx context.Context, questionID string) (*UserQuestion, error) {
	return m.store.Get(ctx, questionID)
}

func (m *AskUserQuestionManager) Answer(ctx context.Context, questionID string, input AnswerUserQuestionInput) (*UserQuestion, error) {
	return m.store.Answer(ctx, questionID, input)
}

func (m *AskUserQuestionManager) ListPending(ctx context.Context, sessionID string) ([]*UserQuestion, error) {
	return m.store.ListPending(ctx, strings.TrimSpace(sessionID))
}

type AskUserQuestionTool struct {
	manager *AskUserQuestionManager
}

func NewAskUserQuestionTool(manager *AskUserQuestionManager) *AskUserQuestionTool {
	if manager == nil {
		manager = NewAskUserQuestionManager(nil)
	}

	return &AskUserQuestionTool{manager: manager}
}

func (t *AskUserQuestionTool) Manager() *AskUserQuestionManager {
	return t.manager
}

func (t *AskUserQuestionTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "ask_user_question",
		Desc: "When critical information is missing, pause execution and ask the user one concise follow-up question. Use this only when the task cannot continue safely without the answer.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"session_id": {
				Type: schema.String,
				Desc: "Optional session identifier used by the host application to group pending questions.",
			},
			"task_id": {
				Type: schema.String,
				Desc: "Optional task or run identifier associated with the question.",
			},
			"agent": {
				Type: schema.String,
				Desc: "Optional agent name that is asking the question.",
			},
			"question": {
				Type:     schema.String,
				Desc:     "The exact follow-up question to show to the user.",
				Required: true,
			},
			"reason": {
				Type: schema.String,
				Desc: "Short explanation of what information is missing and why it blocks execution.",
			},
			"context": {
				Type: schema.String,
				Desc: "Optional extra context that the host can show alongside the question.",
			},
			"required": {
				Type: schema.Boolean,
				Desc: "Whether the user must answer this question before the run can continue.",
			},
			"suggested_answers": {
				Type: schema.Array,
				Desc: "Optional list of suggested answers that the host can render as quick replies.",
				ElemInfo: &schema.ParameterInfo{
					Type: schema.Object,
					SubParams: map[string]*schema.ParameterInfo{
						"label": {
							Type: schema.String,
							Desc: "Optional display label for the suggestion.",
						},
						"value": {
							Type:     schema.String,
							Desc:     "The value to send back if the suggestion is selected.",
							Required: true,
						},
						"description": {
							Type: schema.String,
							Desc: "Optional short description of the suggestion.",
						},
					},
				},
			},
		}),
	}, nil
}

func (t *AskUserQuestionTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...einotool.Option) (string, error) {
	if wasInterrupted, hasState, state := einotool.GetInterruptState[askUserQuestionState](ctx); wasInterrupted && hasState {
		return t.resume(ctx, state)
	}

	var input AskUserQuestionInput
	if err := json.Unmarshal([]byte(argumentsInJSON), &input); err != nil {
		return "", fmt.Errorf("invalid ask_user_question arguments: %w", err)
	}

	question, err := t.manager.Ask(ctx, input)
	if err != nil {
		return "", err
	}

	return "", einotool.StatefulInterrupt(ctx, question.InterruptPayload(), askUserQuestionState{
		QuestionID: question.ID,
	})
}

type askUserQuestionState struct {
	QuestionID string `json:"question_id"`
}

func (t *AskUserQuestionTool) resume(ctx context.Context, state askUserQuestionState) (string, error) {
	question, err := t.manager.Get(ctx, state.QuestionID)
	if err != nil {
		return "", err
	}

	isTarget, hasData, resume := einotool.GetResumeContext[AskUserQuestionResume](ctx)
	if !isTarget {
		return "", einotool.StatefulInterrupt(ctx, question.InterruptPayload(), state)
	}

	if !hasData {
		if question.Status == QuestionStatusAnswered {
			return question.Answer, nil
		}
		return "", einotool.StatefulInterrupt(ctx, question.InterruptPayload(), state)
	}

	if resume.QuestionID != "" && resume.QuestionID != state.QuestionID {
		return "", fmt.Errorf("resume question_id mismatch: expect %s, got %s", state.QuestionID, resume.QuestionID)
	}

	answered, err := t.manager.Answer(ctx, state.QuestionID, AnswerUserQuestionInput{
		Answer:   resume.Answer,
		Metadata: resume.Metadata,
	})
	if err != nil {
		return "", err
	}

	return answered.Answer, nil
}

func (q *UserQuestion) InterruptPayload() *AskUserQuestionInterrupt {
	return &AskUserQuestionInterrupt{
		Type:     "ask_user_question",
		Question: cloneQuestion(q),
	}
}

func cloneQuestion(in *UserQuestion) *UserQuestion {
	if in == nil {
		return nil
	}

	out := *in
	out.SuggestedAnswers = cloneSuggestedAnswers(in.SuggestedAnswers)
	out.Metadata = cloneMetadata(in.Metadata)
	if in.AnsweredAt != nil {
		answerTime := *in.AnsweredAt
		out.AnsweredAt = &answerTime
	}

	return &out
}

func cloneSuggestedAnswers(in []SuggestedAnswer) []SuggestedAnswer {
	if len(in) == 0 {
		return nil
	}

	out := make([]SuggestedAnswer, len(in))
	copy(out, in)
	return out
}

func cloneMetadata(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func mergeMetadata(base map[string]string, extra map[string]string) map[string]string {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}

	out := cloneMetadata(base)
	if out == nil {
		out = make(map[string]string, len(extra))
	}

	for key, value := range extra {
		out[key] = value
	}

	return out
}

func defaultString(value string, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
