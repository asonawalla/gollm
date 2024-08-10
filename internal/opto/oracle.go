// File: internal/opto/oracle.go

package opto

import (
	"context"
	"fmt"
	"reflect"
)

// TraceOracle encapsulates the core OPTO functionality
type TraceOracle interface {
	Execute(ctx context.Context, params map[string]Parameter) (Graph, *Feedback, error)
}

type traceOracle struct {
	executor          func(context.Context, map[string]Parameter) (Graph, error)
	graphBuilder      func(map[string]Parameter, interface{}) Graph
	feedbackGenerator func(Graph) *Feedback
}

func NewTraceOracle(
	executor interface{},
	graphBuilder interface{},
	feedbackGenerator interface{},
) TraceOracle {
	return &traceOracle{
		executor:          adaptExecutor(executor),
		graphBuilder:      adaptGraphBuilder(graphBuilder),
		feedbackGenerator: adaptFeedbackGenerator(feedbackGenerator),
	}
}

func (to *traceOracle) Execute(ctx context.Context, params map[string]Parameter) (Graph, *Feedback, error) {
	graph, err := to.executor(ctx, params)
	if err != nil {
		return nil, nil, fmt.Errorf("execution failed: %w", err)
	}

	feedback := to.feedbackGenerator(graph)

	return graph, feedback, nil
}

// Helper functions to adapt different function signatures

func adaptExecutor(executor interface{}) func(context.Context, map[string]Parameter) (Graph, error) {
	execValue := reflect.ValueOf(executor)
	execType := execValue.Type()

	switch {
	case execType.NumIn() == 2 && execType.In(1) == reflect.TypeOf((*map[string]Parameter)(nil)).Elem():
		// Already in the correct format
		return executor.(func(context.Context, map[string]Parameter) (Graph, error))

	case execType.NumIn() == 2 && execType.In(1) == reflect.TypeOf(""):
		// Convert string-based executor to map-based
		return func(ctx context.Context, params map[string]Parameter) (Graph, error) {
			promptParam, ok := params["prompt"].(*PromptParameter)
			if !ok {
				return nil, fmt.Errorf("invalid prompt parameter")
			}
			results := execValue.Call([]reflect.Value{
				reflect.ValueOf(ctx),
				reflect.ValueOf(promptParam.GetPromptText()),
			})
			if len(results) != 2 {
				return nil, fmt.Errorf("unexpected number of return values from executor")
			}
			result := results[0].Interface()
			err := results[1].Interface()
			if err != nil {
				return nil, err.(error)
			}
			return NewDefaultGraphBuilder()(params, result), nil // Changed this line
		}

	default:
		panic("unsupported executor signature")
	}
}

func adaptGraphBuilder(graphBuilder interface{}) func(map[string]Parameter, interface{}) Graph {
	builderValue := reflect.ValueOf(graphBuilder)
	builderType := builderValue.Type()

	switch {
	case builderType.NumIn() == 2 && builderType.In(0) == reflect.TypeOf((*map[string]Parameter)(nil)).Elem():
		// Already in the correct format
		return graphBuilder.(func(map[string]Parameter, interface{}) Graph)

	case builderType.NumIn() == 2 && builderType.In(0) == reflect.TypeOf(""):
		// Convert string-based builder to map-based
		return func(params map[string]Parameter, result interface{}) Graph {
			promptParam, ok := params["prompt"].(*PromptParameter)
			if !ok {
				return NewGraph()
			}
			return builderValue.Call([]reflect.Value{
				reflect.ValueOf(promptParam.GetPromptText()),
				reflect.ValueOf(result),
			})[0].Interface().(Graph)
		}

	default:
		panic("unsupported graph builder signature")
	}
}

func adaptFeedbackGenerator(feedbackGenerator interface{}) func(Graph) *Feedback {
	genValue := reflect.ValueOf(feedbackGenerator)
	genType := genValue.Type()

	switch {
	case genType.NumIn() == 1 && genType.In(0) == reflect.TypeOf((*Graph)(nil)).Elem():
		// Already in the correct format
		return feedbackGenerator.(func(Graph) *Feedback)

	case genType.NumIn() == 1:
		// Convert interface{}-based generator to Graph-based
		return func(graph Graph) *Feedback {
			outputNode := findOutputNode(graph)
			if outputNode == nil {
				return NewFeedback(0, "No output node found in graph")
			}
			return genValue.Call([]reflect.Value{
				reflect.ValueOf(outputNode.Value()),
			})[0].Interface().(*Feedback)
		}

	default:
		panic("unsupported feedback generator signature")
	}
}

