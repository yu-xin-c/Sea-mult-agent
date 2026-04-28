package executor

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Engine 双层并发 DAG 调度引擎
type Engine struct {
	gk          *Gatekeeper
	box         Sandbox
	cp          CheckpointManager
	agent       Agent
	NodeTimeout time.Duration
}

func NewEngine(gk *Gatekeeper, box Sandbox, cp CheckpointManager, agent Agent) *Engine {
	return &Engine{
		gk:          gk,
		box:         box,
		cp:          cp,
		agent:       agent,
		NodeTimeout: 15 * time.Minute, // 默认 15 分钟
	}
}

// ExecuteGraph 拓扑驱动的并发调度
func (e *Engine) ExecuteGraph(ctx context.Context, graph *DAG) error {
	inDegrees := make(map[string]int)
	outEdges := make(map[string][]*Node)
	var mu sync.Mutex

	// 初始化入度和出边映射
	for id, node := range graph.Nodes {
		inDegrees[id] = node.InDegree
		for _, dep := range node.Dependencies {
			outEdges[dep.ID] = append(outEdges[dep.ID], node)
		}
	}

	readyQueue := make(chan *Node, len(graph.Nodes))
	for _, node := range graph.Nodes {
		if inDegrees[node.ID] == 0 {
			readyQueue <- node
		}
	}

	var wg sync.WaitGroup
	errChan := make(chan error, 1)
	doneNodes := make(chan string, len(graph.Nodes))
	totalNodes := len(graph.Nodes)
	completedCount := 0

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// 调度处理器
	go func() {
		for {
			select {
			case nodeID := <-doneNodes:
				mu.Lock()
				completedCount++
				count := completedCount
				// 消解下游节点入度
				for _, child := range outEdges[nodeID] {
					inDegrees[child.ID]--
					if inDegrees[child.ID] == 0 {
						readyQueue <- child
					}
				}
				mu.Unlock()
				if count == totalNodes {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	// 启动 Worker 处理 readyQueue
LOOP:
	for {
		mu.Lock()
		if completedCount == totalNodes {
			mu.Unlock()
			break
		}
		mu.Unlock()

		select {
		case node := <-readyQueue:
			wg.Add(1)
			go func(n *Node) {
				defer wg.Done()
				if err := e.executeNode(ctx, n); err != nil {
					select {
					case errChan <- fmt.Errorf("node %s failed: %w", n.ID, err):
					default:
					}
					return
				}

				if e.cp != nil {
					id, err := e.cp.Commit(ctx, n.ID)
					if err != nil {
						fmt.Printf("[Checkpoint] Warning: Commit failed for %s: %v\n", n.ID, err)
					} else {
						fmt.Printf("[Checkpoint] Node %s committed as image %s\n", n.ID, id)
					}
				}
				doneNodes <- n.ID
			}(node)

		case err := <-errChan:
			cancel()
			wg.Wait()
			return err

		case <-ctx.Done():
			break LOOP

		case <-time.After(100 * time.Millisecond):
			// 定期唤醒以检查完成状态，防止 select 永久阻塞在 readyQueue
			continue
		}
	}

	wg.Wait()
	return nil
}

// executeNode 节点内竞速机制 (Winner-Takes-All) 并集成 Gatekeeper
func (e *Engine) executeNode(ctx context.Context, node *Node) error {
	fmt.Printf("[Engine] Executing node: %s\n", node.ID)

	if e.gk != nil {
		if err := e.gk.AcquireIO(ctx, 1); err != nil {
			return fmt.Errorf("failed to acquire IO quota: %w", err)
		}
		defer e.gk.ReleaseIO(1)
	}

	if len(node.Commands) == 0 || (len(node.Commands) == 1 && node.Commands[0] == "") {
		if e.agent != nil {
			strategies, err := e.agent.GenerateStrategies(ctx, node.ID)
			if err == nil && len(strategies) > 0 {
				node.Commands = strategies
			}
		}
	}

	nodeCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	resultChan := make(chan string, len(node.Commands))
	var errorMu sync.Mutex
	errorCount := 0
	var lastErr error

	for _, cmd := range node.Commands {
		go func(c string) {
			res, err := e.box.Execute(nodeCtx, c)
			if err != nil {
				errorMu.Lock()
				errorCount++
				lastErr = err
				count := errorCount
				errorMu.Unlock()

				if count == len(node.Commands) {
					// 全部失败
					select {
					case resultChan <- "": // 用空字符串代表全部失败
					case <-nodeCtx.Done():
					}
				}
				return
			}

			select {
			case resultChan <- res:
				cancel()
			case <-nodeCtx.Done():
			}
		}(cmd)
	}

	timeout := e.NodeTimeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	select {
	case res := <-resultChan:
		if res == "" {
			errorMu.Lock()
			err := lastErr
			errorMu.Unlock()
			return fmt.Errorf("all commands failed, last error: %w", err)
		}
		fmt.Printf("[Engine] Node %s success. Result: %s\n", node.ID, res)
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("node %s timed out after %v", node.ID, timeout)
	case <-ctx.Done():
		return ctx.Err()
	}
}
