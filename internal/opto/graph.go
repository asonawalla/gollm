// File: internal/opto/graph.go

package opto

import (
	"fmt"
	"reflect"
	"sync"
)

// Node represents a node in the computational graph
type Node interface {
	ID() string
	Type() string
	Level() int
	Parents() []Node
	Children() []Node
	Value() interface{}
	Parameter() Parameter
	UpdateValue(interface{}) error
	Feedback() *TraceGraph
	ZeroFeedback()
	SetFeedback(*Feedback)
}

// Graph represents the computational graph
type Graph interface {
	AddNode(node Node)
	AddEdge(from, to Node) error
	RemoveEdge(from, to Node) error
	AddWorkflowNode(node WorkflowNode)
	Nodes() []Node
	WorkflowNodes() []WorkflowNode
	ExtractMinimalSubgraph(start, end Node) Graph
	GetParameterNodes() []Node
	BuildGraph(workflow interface{}) error
	BFS(start Node, visit func(Node) bool) error
	DFS(start Node, visit func(Node) bool) error
	TopologicalSort() ([]Node, error)
}

type node struct {
	id        string
	nodeType  string
	value     interface{}
	parameter Parameter
	parents   []Node
	children  []Node
	feedback  *TraceGraph
	mu        sync.RWMutex
}

func NewNode(id string, nodeType string, value interface{}, parameter Parameter) Node {
	return &node{id: id, nodeType: nodeType, value: value, parameter: parameter}
}

func (n *node) Level() int {
	// Implement the logic to determine the level of the node
	// For now, let's return 0 as a placeholder
	return 0
}

func (n *node) ID() string           { return n.id }
func (n *node) Type() string         { return n.nodeType }
func (n *node) Value() interface{}   { return n.value }
func (n *node) Parameter() Parameter { return n.parameter }

func (n *node) UpdateValue(v interface{}) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.value = v
	return nil
}

func (n *node) Parents() []Node {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.parents
}

func (n *node) Children() []Node {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.children
}

func (n *node) Feedback() *TraceGraph {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.feedback
}

func (n *node) ZeroFeedback() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.feedback = nil
}

func (n *node) SetFeedback(f *Feedback) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.feedback = NewTraceGraph([]Node{n}, f)
}

func (n *node) AddParent(parent Node) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.parents = append(n.parents, parent)
}

func (n *node) AddChild(child Node) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.children = append(n.children, child)
}

type graph struct {
	nodes         map[string]Node
	workflowNodes map[string]WorkflowNode
	edges         map[string]map[string]bool
	mu            sync.RWMutex
}

func NewGraph() Graph {
	return &graph{
		nodes:         make(map[string]Node),
		workflowNodes: make(map[string]WorkflowNode),
		edges:         make(map[string]map[string]bool),
	}
}

func (g *graph) AddNode(node Node) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.nodes[node.ID()] = node
}

func (g *graph) Nodes() []Node {
	g.mu.RLock()
	defer g.mu.RUnlock()
	nodes := make([]Node, 0, len(g.nodes))
	for _, node := range g.nodes {
		nodes = append(nodes, node)
	}
	return nodes
}

func (g *graph) ExtractMinimalSubgraph(start, end Node) Graph {
	subgraph := NewGraph()
	visited := make(map[string]bool)

	var dfs func(Node)
	dfs = func(n Node) {
		if visited[n.ID()] {
			return
		}
		visited[n.ID()] = true
		subgraph.AddNode(n)

		if n == end {
			return
		}

		for _, child := range n.Children() {
			dfs(child)
		}
	}

	dfs(start)

	return subgraph
}

func (g *graph) GetParameterNodes() []Node {
	g.mu.RLock()
	defer g.mu.RUnlock()
	var paramNodes []Node
	for _, node := range g.nodes {
		if node.Parameter() != nil {
			paramNodes = append(paramNodes, node)
		}
	}
	return paramNodes
}

func (g *graph) BuildGraph(workflow interface{}) error {
	v := reflect.ValueOf(workflow)
	if v.Kind() != reflect.Struct {
		return fmt.Errorf("workflow must be a struct")
	}

	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		fieldName := v.Type().Field(i).Name

		node := NewNode(fieldName, "OperationNode", field.Interface(), nil)
		g.AddNode(node)

		if field.Kind() == reflect.Func {
			// If the field is a function, we treat it as an operation
			g.addOperationNode(node, field)
		} else if field.CanInterface() && reflect.TypeOf(field.Interface()).Implements(reflect.TypeOf((*Parameter)(nil)).Elem()) {
			// If the field implements the Parameter interface, we treat it as a parameter node
			g.addParameterNode(node, field.Interface().(Parameter))
		}
	}

	return nil
}

