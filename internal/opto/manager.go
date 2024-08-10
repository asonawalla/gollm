// File: internal/opto/manager.go

package opto

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/teilomillet/gollm/internal/llm"
)

// OPTOManager coordinates the overall optimization process
type OPTOManager struct {
	oracle  TraceOracle
	context string
	llm     llm.LLM
}

type OptoPrimeResponse struct {
	Reasoning  string `json:"reasoning" validate:"required"`
	Suggestion string `json:"suggestion" validate:"required"`
	Prompt     string `json:"prompt" validate:"required"`
}

func NewOPTOManager(oracle TraceOracle, context string, l llm.LLM) (*OPTOManager, error) {
	if l == nil {
		return nil, fmt.Errorf("LLM instance cannot be nil")
	}

	return &OPTOManager{
		oracle:  oracle,
		context: context,
		llm:     l,
	}, nil
}

func (om *OPTOManager) Optimize(ctx context.Context, initialParams map[string]Parameter, iterations int) (map[string]Parameter, error) {
	currentParams := initialParams
	var bestParams map[string]Parameter
	var bestScore float64

	for i := 0; i < iterations; i++ {
		log.Printf("Iteration %d", i)

		// Execute Trace Oracle
		graph, feedback, err := om.oracle.Execute(ctx, currentParams)
		if err != nil {
			return nil, fmt.Errorf("optimization iteration %d failed: %w", i, err)
		}

		// Update parameters using OptoPrime
		updates, err := om.OptoPrime(graph, feedback, om.context)
		if err != nil {
			return nil, fmt.Errorf("parameter update failed at iteration %d: %w", i, err)
		}

		// Apply updates to current parameters
		for name, value := range updates {
			if param, ok := currentParams[name]; ok {
				param.SetValue(value)
			}
		}

		score := feedback.Score
		log.Printf("Iteration %d - Score: %f", i, score)

		if score > bestScore {
			bestScore = score
			bestParams = copyParams(currentParams)
		}
	}

	return bestParams, nil
}

func (om *OPTOManager) preparePrompt(subgraph *TraceGraph, feedback *Feedback, ctx string) string {
	return fmt.Sprintf(`
Context: %s

Optimization Goal: Improve the parameters of the computational workflow represented by the graph structure below.

Graph Structure:
%s

Current Performance:
Score: %.2f
Feedback: %s

Previous Iterations: [You can add information about previous iterations here if available]

Task:
1. Analyze the graph structure, current performance, and feedback.
2. Consider the context and optimization goal.
3. Suggest improvements to the parameters to optimize the output.

Your response should be a JSON object where keys are parameter names and values are the suggested updates.
Provide reasoning for each suggested update.

Example format:
{
    "param1": {"value": "new_value", "reasoning": "explanation for this update"},
    "param2": {"value": 42, "reasoning": "justification for this change"}
}

Remember to consider the interactions between parameters and the overall optimization goal.
`, ctx, subgraphToString(subgraph), feedback.Score, feedback.Message)
}

func traceGraphToString(tg *TraceGraph) string {
	var result strings.Builder
	for _, item := range tg.Graph {
		result.WriteString(fmt.Sprintf("Node: %s, Type: %s, Value: %v\n", item.Node.ID(), item.Node.Type(), item.Node.Value()))
	}
	return result.String()
}

func (om *OPTOManager) parseResponse(response string) (map[string]interface{}, error) {
	var updates map[string]interface{}
	err := json.Unmarshal([]byte(response), &updates)
	if err != nil {
		return nil, fmt.Errorf("failed to parse LLM response: %w", err)
	}
	return updates, nil
}

func cleanJSONResponse(response string) string {
	// Remove markdown code block syntax if present
	response = strings.TrimPrefix(response, "```json")
	response = strings.TrimSuffix(response, "```")
	response = strings.TrimSpace(response)

	// Find the first '{' and last '}'
	start := strings.Index(response, "{")
	end := strings.LastIndex(response, "}")

	if start != -1 && end != -1 && start < end {
		return response[start : end+1]
	}

	// If we couldn't find valid JSON delimiters, return the original string
	return response
}