// Helper function to find the output node in a graph
func findOutputNode(graph Graph) Node {
	var outputNode Node
	graph.BFS(graph.Nodes()[0], func(node Node) bool {
		if node.Type() == OutputNode {
			outputNode = node
			return false
		}
		return true
	})
	return outputNode
}

// NewDefaultGraphBuilder creates a default graph builder function for prompt-based workflows
func NewDefaultGraphBuilder() func(map[string]Parameter, interface{}) Graph {
	return func(params map[string]Parameter, result interface{}) Graph {
		g := NewGraph()

		// Create input nodes for each parameter
		for name, param := range params {
			inputNode := NewWorkflowNode(name, InputNode, param.GetValue(), param)
			g.AddWorkflowNode(inputNode)
		}

		// Create result node
		resultNode := NewWorkflowNode("result", OutputNode, result, nil)
		g.AddWorkflowNode(resultNode)

		// Connect all input nodes to the result node
		for _, node := range g.Nodes() {
			if node.Type() == InputNode {
				resultNode.AddDependency(node.(WorkflowNode))
			}
		}

		return g
	}
}

// NewDefaultFeedbackGenerator creates a default feedback generator function
func NewDefaultFeedbackGenerator() func(interface{}) *Feedback {
	return func(result interface{}) *Feedback {
		// This is a simple placeholder. In a real implementation,
		// you would analyze the result and generate appropriate feedback.
		return NewFeedback(0.5, "Default feedback based on execution result")
	}
}

func buildGraph(g Graph, v reflect.Value, name string, parent WorkflowNode) WorkflowNode {
	if !v.IsValid() {
		return nil
	}

	var newNode WorkflowNode
	nodeType := OperationNode // Default node type

	switch v.Kind() {
	case reflect.Ptr, reflect.Interface:
		if v.IsNil() {
			return nil
		}
		return buildGraph(g, v.Elem(), name, parent)
	case reflect.Struct:
		newNode = NewWorkflowNode(name, nodeType, v.Interface(), nil)
		g.AddWorkflowNode(newNode)
		if parent != nil {
			newNode.AddDependency(parent)
		}
		for i := 0; i < v.NumField(); i++ {
			field := v.Field(i)
			fieldName := v.Type().Field(i).Name
			childNode := buildGraph(g, field, fieldName, newNode)
			if childNode != nil {
				newNode.AddDependency(childNode)
			}
		}
	case reflect.Slice, reflect.Array:
		newNode = NewWorkflowNode(name, nodeType, v.Interface(), nil)
		g.AddWorkflowNode(newNode)
		if parent != nil {
			newNode.AddDependency(parent)
		}
		for i := 0; i < v.Len(); i++ {
			childNode := buildGraph(g, v.Index(i), fmt.Sprintf("%s[%d]", name, i), newNode)
			if childNode != nil {
				newNode.AddDependency(childNode)
			}
		}
	case reflect.Map:
		newNode = NewWorkflowNode(name, nodeType, v.Interface(), nil)
		g.AddWorkflowNode(newNode)
		if parent != nil {
			newNode.AddDependency(parent)
		}
		for _, key := range v.MapKeys() {
			childNode := buildGraph(g, v.MapIndex(key), fmt.Sprintf("%s[%v]", name, key.Interface()), newNode)
			if childNode != nil {
				newNode.AddDependency(childNode)
			}
		}
	default:
		newNode = NewWorkflowNode(name, nodeType, v.Interface(), nil)
		g.AddWorkflowNode(newNode)
		if parent != nil {
			newNode.AddDependency(parent)
		}
	}

	// Check if the newNode represents a Parameter
	if v.Type().Implements(reflect.TypeOf((*Parameter)(nil)).Elem()) {
		newNode = NewWorkflowNode(name, ParameterNode, v.Interface(), v.Interface().(Parameter))
		g.AddWorkflowNode(newNode)
	}

	return newNode
}
