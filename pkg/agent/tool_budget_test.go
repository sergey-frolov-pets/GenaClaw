package agent

import (
	"context"
	"sync"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

// countingTool is a fake tool that counts how many times it was executed and
// returns a fixed result. Used to verify the per-turn tool-call budget.
type countingTool struct {
	name  string
	mu    sync.Mutex
	calls int
}

func (c *countingTool) Name() string        { return c.name }
func (c *countingTool) Description() string { return "fake counting tool" }
func (c *countingTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (c *countingTool) Execute(_ context.Context, _ map[string]any) *toolshared.ToolResult {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return toolshared.NewToolResult("ok")
}

func (c *countingTool) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// multiToolCallProvider returns N tool calls on the first LLM call and a final
// text response on every subsequent call. This lets a single iteration attempt
// more tool calls than the configured budget.
type multiToolCallProvider struct {
	toolName  string
	numCalls  int
	response  string
	callCount int
	mu        sync.Mutex
}

func (p *multiToolCallProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.callCount++
	count := p.callCount
	p.mu.Unlock()

	if count == 1 {
		calls := make([]providers.ToolCall, 0, p.numCalls)
		for i := 0; i < p.numCalls; i++ {
			calls = append(calls, providers.ToolCall{
				ID:        "call_" + string(rune('a'+i)),
				Name:      p.toolName,
				Arguments: map[string]any{},
			})
		}
		return &providers.LLMResponse{
			Content:      "calling tools",
			ToolCalls:    calls,
			FinishReason: "tool_calls",
		}, nil
	}
	return &providers.LLMResponse{
		Content:      p.response,
		FinishReason: "stop",
	}, nil
}

func (p *multiToolCallProvider) GetDefaultModel() string { return "multi-tool-model" }

// TestRunTurn_ToolCallBudget verifies that when MaxToolCalls is set, the agent
// stops executing tools once the budget is exhausted, even if the LLM requested
// more tool calls in the same iteration.
func TestRunTurn_ToolCallBudget(t *testing.T) {
	const budget = 2
	const requested = 5

	provider := &multiToolCallProvider{
		toolName: "fake_count",
		numCalls: requested,
		response: "done",
	}
	al, agent, cleanup := newTurnCoordTestLoop(t, provider)
	defer cleanup()

	// Register a fake tool so the calls actually execute (and get counted).
	fake := &countingTool{name: "fake_count"}
	agent.Tools.Register(fake)

	// Enforce a hard budget of 2 tool calls for the whole turn.
	agent.MaxToolCalls = budget

	pipeline := NewPipeline(al)
	opts := makeTestProcessOpts("test-session-toolbudget")

	ts := newTurnState(agent, opts, turnEventScope{
		turnID:  "turn-toolbudget",
		context: newTurnContext(nil, nil, nil),
	})

	result, err := al.runTurn(context.Background(), ts, pipeline)
	if err != nil {
		t.Fatalf("runTurn failed: %v", err)
	}
	if result.status != TurnEndStatusCompleted {
		t.Errorf("expected status Completed, got %v", result.status)
	}

	// The fake tool must have executed exactly `budget` times, not `requested`.
	if got := fake.callCount(); got != budget {
		t.Errorf("tool executed %d times, want %d (budget)", got, budget)
	}
}

// TestRunTurn_ToolCallBudgetUnlimited verifies that MaxToolCalls=0 means no
// per-turn tool budget: all requested calls in the first iteration execute.
func TestRunTurn_ToolCallBudgetUnlimited(t *testing.T) {
	const requested = 3

	provider := &multiToolCallProvider{
		toolName: "fake_count_unlimited",
		numCalls: requested,
		response: "done",
	}
	al, agent, cleanup := newTurnCoordTestLoop(t, provider)
	defer cleanup()

	fake := &countingTool{name: "fake_count_unlimited"}
	agent.Tools.Register(fake)

	// Default: no budget (0 = unlimited).
	agent.MaxToolCalls = 0

	pipeline := NewPipeline(al)
	opts := makeTestProcessOpts("test-session-toolbudget-unlimited")

	ts := newTurnState(agent, opts, turnEventScope{
		turnID:  "turn-toolbudget-unlimited",
		context: newTurnContext(nil, nil, nil),
	})

	result, err := al.runTurn(context.Background(), ts, pipeline)
	if err != nil {
		t.Fatalf("runTurn failed: %v", err)
	}
	if result.status != TurnEndStatusCompleted {
		t.Errorf("expected status Completed, got %v", result.status)
	}

	if got := fake.callCount(); got != requested {
		t.Errorf("tool executed %d times, want %d (no budget)", got, requested)
	}
}