func (om *OPTOManager) OptoPrime(graph Graph, feedback *Feedback, ctx string) (map[string]interface{}, error) {
	// Create a graph propagator
	propagator := NewGraphPropagator(graph)

	// Find the output node
	outputNode := findOutputNode(graph)
	if outputNode == nil {
		return nil, fmt.Errorf("no output node found in graph")
	}

	// Propagate the minimal subgraph
	subgraph := propagator.PropagateMinimalSubgraph(graph.Nodes()[0], outputNode)

	// Prepare the prompt for the LLM
	prompt := om.preparePrompt(subgraph, feedback, ctx)

	// Generate a response from the LLM
	response, _, err := om.llm.Generate(context.Background(), prompt)
	if err != nil {
		return nil, fmt.Errorf("failed to generate response: %w", err)
	}

	// Parse the response to get parameter updates
	updates, err := om.parseResponse(response)
	if err != nil {
		return nil, fmt.Errorf("failed to parse OptoPrime response: %w", err)
	}

	return updates, nil
}

func extractJSON(s string) string {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start != -1 && end != -1 && end > start {
		return s[start : end+1]
	}
	return ""
}

func copyParams(params map[string]Parameter) map[string]Parameter {
	copy := make(map[string]Parameter)
	for k, v := range params {
		copy[k] = v
	}
	return copy
}

func (om *OPTOManager) extractSuggestion(response string) (string, error) {
	// Split the response into lines
	lines := strings.Split(response, "\n")

	// Possible headers for the Suggestion section
	suggestionHeaders := []string{
		"Suggestion:",
		"4. Suggestion:",
		"### 4. Suggestion:",
		"### Suggestion:",
	}

	var suggestion strings.Builder
	inSuggestion := false

	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)

		// Check if we've reached the Suggestion section
		for _, header := range suggestionHeaders {
			if strings.HasPrefix(trimmedLine, header) {
				inSuggestion = true
				break
			}
		}

		// If we're in the Suggestion section, add the line to our suggestion
		if inSuggestion {
			// Check if we've reached the end of the Suggestion section
			if strings.HasPrefix(trimmedLine, "###") && !strings.Contains(trimmedLine, "Suggestion") {
				break
			}
			suggestion.WriteString(line + "\n")
		}
	}

	if suggestion.Len() == 0 {
		return "", fmt.Errorf("no suggestion found in LLM response")
	}

	return strings.TrimSpace(suggestion.String()), nil
}

func (om *OPTOManager) applySuggestion(currentPrompt, suggestionJSON string) (string, error) {
	var suggestion struct {
		Reasoning  string `json:"reasoning"`
		Suggestion string `json:"suggestion"`
	}

	err := json.Unmarshal([]byte(suggestionJSON), &suggestion)
	if err != nil {
		return "", fmt.Errorf("failed to parse suggestion JSON: %w", err)
	}

	if suggestion.Suggestion == "" {
		return "", fmt.Errorf("empty suggestion received")
	}

	return suggestion.Suggestion, nil
}

func graphToString(graph Graph) string {
	var result strings.Builder
	for _, node := range graph.Nodes() {
		result.WriteString(fmt.Sprintf("Node: %s, Type: %s, Value: %v\n", node.ID(), node.Type(), node.Value()))
	}
	return result.String()
}

func subgraphToString(subgraph *TraceGraph) string {
	var result strings.Builder
	for id, item := range subgraph.Graph {
		result.WriteString(fmt.Sprintf("Node: %s, Type: %s, Value: %v\n", id, item.Node.Type(), item.Node.Value()))
	}
	return result.String()
}

func scorePrompt(prompt string, feedback *Feedback) float64 {
	if len(prompt) == 0 {
		return 0 // Return 0 score for empty prompts
	}
	return feedback.Score * (1 - float64(len(prompt))/1000) // Penalize very long prompts
}