func (g *graph) addOperationNode(node Node, field reflect.Value) {
	// Implementation to add operation node and its connections
	// This would involve analyzing the function signature and creating appropriate connections
}

func (g *graph) addParameterNode(node Node, param Parameter) {
	// Implementation to add parameter node
	// This might involve creating connections to operations that use this parameter
}

// WorkflowNodeType represents different types of nodes in a computational workflow
type WorkflowNodeType int

const (
	InputNode     = "InputNode"
	OperationNode = "OperationNode"
	OutputNode    = "OutputNode"
	ParameterNode = "ParameterNode"
)

// WorkflowNode represents a node in a computational workflow
type WorkflowNode interface {
	Node
	NodeType() string
	Dependencies() []WorkflowNode
	AddDependency(dep WorkflowNode)
	AddFeedback(child Node, feedback *TraceGraph)
}

type workflowNode struct {
	node
	nodeType     string
	dependencies []WorkflowNode
}

func (wn *workflowNode) AddFeedback(child Node, feedback *TraceGraph) {
	// Implement the logic to add feedback
	// For now, let's just set the feedback
	wn.SetFeedback(feedback.UserFeedback.(*Feedback))
}

func (wn *workflowNode) NodeType() string {
	return wn.nodeType
}

func NewWorkflowNode(id string, nodeType string, value interface{}, parameter Parameter) WorkflowNode {
	return &workflowNode{
		node:     node{id: id, nodeType: nodeType, value: value, parameter: parameter},
		nodeType: nodeType,
	}
}

func (wn *workflowNode) Level() int {
	// Implement the logic to determine the level of the workflow node
	// For now, let's return the level of the underlying node
	return wn.node.Level()
}

func (wn *workflowNode) Dependencies() []WorkflowNode {
	return wn.dependencies
}

func (wn *workflowNode) AddDependency(dep WorkflowNode) {
	wn.dependencies = append(wn.dependencies, dep)
	wn.AddParent(dep)
}

func (g *graph) AddWorkflowNode(node WorkflowNode) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.workflowNodes[node.ID()] = node
	g.nodes[node.ID()] = node
}

func (g *graph) WorkflowNodes() []WorkflowNode {
	g.mu.RLock()
	defer g.mu.RUnlock()
	nodes := make([]WorkflowNode, 0, len(g.workflowNodes))
	for _, node := range g.workflowNodes {
		nodes = append(nodes, node)
	}
	return nodes
}

func (g *graph) AddEdge(from, to Node) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if _, exists := g.nodes[from.ID()]; !exists {
		return fmt.Errorf("node %s does not exist", from.ID())
	}
	if _, exists := g.nodes[to.ID()]; !exists {
		return fmt.Errorf("node %s does not exist", to.ID())
	}

	if g.edges[from.ID()] == nil {
		g.edges[from.ID()] = make(map[string]bool)
	}
	g.edges[from.ID()][to.ID()] = true

	return nil
}

func (g *graph) RemoveEdge(from, to Node) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if _, exists := g.edges[from.ID()]; !exists {
		return fmt.Errorf("edge does not exist")
	}

	delete(g.edges[from.ID()], to.ID())
	return nil
}

func (g *graph) BFS(start Node, visit func(Node) bool) error {
	g.mu.RLock()
	defer g.mu.RUnlock()

	queue := []Node{start}
	visited := make(map[string]bool)

	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]

		if visited[node.ID()] {
			continue
		}

		visited[node.ID()] = true
		if !visit(node) {
			return nil
		}

		for childID := range g.edges[node.ID()] {
			if !visited[childID] {
				queue = append(queue, g.nodes[childID])
			}
		}
	}

	return nil
}

func (g *graph) DFS(start Node, visit func(Node) bool) error {
	g.mu.RLock()
	defer g.mu.RUnlock()

	visited := make(map[string]bool)
	var dfsRecursive func(Node) bool

	dfsRecursive = func(node Node) bool {
		visited[node.ID()] = true
		if !visit(node) {
			return false
		}

		for childID := range g.edges[node.ID()] {
			if !visited[childID] {
				if !dfsRecursive(g.nodes[childID]) {
					return false
				}
			}
		}

		return true
	}

	return nil
}

func (g *graph) TopologicalSort() ([]Node, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	visited := make(map[string]bool)
	stack := make([]Node, 0)

	var visit func(Node) error
	visit = func(node Node) error {
		visited[node.ID()] = true

		for childID := range g.edges[node.ID()] {
			if !visited[childID] {
				if err := visit(g.nodes[childID]); err != nil {
					return err
				}
			}
		}

		stack = append([]Node{node}, stack...)
		return nil
	}

	for _, node := range g.nodes {
		if !visited[node.ID()] {
			if err := visit(node); err != nil {
				return nil, err
			}
		}
	}

	return stack, nil
}
