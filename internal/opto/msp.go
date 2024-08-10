// File: internal/opto/msp.go

package opto

import (
	"reflect"
)

// TraceGraph represents the feedback container used by GraphPropagator
type TraceGraph struct {
	Graph        map[string]*Item // Change to map for easier lookup
	UserFeedback interface{}
}

type Item struct {
	Node     Node
	Priority int
}

func NewTraceGraph(nodes []Node, userFeedback interface{}) *TraceGraph {
	items := make(map[string]*Item, len(nodes))
	for _, node := range nodes {
		items[node.ID()] = &Item{
			Node:     node,
			Priority: node.Level(),
		}
	}
	return &TraceGraph{
		Graph:        items,
		UserFeedback: userFeedback,
	}
}

// Add combines two TraceGraph instances
func (tg *TraceGraph) Add(other *TraceGraph) *TraceGraph {
	if tg.UserFeedback == nil && other.UserFeedback == nil {
		panic("One of the user feedback should not be nil")
	}

	var userFeedback interface{}
	if tg.UserFeedback == nil || other.UserFeedback == nil {
		userFeedback = tg.UserFeedback
		if userFeedback == nil {
			userFeedback = other.UserFeedback
		}
	} else {
		if !reflect.DeepEqual(tg.UserFeedback, other.UserFeedback) {
			panic("User feedback should be the same for all children")
		}
		userFeedback = tg.UserFeedback
	}

	newGraph := make(map[string]*Item)
	for id, item := range tg.Graph {
		newGraph[id] = item
	}
	for id, item := range other.Graph {
		newGraph[id] = item
	}

	return &TraceGraph{
		Graph:        newGraph,
		UserFeedback: userFeedback,
	}
}

// Len returns the number of nodes in the graph
func (tg *TraceGraph) Len() int {
	return len(tg.Graph)
}

// GraphPropagator is responsible for propagating feedback through the graph
type GraphPropagator struct {
	graph Graph
}

func NewGraphPropagator(g Graph) *GraphPropagator {
	return &GraphPropagator{graph: g}
}

func (gp *GraphPropagator) InitFeedback(node Node, feedback interface{}) *TraceGraph {
	return NewTraceGraph([]Node{node}, feedback)
}

func (gp *GraphPropagator) Propagate(child Node) map[Node]*TraceGraph {
	result := make(map[Node]*TraceGraph)
	childFeedback := child.Feedback()

	for _, parent := range child.Parents() {
		parentGraph := NewTraceGraph([]Node{parent}, childFeedback.UserFeedback)
		result[parent] = parentGraph.Add(childFeedback)
	}

	if messageNode, ok := child.(*MessageNode); ok {
		// Handle hidden dependencies for MessageNode
		for _, param := range messageNode.HiddenDependencies() {
			workflowNode, ok := param.(WorkflowNode)
			if !ok {
				continue
			}
			workflowNode.AddFeedback(child, childFeedback)
		}
	}

	return result
}

func (gp *GraphPropagator) Aggregate(feedbacks map[Node][]*TraceGraph) *TraceGraph {
	var result *TraceGraph

	for _, feedbackList := range feedbacks {
		for _, feedback := range feedbackList {
			if result == nil {
				result = feedback
			} else {
				result = result.Add(feedback)
			}
		}
	}

	return result
}

// PropagateMinimalSubgraph extracts the minimal subgraph between start and end nodes
func (gp *GraphPropagator) PropagateMinimalSubgraph(start, end Node) *TraceGraph {
	subgraph := NewTraceGraph(nil, nil)
	visited := make(map[string]bool)
	gp.dfs(start, end, subgraph, visited)
	return subgraph
}

func (gp *GraphPropagator) PropagateFullGraph(feedback *Feedback) *TraceGraph {
	outputNode := findOutputNode(gp.graph)
	if outputNode == nil {
		return NewTraceGraph(nil, feedback)
	}

	subgraph := gp.PropagateMinimalSubgraph(gp.graph.Nodes()[0], outputNode)
	subgraph.UserFeedback = feedback

	return subgraph
}

// dfs performs a depth-first search to build the minimal subgraph
func (gp *GraphPropagator) dfs(current, end Node, subgraph *TraceGraph, visited map[string]bool) {
	if visited[current.ID()] {
		return
	}
	visited[current.ID()] = true
	subgraph.Graph[current.ID()] = &Item{Node: current, Priority: current.Level()}

	if current == end {
		return
	}

	for _, child := range current.Children() {
		gp.dfs(child, end, subgraph, visited)
	}
}

// SumFeedback sums the feedback from multiple nodes
func SumFeedback(nodes []Node) *TraceGraph {
	var result *TraceGraph
	for _, node := range nodes {
		feedback := node.Feedback()
		if feedback == nil {
			continue
		}
		if result == nil {
			result = feedback
		} else {
			result = result.Add(feedback)
		}
	}
	return result
}

func (tg *TraceGraph) ExtractParameterUpdates() map[string]interface{} {
	updates := make(map[string]interface{})
	for id, item := range tg.Graph {
		if item.Node.Type() == ParameterNode {
			updates[id] = item.Node.Value()
		}
	}
	return updates
}

// Additional types and methods that need to be implemented:

type MessageNode struct {
	WorkflowNode
	info map[string]interface{}
}

func (n *MessageNode) Inputs() map[string][]Node {
	return n.info["inputs"].(map[string][]Node)
}

func (n *MessageNode) ParameterDependencies() []Node {
	output, ok := n.info["output"].(*MessageNode)
	if !ok {
		return nil
	}
	return output.ParameterDependencies()
}

func (n *MessageNode) ExpandableDependencies() []Node {
	output, ok := n.info["output"].(*MessageNode)
	if !ok {
		return nil
	}
	return output.ExpandableDependencies()
}

func (n *MessageNode) Backward(feedback string, retainGraph bool) {
	output, ok := n.info["output"].(*MessageNode)
	if !ok {
		return
	}
	output.SetFeedback(&Feedback{Message: feedback})
	if retainGraph {
		// Implement graph retention logic if needed
	}
}

func (n *MessageNode) HiddenDependencies() []Node {
	hiddenDeps, ok := n.info["hidden_dependencies"].([]Node)
	if !ok {
		return nil
	}
	return hiddenDeps
}

func (f *Feedback) Equals(other *Feedback) bool {
	return f.Score == other.Score && f.Message == other.Message
}
