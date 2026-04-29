package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"scholar-agent-backend/internal/appconfig"
	"scholar-agent-backend/internal/models"
	"scholar-agent-backend/internal/prompts"
	"scholar-agent-backend/internal/sandbox"
	"strings"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

const (
	defaultSandboxImage  = "python:3.9-bullseye"
	defaultPipTimeoutSec = "60"
)

// Agent 接口定义了系统中自治工作者的标准行为
type Agent interface {
	// ExecuteTask 执行分配给该 Agent 的具体任务
	ExecuteTask(ctx context.Context, task *models.Task, sharedContext map[string]interface{}) error
}

// logToContext 辅助函数，将日志同时输出到控制台和 Context 中的 Channel，以支持 SSE 流式返回
func logToContext(ctx context.Context, format string, v ...interface{}) {
	msg := fmt.Sprintf(format, v...)
	log.Println(msg)
	if ch, ok := ctx.Value("logChannel").(chan string); ok {
		// 非阻塞发送
		select {
		case ch <- msg:
		default:
		}
	}
}

// CoderAgent 负责根据需求生成代码，并在沙箱中迭代测试
type CoderAgent struct {
	Name          string
	SystemPrompt  string
	Sandbox       *sandbox.SandboxClient
	ChatModel     *openai.ChatModel
	CodeOnlyChain compose.Runnable[string, string]
	EinoChain     compose.Runnable[string, string] // 使用 Eino 编排的执行链
}

func BuildCoderSystemPrompt() string {
	return "你是一个资深的 AI 科研助理和 Python 开发者。你的任务是在 Docker 沙箱（python:3.9-bullseye 镜像）中生成可运行的 Python 代码。\n\n" +
		"【通用约束】：\n" +
		"1. 只输出纯 Python 代码，不要 Markdown 包裹。\n" +
		"2. 依赖名称必须保持通用，例如 your-package-name；不要在通用提示词中写死具体框架。\n" +
		"3. 不依赖外网 API、私有密钥或远程数据集，优先使用本地构造样例。\n" +
		"4. 运行结束后输出 JSON 格式的结果摘要，便于调度器解析。\n"
}

type coderContextKey string

const (
	coderSystemPromptContextKey coderContextKey = "coder_system_prompt"
	coderIntentTypeContextKey   coderContextKey = "coder_intent_type"
	coderTaskTypeContextKey     coderContextKey = "coder_task_type"
	coderTaskNameContextKey     coderContextKey = "coder_task_name"
)

// NewCoderAgent 实例化一个新的 CoderAgent，并初始化真实的 Eino 执行链
func NewCoderAgent(sandbox *sandbox.SandboxClient) *CoderAgent {
	agent := &CoderAgent{
		Name:         "coder_agent",
		SystemPrompt: prompts.CoderSystemPrompt,
		Sandbox:      sandbox,
	}
	agent.initRealEinoChain()

	return agent
}

// initRealEinoChain 使用字节跳动 Eino 框架和真实的 LLM 模型编排逻辑流
func (a *CoderAgent) initRealEinoChain() {
	// 1. 初始化真实的 LLM ChatModel
	llmCfg, err := appconfig.LoadLLMConfig()
	if err != nil {
		log.Fatalf("加载 LLM 配置失败: %v", err)
	}

	chatModel, err := openai.NewChatModel(context.Background(), &openai.ChatModelConfig{
		BaseURL: llmCfg.BaseURL,
		APIKey:  llmCfg.APIKey,
		Model:   llmCfg.Model,
	})
	if err != nil {
		log.Fatalf("初始化大模型失败: %v", err)
	}
	a.ChatModel = chatModel

	// 2. 构建 Eino Graph
	codeOnlyGraph := compose.NewGraph[string, string]()
	codeOnlyGraph.AddLambdaNode("Prompt_Builder", compose.InvokableLambda(func(ctx context.Context, input string) ([]*schema.Message, error) {
		logToContext(ctx, "[%s] Eino 节点 [Prompt_Builder]: 正在组装代码生成提示词", a.Name)
		systemPrompt := coderSystemPromptFromContext(ctx, a.SystemPrompt)
		messages := []*schema.Message{
			{Role: schema.System, Content: systemPrompt},
			{Role: schema.User, Content: prompts.CoderTaskUserPrompt(input)},
		}
		return messages, nil
	}))
	codeOnlyGraph.AddChatModelNode("LLM_Generate_Code", chatModel)
	codeOnlyGraph.AddLambdaNode("Code_Extractor", compose.InvokableLambda(func(ctx context.Context, msg *schema.Message) (string, error) {
		logToContext(ctx, "[%s] Eino 节点 [Code_Extractor]: 开始提取大模型生成的代码", a.Name)
		code := msg.Content
		code = strings.TrimPrefix(code, "```python\n")
		code = strings.TrimPrefix(code, "```python")
		code = strings.TrimSuffix(code, "```")
		logToContext(ctx, "[%s] 成功提取到可执行代码", a.Name)
		if codeChan, ok := ctx.Value("codeChannel").(chan string); ok {
			codeChan <- code
		}
		return code, nil
	}))
	codeOnlyGraph.AddEdge(compose.START, "Prompt_Builder")
	codeOnlyGraph.AddEdge("Prompt_Builder", "LLM_Generate_Code")
	codeOnlyGraph.AddEdge("LLM_Generate_Code", "Code_Extractor")
	codeOnlyGraph.AddEdge("Code_Extractor", compose.END)

	codeOnlyRunnable, err := codeOnlyGraph.Compile(context.Background())
	if err != nil {
		log.Fatalf("编译 Eino CodeOnly 链失败: %v", err)
	}
	a.CodeOnlyChain = codeOnlyRunnable

	graph := compose.NewGraph[string, string]()

	// 节点 1: 提示词模板 (Prompt Template)
	graph.AddLambdaNode("Prompt_Builder", compose.InvokableLambda(func(ctx context.Context, input string) ([]*schema.Message, error) {
		logToContext(ctx, "[%s] Eino 节点 [Prompt_Builder]: 正在组装提示词", a.Name)
		systemPrompt := coderSystemPromptFromContext(ctx, a.SystemPrompt)
		messages := []*schema.Message{
			{Role: schema.System, Content: systemPrompt},
			{Role: schema.User, Content: prompts.CoderTaskUserPrompt(input)},
		}
		return messages, nil
	}))

	// 节点 2: LLM 生成代码 (调用真实的 ChatModel)
	graph.AddChatModelNode("LLM_Generate_Code", chatModel)

	// 节点 3: 清洗并提取代码
	graph.AddLambdaNode("Code_Extractor", compose.InvokableLambda(func(ctx context.Context, msg *schema.Message) (string, error) {
		logToContext(ctx, "[%s] Eino 节点 [Code_Extractor]: 开始提取大模型生成的代码", a.Name)
		code := msg.Content
		// 简单清洗可能附带的 Markdown 标记
		code = strings.TrimPrefix(code, "```python\n")
		code = strings.TrimPrefix(code, "```python")
		code = strings.TrimSuffix(code, "```")
		logToContext(ctx, "[%s] 成功提取到可执行代码", a.Name)

		// 保存代码到 Context，以便外部获取
		if codeChan, ok := ctx.Value("codeChannel").(chan string); ok {
			codeChan <- code
		}

		return code, nil
	}))

	// 节点 4: 沙箱执行与自愈循环 (Self-Correction Loop)
	graph.AddLambdaNode("Sandbox_Execute", compose.InvokableLambda(func(ctx context.Context, code string) (string, error) {
		logToContext(ctx, "[%s] Eino 节点 [Sandbox_Execute]: 准备将代码送入持久化 Docker 沙箱执行", a.Name)
		if a.Sandbox == nil {
			logToContext(ctx, "[Warning] 沙箱未初始化，跳过实际执行")
			return "【由于本地未安装或未启动 Docker Desktop，跳过沙箱执行环节】\n\n大模型生成的代码如下：\n\n" + code, nil
		}

		// 获取预先创建好的长生命周期容器 ID
		containerID, ok := ctx.Value("containerID").(string)
		if !ok || containerID == "" {
			logToContext(ctx, "[Warning] 无法获取长生命周期容器 ID，降级为单次执行模式")
			// 创建临时沙箱执行
			tempID, err := a.Sandbox.CreatePersistentSandbox(ctx, "temp", defaultSandboxImage, "")
			if err != nil {
				return "", fmt.Errorf("创建临时沙箱失败: %w", err)
			}
			defer a.Sandbox.CleanupSandbox(context.Background(), tempID)

			res, err := a.Sandbox.RunPythonCode(ctx, tempID, code)
			if err != nil {
				return "", fmt.Errorf("沙箱执行失败: %w", err)
			}

			// 处理图片输出
			if len(res.Images) > 0 {
				logToContext(ctx, "[%s] 检测到代码生成了 %d 张图表，正在推送至前端...", a.Name, len(res.Images))
				if codeChan, ok := ctx.Value("codeChannel").(chan string); ok {
					for _, imgBase64 := range res.Images {
						// 通过特殊的标识符发送图片给前端
						codeChan <- "IMAGE:" + imgBase64
					}
				}
			}

			return res.Stdout + "\n" + res.Stderr, nil
		}

		maxRetries := 3
		currentCode := code
		var finalOutput string

		for i := 0; i < maxRetries; i++ {
			logToContext(ctx, "[%s] 开始在持久化容器中执行代码 (第 %d/%d 次)...", a.Name, i+1, maxRetries)

			// 改进执行逻辑：直接使用 RunPythonCode 以便沙箱能够拦截并返回图表
			res, err := a.Sandbox.RunPythonCode(ctx, containerID, currentCode)

			var output string
			if res != nil {
				output = res.Stdout + "\n" + res.Stderr
				// 处理图片输出
				if len(res.Images) > 0 {
					logToContext(ctx, "[%s] 检测到代码生成了 %d 张图表，正在推送至前端...", a.Name, len(res.Images))
					if codeChan, ok := ctx.Value("codeChannel").(chan string); ok {
						for _, imgBase64 := range res.Images {
							codeChan <- "IMAGE:" + imgBase64
						}
					}
				}
			}

			if err != nil || (res != nil && res.ExitCode != 0) {
				if err == nil {
					err = fmt.Errorf("exit code: %d", res.ExitCode)
				}
				logToContext(ctx, "[%s] 代码执行失败，触发 Self-Correction 机制。错误: %v", a.Name, err)

				// 构建修正 Prompt 再次调用大模型
				correctionPrompt := prompts.CoderSelfCorrectionUserPrompt(err, output)

				logToContext(ctx, "[%s] 正在调用大模型进行代码自修复...", a.Name)
				msg, err := chatModel.Generate(ctx, []*schema.Message{
					{Role: schema.System, Content: coderSystemPromptFromContext(ctx, a.SystemPrompt)},
					{Role: schema.User, Content: correctionPrompt},
				})
				if err != nil {
					logToContext(ctx, "[%s] 错误: 自修复调用大模型失败: %v", a.Name, err)
					return "", fmt.Errorf("Self-Correction 调用大模型失败: %w", err)
				}

				currentCode = strings.TrimPrefix(msg.Content, "```python\n")
				currentCode = strings.TrimPrefix(currentCode, "```python")
				currentCode = strings.TrimSuffix(currentCode, "```")

				finalOutput = fmt.Sprintf("执行失败，已尝试修复。错误日志:\n%v\n输出:\n%s", err, output)
				continue // 尝试用新代码再次运行
			}

			// 成功执行
			logToContext(ctx, "[%s] 持久化沙箱执行成功！获取到执行结果。", a.Name)
			return output, nil
		}

		logToContext(ctx, "[%s] 达到最大重试次数，任务执行失败", a.Name)
		return finalOutput + "\n\n【达到最大重试次数，任务执行失败】", fmt.Errorf("达到最大重试次数")
	}))

	// 3. 定义边 (Edges) 将节点串联起来
	graph.AddEdge(compose.START, "Prompt_Builder")
	graph.AddEdge("Prompt_Builder", "LLM_Generate_Code")
	graph.AddEdge("LLM_Generate_Code", "Code_Extractor")
	graph.AddEdge("Code_Extractor", "Sandbox_Execute")
	graph.AddEdge("Sandbox_Execute", compose.END)

	// 4. 编译成 Runnable 链
	runnable, err := graph.Compile(context.Background())
	if err != nil {
		log.Fatalf("编译 Eino 链失败: %v", err)
	}

	a.EinoChain = runnable
	log.Printf("[%s] 成功初始化基于真实大模型的 Eino 执行链", a.Name)
}

// initMockEinoChain 保留作为兜底的 Mock 链
func (a *CoderAgent) initMockEinoChain() {
	graph := compose.NewGraph[string, string]()
	graph.AddLambdaNode("LLM_Generate_Code", compose.InvokableLambda(func(ctx context.Context, input string) (string, error) {
		return a.mockLLMGenerateCode(input), nil
	}))
	graph.AddLambdaNode("Sandbox_Execute", compose.InvokableLambda(func(ctx context.Context, code string) (string, error) {
		if a.Sandbox == nil {
			return code, nil
		}
		// 临时执行环境
		tempID, _ := a.Sandbox.CreatePersistentSandbox(ctx, "mock", defaultSandboxImage, "")
		defer a.Sandbox.CleanupSandbox(context.Background(), tempID)

		// 同样写入文件执行，提高成功率
		tempDir, _ := os.MkdirTemp("", "scholar_workspace_mock_")
		defer os.RemoveAll(tempDir)
		scriptPath := filepath.Join(tempDir, "run_script.py")
		_ = os.MkdirAll(filepath.Dir(scriptPath), 0777)
		_ = os.WriteFile(scriptPath, []byte(code), 0666)

		res, _ := a.Sandbox.RunPythonCode(ctx, tempID, code)
		return res.Stdout + res.Stderr, nil
	}))
	graph.AddEdge(compose.START, "LLM_Generate_Code")
	graph.AddEdge("LLM_Generate_Code", "Sandbox_Execute")
	graph.AddEdge("Sandbox_Execute", compose.END)
	runnable, err := graph.Compile(context.Background())
	if err != nil {
		log.Fatalf("编译 Eino 链失败: %v", err)
	}
	a.EinoChain = runnable
}

func coderSystemPromptFromContext(ctx context.Context, fallback string) string {
	if prompt, ok := ctx.Value(coderSystemPromptContextKey).(string); ok && strings.TrimSpace(prompt) != "" {
		return prompt
	}
	return fallback
}

func sharedContextValue(sharedContext map[string]interface{}, key string) string {
	if sharedContext == nil {
		return ""
	}
	value, ok := sharedContext[key]
	if !ok || value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

func (a *CoderAgent) contextWithTaskPrompt(ctx context.Context, task *models.Task, sharedContext map[string]interface{}) context.Context {
	if task == nil {
		return ctx
	}
	intentType := sharedContextValue(sharedContext, "intent_type")
	systemPrompt := prompts.CoderSystemPromptForTask(intentType, task.Type, task.Name, task.Description)
	ctx = context.WithValue(ctx, coderSystemPromptContextKey, systemPrompt)
	ctx = context.WithValue(ctx, coderIntentTypeContextKey, intentType)
	ctx = context.WithValue(ctx, coderTaskTypeContextKey, task.Type)
	ctx = context.WithValue(ctx, coderTaskNameContextKey, task.Name)
	return ctx
}

func deterministicFrameworkBenchmarkCode(task *models.Task) (string, bool) {
	target := frameworkBenchmarkTarget(task)
	if target == "" || task == nil || strings.TrimSpace(task.Type) != "generate_code" {
		return "", false
	}
	return buildDeterministicFrameworkBenchmarkCode(target), true
}

func frameworkBenchmarkTarget(task *models.Task) string {
	if task == nil {
		return ""
	}
	branchText := strings.ToLower(strings.Join(append([]string{task.Name}, task.OutputArtifacts...), "\n"))
	switch {
	case strings.Contains(branchText, "llamaindex") || strings.Contains(branchText, "llama_index") || strings.Contains(branchText, "llama-index"):
		return "llamaindex"
	case strings.Contains(branchText, "langchain"):
		return "langchain"
	}

	description := strings.ToLower(task.Description)
	hasLlamaIndex := strings.Contains(description, "llamaindex") || strings.Contains(description, "llama_index") || strings.Contains(description, "llama-index")
	hasLangChain := strings.Contains(description, "langchain")
	switch {
	case hasLlamaIndex && !hasLangChain:
		return "llamaindex"
	case hasLangChain && !hasLlamaIndex:
		return "langchain"
	default:
		return ""
	}
}

func buildDeterministicFrameworkBenchmarkCode(target string) string {
	packageName := "langchain"
	moduleName := "langchain"
	displayName := "LangChain"
	if target == "llamaindex" {
		packageName = "llama-index"
		moduleName = "llama_index"
		displayName = "LlamaIndex"
	}

	return fmt.Sprintf(`import hashlib
import json
import math
import statistics
import time

FRAMEWORK = %q
PACKAGE_NAME = %q
FRAMEWORK_MODULE = %q

DOCS = [
    {"id": "doc_rag", "text": "RAG systems combine retrieval over private documents with a generator that answers using grounded context."},
    {"id": "doc_eval", "text": "Offline framework benchmarks should compare indexing latency, retrieval latency, recall, throughput, and stability under identical data."},
    {"id": "doc_agent", "text": "Agent orchestration frameworks focus on tool calling, state transitions, retries, observability, and workflow recovery."},
    {"id": "doc_vector", "text": "Vector stores support similarity search over embeddings and often add metadata filters, persistence, and approximate nearest neighbor indexes."},
]

QUERIES = [
    {"query": "how should a RAG benchmark compare frameworks", "expected": "doc_eval"},
    {"query": "what does RAG combine", "expected": "doc_rag"},
    {"query": "what do agent frameworks focus on", "expected": "doc_agent"},
    {"query": "what does a vector store provide", "expected": "doc_vector"},
]

def embed(text, dim=64):
    vector = [0.0] * dim
    for token in text.lower().replace(".", " ").replace(",", " ").split():
        digest = hashlib.sha256(token.encode("utf-8")).digest()
        index = int.from_bytes(digest[:4], "big") %% dim
        sign = 1.0 if digest[4] %% 2 == 0 else -1.0
        vector[index] += sign
    norm = math.sqrt(sum(value * value for value in vector)) or 1.0
    return [value / norm for value in vector]

def cosine(left, right):
    return sum(a * b for a, b in zip(left, right))

def build_index(docs):
    start = time.perf_counter()
    index = [{"id": doc["id"], "text": doc["text"], "embedding": embed(doc["text"])} for doc in docs]
    return index, time.perf_counter() - start

def retrieve(index, query, k=3):
    query_vector = embed(query)
    scored = sorted(((cosine(query_vector, item["embedding"]), item) for item in index), key=lambda pair: pair[0], reverse=True)
    return [item for _, item in scored[:k]]

def answer(query, contexts):
    joined = " ".join(item["text"] for item in contexts)
    return "Mock answer for %%s based on %%d context chars" %% (query[:48], len(joined))

def percentile(values, ratio):
    ordered = sorted(values)
    if not ordered:
        return 0.0
    pos = min(len(ordered) - 1, int(round((len(ordered) - 1) * ratio)))
    return ordered[pos]

def main():
    version = "not_installed_offline_mock"
    index, build_seconds = build_index(DOCS)
    latencies = []
    hits = 0
    outputs = []
    for item in QUERIES:
        start = time.perf_counter()
        contexts = retrieve(index, item["query"], k=3)
        response = answer(item["query"], contexts)
        elapsed = time.perf_counter() - start
        latencies.append(elapsed)
        hits += int(any(ctx["id"] == item["expected"] for ctx in contexts))
        outputs.append({"query": item["query"], "top_ids": [ctx["id"] for ctx in contexts], "answer": response})
    total_query_seconds = sum(latencies)
    metrics = {
        "framework": FRAMEWORK,
        "package": PACKAGE_NAME,
        "framework_module": FRAMEWORK_MODULE,
        "package_version": version,
        "mode": "offline_mock_framework_benchmark",
        "offline_dependency_policy": "no external dependency installation",
        "uses_external_llm_api": False,
        "uses_external_embedding_api": False,
        "document_count": len(DOCS),
        "query_count": len(QUERIES),
        "index_build_seconds": build_seconds,
        "avg_query_latency_seconds": statistics.mean(latencies),
        "p95_query_latency_seconds": percentile(latencies, 0.95),
        "throughput_qps": len(QUERIES) / total_query_seconds if total_query_seconds else 0.0,
        "recall_at_3": hits / len(QUERIES),
        "sample_outputs": outputs,
    }
    print(json.dumps(metrics, ensure_ascii=False, indent=2))

if __name__ == "__main__":
    main()
`, displayName, packageName, moduleName)
}

func stripInlinePipInstall(code string) string {
	lines := strings.Split(code, "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "subprocess.check_call") && strings.Contains(lower, "pip") && strings.Contains(lower, "install") {
			continue
		}
		filtered = append(filtered, line)
	}
	return strings.Join(filtered, "\n")
}

func filterFrameworkBenchmarkDependencies(task *models.Task, dependencies []string) []string {
	target := frameworkBenchmarkTarget(task)
	if target == "" {
		return dependencies
	}
	return []string{}
}

// ExecuteTask 使用 Eino Chain 执行任务
func (a *CoderAgent) ExecuteTask(ctx context.Context, task *models.Task, sharedContext map[string]interface{}) error {
	if task != nil && task.Type == "resolve_dependencies" {
		return a.resolveDependenciesTask(ctx, task)
	}
	ctx = a.contextWithTaskPrompt(ctx, task, sharedContext)
	// sandbox_agent tasks must run through the deterministic sandbox path so that:
	// - runtime_session / prepared_runtime artifacts are produced consistently
	// - the scheduler can wire outputs -> inputs between plan nodes
	// Routing sandbox_agent to the Eino chain can create ephemeral sandboxes and skip
	// artifact emission, which breaks dependency installation with "missing runtime_session".
	if task != nil && task.AssignedTo == "sandbox_agent" {
		return a.executeSandboxTask(ctx, task)
	}
	log.Printf("[%s] 开始执行任务: %s (使用 Eino 驱动)", a.Name, task.Name)

	codeChan := make(chan string, 10)
	ctx = context.WithValue(ctx, "codeChannel", codeChan)

	// 在后台收集生成的代码和图片
	var codeCollector sync.WaitGroup
	codeCollector.Add(1)
	go func() {
		defer codeCollector.Done()
		for msg := range codeChan {
			if strings.HasPrefix(msg, "IMAGE:") {
				task.ImageBase64 = strings.TrimPrefix(msg, "IMAGE:")
			} else {
				task.Code = msg
			}
		}
	}()

	// 通过 Eino 链运行任务描述
	selectedChain := a.CodeOnlyChain
	usesSandbox := task.AssignedTo == "sandbox_agent"
	if usesSandbox || selectedChain == nil {
		selectedChain = a.EinoChain
	}
	if usesSandbox {
		logToContext(ctx, "[%s] 当前节点为 sandbox_agent，执行时才会尝试创建或复用沙箱容器", a.Name)
	} else {
		logToContext(ctx, "[%s] 当前节点为 %s，仅执行代码生成/检查链路，不触发沙箱创建", a.Name, task.AssignedTo)
	}
	output, err := selectedChain.Invoke(ctx, task.Description)
	close(codeChan) // 关闭通道让 goroutine 退出
	codeCollector.Wait()

	if err != nil {
		log.Printf("[%s] Eino 执行流失败: %v", a.Name, err)
		task.Status = models.StatusFailed
		task.Error = fmt.Sprintf("执行失败: %v", err)
		return err
	}

	if replacement, ok := deterministicFrameworkBenchmarkCode(task); ok {
		output = replacement
		task.Code = replacement
	} else if task.Code != "" {
		task.Code = stripInlinePipInstall(task.Code)
		output = stripInlinePipInstall(output)
	}

	log.Printf("[%s] Eino 执行流完成. 最终输出:\n%s", a.Name, output)
	task.Result = output
	task.Status = models.StatusCompleted
	return nil
}

// mockLLMGenerateCode 模拟大模型根据提示词生成 Python 代码
func (a *CoderAgent) executeSandboxTask(ctx context.Context, task *models.Task) error {
	if task == nil {
		return fmt.Errorf("task is nil")
	}
	if a.Sandbox == nil {
		task.Status = models.StatusFailed
		task.Error = "sandbox client is not configured"
		return fmt.Errorf("%s", task.Error)
	}

	switch inferSandboxTaskKind(task) {
	case "prepare_runtime":
		return a.prepareRuntime(ctx, task)
	case "install_dependencies":
		return a.installDependencies(ctx, task)
	case "execute_code":
		return a.executeCodeInSandbox(ctx, task)
	default:
		return a.executeCodeInSandbox(ctx, task)
	}
}

func (a *CoderAgent) prepareRuntime(ctx context.Context, task *models.Task) error {
	logToContext(ctx, "[%s] Preparing runtime for %s", a.Name, task.Name)

	if existing := chooseNonEmpty(extractTaskInputLike(task, "runtime_session"), extractTaskInputLike(task, "runtime_env"), extractTaskInputLike(task, "prepared_runtime")); strings.HasPrefix(existing, "dk-") || strings.HasPrefix(existing, "os-") {
		task.Result = existing
		logToContext(ctx, "[%s] Reusing existing runtime: %s", a.Name, existing)
		if task.Metadata == nil {
			task.Metadata = map[string]any{}
		}
		task.Metadata["artifact_values"] = map[string]string{
			"runtime_session": existing,
		}
		task.Status = models.StatusCompleted
		return nil
	}

	mountPath := strings.TrimSpace(extractTaskInputLike(task, "workspace_path"))
	sandboxID, err := a.Sandbox.CreatePersistentSandbox(ctx, task.ID, defaultSandboxImage, mountPath)
	if err != nil {
		task.Status = models.StatusFailed
		task.Error = fmt.Sprintf("failed to create runtime sandbox: %v", err)
		return err
	}
	task.Result = sandboxID
	logToContext(ctx, "[%s] Runtime ready: %s", a.Name, sandboxID)
	if task.Metadata == nil {
		task.Metadata = map[string]any{}
	}
	task.Metadata["artifact_values"] = buildArtifactValueMap(task, map[string]string{
		"runtime_session": sandboxID,
	})
	task.Status = models.StatusCompleted
	return nil
}

func (a *CoderAgent) installDependencies(ctx context.Context, task *models.Task) error {
	runtimeSession := chooseNonEmpty(extractTaskInputLike(task, "runtime_session"), extractTaskInputLike(task, "runtime_env"))
	if strings.TrimSpace(runtimeSession) == "" {
		task.Status = models.StatusFailed
		task.Error = "missing runtime_session input for dependency installation"
		return fmt.Errorf("%s", task.Error)
	}

	// 用于在需要时重建沙箱（例如依赖声明 Requires-Python>=3.10/3.11）
	// 说明：mountPath 允许把 repo/workspace 挂回同一个执行环境，避免后续 execute_code 找不到代码文件。
	mountPath := strings.TrimSpace(extractTaskInputLike(task, "workspace_path"))

	rawDependencySpec := extractTaskInputLike(task, "dependency_spec")
	dependencies, parseErr := parseDependencySpec(rawDependencySpec)
	if parseErr != nil {
		task.Status = models.StatusFailed
		task.Error = fmt.Sprintf("invalid dependency_spec: %v", parseErr)
		task.Result = strings.TrimSpace(rawDependencySpec)
		return fmt.Errorf("%s", task.Error)
	}
	if len(dependencies) == 0 {
		task.Result = "no external dependencies detected"
		task.Status = models.StatusCompleted
		if task.Metadata == nil {
			task.Metadata = map[string]any{}
		}
		task.Metadata["artifact_values"] = buildArtifactValueMap(task, map[string]string{
			"prepared_runtime":          runtimeSession,
			"dependency_install_report": "no external dependencies detected",
		})
		return nil
	}

	dependencies = filterFrameworkBenchmarkDependencies(task, dependencies)
	dependencies = normalizeDependenciesForPython39(dependencies)
	dependencies = filterFrameworkBenchmarkDependencies(task, dependencies)
	dependencies = filterStandardLibraryDependencies(dependencies)
	if len(dependencies) == 0 {
		task.Result = "no external dependencies detected"
		task.Status = models.StatusCompleted
		if task.Metadata == nil {
			task.Metadata = map[string]any{}
		}
		task.Metadata["artifact_values"] = buildArtifactValueMap(task, map[string]string{
			"prepared_runtime":          runtimeSession,
			"dependency_install_report": "no external dependencies detected",
		})
		return nil
	}

	// ReAct 依赖恢复：优先让模型根据 pip 日志给出结构化动作，
	// 再由规则兜底，避免把“重试”做成纯盲试。
	maxAttempts := 3
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		logToContext(ctx, "[%s] Installing dependencies in sandbox %s (attempt %d/%d): %s", a.Name, runtimeSession, attempt, maxAttempts, strings.Join(dependencies, ", "))
		cmd := append([]string{"python3", "-m", "pip", "install", "--default-timeout", defaultPipTimeoutSec}, dependencies...)
		res, err := a.Sandbox.ExecCommandStream(ctx, runtimeSession, cmd, func(stream string, line string) {
			logToContext(ctx, "[%s] pip %s: %s", a.Name, stream, line)
		})
		if err != nil {
			task.Status = models.StatusFailed
			task.Error = fmt.Sprintf("dependency installation failed: %v", err)
			return err
		}
		if res == nil {
			task.Status = models.StatusFailed
			task.Error = "dependency installation returned nil response"
			return fmt.Errorf("%s", task.Error)
		}

		logToContext(ctx, "[%s] pip install exit_code=%d", a.Name, res.ExitCode)
		if res.ExitCode == 0 {
			report := strings.TrimSpace(res.Stdout)
			if report == "" {
				report = "dependencies installed successfully"
			}
			task.Result = report
			task.Status = models.StatusCompleted
			if task.Metadata == nil {
				task.Metadata = map[string]any{}
			}
			task.Metadata["artifact_values"] = buildArtifactValueMap(task, map[string]string{
				"prepared_runtime":          runtimeSession,
				"dependency_install_report": report,
			})
			return nil
		}

		stdout := strings.TrimSpace(res.Stdout)
		stderr := strings.TrimSpace(res.Stderr)
		errText := chooseNonEmpty(stderr, stdout, fmt.Sprintf("pip install exited with code %d", res.ExitCode))

		// 没有下一次尝试了，直接失败返回
		if attempt >= maxAttempts {
			task.Status = models.StatusFailed
			task.Result = stdout
			task.Error = errText
			return fmt.Errorf("%s", task.Error)
		}

		// 第一优先级：模型基于 pip 错误做一次结构化 ReAct 决策。
		if plan, reactErr := a.planDependencyRecovery(ctx, dependencies, errText); reactErr == nil && plan.Action != "" {
			nextDependencies, nextRuntimeSession, changed, applyErr := a.applyDependencyRecoveryPlan(ctx, task, dependencies, runtimeSession, mountPath, plan)
			if applyErr == nil && changed {
				dependencies = nextDependencies
				runtimeSession = nextRuntimeSession
				logToContext(ctx, "[%s] ReAct repair applied: action=%s, reason=%s", a.Name, plan.Action, plan.Reason)
				continue
			}
			if applyErr != nil {
				logToContext(ctx, "[%s] ReAct repair plan apply failed, fallback to rules: %v", a.Name, applyErr)
			} else {
				logToContext(ctx, "[%s] ReAct repair returned no effective change, fallback to rules.", a.Name)
			}
		}

		// 第二优先级：规则兜底，覆盖已知稳定场景。
		nextDependencies, nextRuntimeSession, changed, fallbackReason, fallbackErr := a.applyRuleBasedDependencyFallback(ctx, task, dependencies, runtimeSession, mountPath, errText)
		if fallbackErr != nil {
			task.Status = models.StatusFailed
			task.Result = stdout
			task.Error = fallbackErr.Error()
			return fallbackErr
		}
		if changed {
			dependencies = nextDependencies
			runtimeSession = nextRuntimeSession
			logToContext(ctx, "[%s] Rule-based dependency fallback applied: %s", a.Name, fallbackReason)
			continue
		}

		// 没有命中的自动纠正规则：保留原错误直接返回，避免无限重试
		task.Status = models.StatusFailed
		task.Result = stdout
		task.Error = errText
		return fmt.Errorf("%s", task.Error)
	}

	// 理论上不会到达这里（for 循环内已 return）
	task.Status = models.StatusFailed
	task.Error = "dependency installation failed after retries"
	return fmt.Errorf("%s", task.Error)
}

type dependencyRecoveryPlan struct {
	Action           string   `json:"action"`
	Reason           string   `json:"reason"`
	RemovePackage    string   `json:"remove_package"`
	ReplacePackage   string   `json:"replace_package"`
	WithPackage      string   `json:"with_package"`
	TargetImage      string   `json:"target_image"`
	NextDependencies []string `json:"next_dependencies"`
}

func (a *CoderAgent) planDependencyRecovery(ctx context.Context, dependencies []string, pipError string) (*dependencyRecoveryPlan, error) {
	if a == nil || a.ChatModel == nil {
		return nil, fmt.Errorf("chat model is not configured")
	}

	userPrompt := prompts.DependencyRecoveryUserPrompt(mustJSON(dependencies), pipError)
	msg, err := a.ChatModel.Generate(ctx, []*schema.Message{
		{Role: schema.System, Content: prompts.DependencyRecoverySystemPrompt},
		{Role: schema.User, Content: userPrompt},
	})
	if err != nil {
		return nil, err
	}

	cleaned := cleanJSONFence(msg.Content)
	var plan dependencyRecoveryPlan
	if err := json.Unmarshal([]byte(cleaned), &plan); err != nil {
		return nil, fmt.Errorf("parse dependency recovery plan failed: %w", err)
	}
	plan.Action = strings.TrimSpace(plan.Action)
	plan.Reason = strings.TrimSpace(plan.Reason)
	return &plan, nil
}

func (a *CoderAgent) applyDependencyRecoveryPlan(ctx context.Context, task *models.Task, dependencies []string, runtimeSession string, mountPath string, plan *dependencyRecoveryPlan) ([]string, string, bool, error) {
	if plan == nil {
		return dependencies, runtimeSession, false, nil
	}

	switch strings.TrimSpace(plan.Action) {
	case "remove_package":
		name := strings.TrimSpace(plan.RemovePackage)
		if name == "" {
			return dependencies, runtimeSession, false, fmt.Errorf("react remove_package plan missing package name")
		}
		next := dropDependencyByRoot(dependencies, name)
		if sameStringSlice(next, dependencies) {
			return dependencies, runtimeSession, false, nil
		}
		return next, runtimeSession, true, nil
	case "replace_package":
		from := strings.TrimSpace(plan.ReplacePackage)
		to := strings.TrimSpace(plan.WithPackage)
		if from == "" || to == "" {
			return dependencies, runtimeSession, false, fmt.Errorf("react replace_package plan missing fields")
		}
		next := replaceDependencyByRoot(dependencies, from, to)
		if sameStringSlice(next, dependencies) {
			return dependencies, runtimeSession, false, nil
		}
		return next, runtimeSession, true, nil
	case "rewrite_dependencies":
		next := filterStandardLibraryDependencies(normalizeDependenciesForPython39(uniqueDependencies(plan.NextDependencies)))
		if len(next) == 0 {
			return dependencies, runtimeSession, false, fmt.Errorf("react rewrite_dependencies plan produced empty dependency list")
		}
		if sameStringSlice(next, dependencies) {
			return dependencies, runtimeSession, false, nil
		}
		return next, runtimeSession, true, nil
	case "upgrade_python":
		targetImage := strings.TrimSpace(plan.TargetImage)
		if targetImage == "" {
			targetImage = choosePythonUpgradeImage(plan.Reason)
		}
		nextRuntimeSession, err := a.recreateSandboxForDependencies(ctx, task, runtimeSession, mountPath, targetImage)
		if err != nil {
			return dependencies, runtimeSession, false, err
		}
		return dependencies, nextRuntimeSession, true, nil
	case "abort", "":
		return dependencies, runtimeSession, false, nil
	default:
		return dependencies, runtimeSession, false, fmt.Errorf("unknown react action %q", plan.Action)
	}
}

func (a *CoderAgent) applyRuleBasedDependencyFallback(ctx context.Context, task *models.Task, dependencies []string, runtimeSession string, mountPath string, errText string) ([]string, string, bool, string, error) {
	if pipErrorIndicatesPythonVersionMismatch(errText) {
		targetImage := choosePythonUpgradeImage(errText)
		nextRuntimeSession, err := a.recreateSandboxForDependencies(ctx, task, runtimeSession, mountPath, targetImage)
		if err != nil {
			return dependencies, runtimeSession, false, "", fmt.Errorf("%s; additionally failed to create upgraded sandbox %q: %v", errText, targetImage, err)
		}
		return dependencies, nextRuntimeSession, true, "upgrade python sandbox after Requires-Python mismatch", nil
	}

	if missing, ok := pipErrorMissingRequirement(errText); ok {
		// 如果 missing 是标准库模块，说明依赖识别链路还漏了一层清洗，直接删掉再试。
		if len(filterStandardLibraryDependencies([]string{missing})) == 0 {
			nextDependencies := dropDependencyByRoot(dependencies, missing)
			if !sameStringSlice(nextDependencies, dependencies) {
				return nextDependencies, runtimeSession, true, fmt.Sprintf("remove stdlib module %q from dependency list", missing), nil
			}
		}
	}

	return dependencies, runtimeSession, false, "", nil
}

func (a *CoderAgent) recreateSandboxForDependencies(ctx context.Context, task *models.Task, runtimeSession string, mountPath string, targetImage string) (string, error) {
	if a == nil || a.Sandbox == nil {
		return "", fmt.Errorf("sandbox client is not configured")
	}
	newSandboxID, err := a.Sandbox.CreatePersistentSandbox(ctx, task.ID+"-py-upgrade", targetImage, mountPath)
	if err != nil {
		return "", err
	}
	logToContext(ctx, "[%s] Switching sandbox %s -> %s (%s) for dependency retry.", a.Name, runtimeSession, newSandboxID, targetImage)
	_ = a.Sandbox.CleanupSandbox(context.Background(), runtimeSession)
	return newSandboxID, nil
}

func pipErrorIndicatesPythonVersionMismatch(text string) bool {
	lower := strings.ToLower(text)
	if strings.Contains(lower, "requires-python") && (strings.Contains(lower, ">=3.10") || strings.Contains(lower, ">=3.11") || strings.Contains(lower, ">=3.12")) {
		return true
	}
	if strings.Contains(lower, "require a different python version") {
		return true
	}
	return false
}

func choosePythonUpgradeImage(text string) string {
	lower := strings.ToLower(text)
	// 优先满足更高版本要求；默认 3.11（兼容运行 3.9 代码，且能覆盖多数 Requires-Python>=3.10/3.11 的包）
	if strings.Contains(lower, ">=3.12") {
		return "python:3.12-bullseye"
	}
	if strings.Contains(lower, ">=3.11") {
		return "python:3.11-bullseye"
	}
	if strings.Contains(lower, ">=3.10") {
		return "python:3.10-bullseye"
	}
	return "python:3.11-bullseye"
}

func pipErrorMissingRequirement(text string) (string, bool) {
	// 例：
	// - "ERROR: No matching distribution found for shutil"
	// - "ERROR: Could not find a version that satisfies the requirement X (from versions: ...)"
	for _, prefix := range []string{
		"no matching distribution found for ",
		"could not find a version that satisfies the requirement ",
	} {
		lower := strings.ToLower(text)
		idx := strings.Index(lower, prefix)
		if idx < 0 {
			continue
		}
		raw := strings.TrimSpace(text[idx+len(prefix):])
		if raw == "" {
			continue
		}
		// 截断到空格/括号前，拿到包名 root
		raw = strings.TrimPrefix(raw, ":")
		raw = strings.TrimSpace(raw)
		for _, sep := range []string{" ", "(", ";", ","} {
			if cut := strings.Index(raw, sep); cut >= 0 {
				raw = raw[:cut]
			}
		}
		raw = strings.TrimSpace(strings.Trim(raw, `"'`))
		if raw == "" {
			continue
		}
		root := strings.Split(raw, "[")[0]
		root = strings.Split(root, "<")[0]
		root = strings.Split(root, ">")[0]
		root = strings.Split(root, "=")[0]
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		return root, true
	}
	return "", false
}

func dropDependencyByRoot(items []string, root string) []string {
	root = strings.ToLower(strings.TrimSpace(root))
	if root == "" {
		return items
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		name := strings.ToLower(strings.TrimSpace(item))
		if name == "" {
			continue
		}
		normalizedRoot := strings.Split(name, "[")[0]
		normalizedRoot = strings.Split(normalizedRoot, "<")[0]
		normalizedRoot = strings.Split(normalizedRoot, ">")[0]
		normalizedRoot = strings.Split(normalizedRoot, "=")[0]
		normalizedRoot = strings.TrimSpace(normalizedRoot)
		if normalizedRoot == root {
			continue
		}
		out = append(out, item)
	}
	return out
}

func replaceDependencyByRoot(items []string, from string, to string) []string {
	from = strings.ToLower(strings.TrimSpace(from))
	to = strings.TrimSpace(to)
	if from == "" || to == "" {
		return items
	}
	out := make([]string, 0, len(items))
	replaced := false
	for _, item := range items {
		name := strings.ToLower(strings.TrimSpace(item))
		if name == "" {
			continue
		}
		root := strings.Split(name, "[")[0]
		root = strings.Split(root, "<")[0]
		root = strings.Split(root, ">")[0]
		root = strings.Split(root, "=")[0]
		root = strings.TrimSpace(root)
		if root == from {
			out = append(out, to)
			replaced = true
			continue
		}
		out = append(out, item)
	}
	if !replaced {
		out = append(out, to)
	}
	return uniqueDependencies(out)
}

func sameStringSlice(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for idx := range left {
		if left[idx] != right[idx] {
			return false
		}
	}
	return true
}

func cleanJSONFence(raw string) string {
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	return strings.TrimSpace(cleaned)
}

func mustJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "[]"
	}
	return string(encoded)
}

var pythonStandardLibraryModules = map[string]struct{}{
	"abc": {}, "argparse": {}, "asyncio": {}, "base64": {}, "collections": {}, "contextlib": {}, "copy": {}, "csv": {}, "dataclasses": {}, "datetime": {}, "functools": {},
	"hashlib": {}, "io": {}, "itertools": {}, "json": {}, "logging": {}, "math": {}, "os": {}, "pathlib": {}, "random": {},
	"re": {}, "statistics": {}, "string": {}, "subprocess": {}, "sys": {}, "tempfile": {}, "time": {}, "typing": {}, "uuid": {}, "warnings": {},
	// 标准库：避免被误判为 PyPI 包（例如 `shutil` 不是第三方依赖，pip 永远装不上）
	"__future__": {}, "codecs": {}, "glob": {}, "inspect": {}, "shutil": {}, "tarfile": {}, "urllib": {},
}

var pythonImportPackageMap = map[string]string{
	"cv2":                              "opencv-python",
	"sklearn":                          "scikit-learn",
	"PIL":                              "pillow",
	"bs4":                              "beautifulsoup4",
	"yaml":                             "pyyaml",
	"llama_index":                      "llama-index",
	"llama_index.llms.openai":          "llama-index-llms-openai",
	"llama_index.embeddings.openai":    "llama-index-embeddings-openai",
	"llama_index.vector_stores.chroma": "llama-index-vector-stores-chroma",
	"langchain_openai":                 "langchain-openai",
	"langchain_community":              "langchain-community",
	"faiss":                            "faiss-cpu",
}

func isPythonStandardLibraryModule(name string) bool {
	root := strings.TrimSpace(name)
	if root == "" {
		return false
	}
	root = strings.Split(root, ".")[0]
	root = strings.Split(root, "<")[0]
	root = strings.Split(root, ">")[0]
	root = strings.Split(root, "=")[0]
	root = strings.Split(root, "[")[0]
	root = strings.ToLower(strings.TrimSpace(root))
	_, ok := pythonStandardLibraryModules[root]
	return ok
}

func mapPythonImportToPackage(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	if mapped, ok := pythonImportPackageMap[root]; ok {
		return mapped
	}
	if mapped, ok := pythonImportPackageMap[strings.ToLower(root)]; ok {
		return mapped
	}
	primary := strings.Split(root, ".")[0]
	if mapped, ok := pythonImportPackageMap[primary]; ok {
		return mapped
	}
	if mapped, ok := pythonImportPackageMap[strings.ToLower(primary)]; ok {
		return mapped
	}
	return primary
}

func extractMissingPythonModule(errText string) string {
	for _, marker := range []string{"No module named '", `No module named "`} {
		start := strings.Index(errText, marker)
		if start < 0 {
			continue
		}
		rest := errText[start+len(marker):]
		end := strings.IndexAny(rest, `'"`)
		if end < 0 {
			continue
		}
		return strings.TrimSpace(rest[:end])
	}

	const plainMarker = "No module named "
	start := strings.Index(errText, plainMarker)
	if start < 0 {
		return ""
	}
	rest := strings.TrimSpace(errText[start+len(plainMarker):])
	for _, token := range strings.Fields(rest) {
		token = strings.TrimSpace(strings.Trim(token, `"'.,:;()[]{}<>`))
		if token != "" {
			return token
		}
	}
	return ""
}

func cleanCodeFence(raw string) string {
	cleaned := strings.TrimSpace(raw)
	for _, prefix := range []string{"```python", "```py", "```"} {
		if strings.HasPrefix(cleaned, prefix) {
			cleaned = strings.TrimSpace(strings.TrimPrefix(cleaned, prefix))
		}
	}
	if strings.HasSuffix(cleaned, "```") {
		cleaned = strings.TrimSpace(strings.TrimSuffix(cleaned, "```"))
	}
	return cleaned
}

func shouldAttemptPythonRuntimeCodeRepair(errText string) bool {
	lower := strings.ToLower(errText)
	return strings.Contains(lower, "importerror: cannot import name") ||
		strings.Contains(lower, "attributeerror: module") ||
		strings.Contains(lower, "llama_index") ||
		strings.Contains(lower, "syntaxerror:") ||
		strings.Contains(lower, "f-string: invalid syntax") ||
		strings.Contains(lower, "invalid_api_key") ||
		strings.Contains(lower, "incorrect api key") ||
		strings.Contains(lower, "authenticationerror") ||
		strings.Contains(lower, "sk-placeholder")
}

func (a *CoderAgent) repairGeneratedPythonCodeForRuntimeError(ctx context.Context, code string, errText string) (string, bool, error) {
	if a == nil || a.ChatModel == nil || strings.TrimSpace(code) == "" || !shouldAttemptPythonRuntimeCodeRepair(errText) {
		return "", false, nil
	}

	intentType, _ := ctx.Value(coderIntentTypeContextKey).(string)
	taskType, _ := ctx.Value(coderTaskTypeContextKey).(string)
	taskName, _ := ctx.Value(coderTaskNameContextKey).(string)
	prompt := prompts.RuntimeCodeRepairUserPromptForTask(errText, code, intentType, taskType, taskName)
	msg, err := a.ChatModel.Generate(ctx, []*schema.Message{
		{Role: schema.System, Content: coderSystemPromptFromContext(ctx, a.SystemPrompt)},
		{Role: schema.User, Content: prompt},
	})
	if err != nil {
		return "", false, err
	}

	repaired := cleanCodeFence(msg.Content)
	if strings.TrimSpace(repaired) == "" || strings.TrimSpace(repaired) == strings.TrimSpace(code) {
		return "", false, nil
	}
	return repaired, true, nil
}

func (a *CoderAgent) runPythonTaskInSandbox(ctx context.Context, sandboxID string, workspacePath string, codeFilePath string, code string) (*sandbox.PythonRunResponse, error) {
	if strings.TrimSpace(workspacePath) != "" && strings.TrimSpace(codeFilePath) != "" {
		relPath, relErr := filepath.Rel(workspacePath, codeFilePath)
		if relErr == nil && !strings.HasPrefix(relPath, "..") {
			cmd := []string{"bash", "-lc", buildMountedWorkspacePythonCommand(relPath)}
			logToContext(ctx, "[%s] Executing repo entry file in mounted workspace: %s", a.Name, relPath)
			return a.Sandbox.ExecCommandStream(ctx, sandboxID, cmd, func(stream string, line string) {
				logToContext(ctx, "[%s] python %s: %s", a.Name, stream, line)
			})
		}
	}

	if err := a.validatePythonSyntaxInSandbox(ctx, sandboxID, code); err != nil {
		return nil, err
	}
	return a.Sandbox.RunPythonCodeStream(ctx, sandboxID, code, func(stream string, line string) {
		logToContext(ctx, "[%s] python %s: %s", a.Name, stream, line)
	})
}

func (a *CoderAgent) installMissingPythonModule(ctx context.Context, sandboxID string, errText string) (bool, error) {
	missingModule := extractMissingPythonModule(errText)
	if missingModule == "" || isPythonStandardLibraryModule(missingModule) {
		return false, nil
	}

	pkg := mapPythonImportToPackage(missingModule)
	if pkg == "" || isPythonStandardLibraryModule(pkg) {
		return false, nil
	}

	// 运行期兜底：依赖识别漏掉模块时，在当前 prepared_runtime 内最小补装一次。
	logToContext(ctx, "[%s] Missing module detected at runtime: %s -> %s, retrying with pip install", a.Name, missingModule, pkg)
	res, err := a.Sandbox.ExecCommandStream(ctx, sandboxID, []string{"python3", "-m", "pip", "install", "--default-timeout", defaultPipTimeoutSec, pkg}, func(stream string, line string) {
		logToContext(ctx, "[%s] pip %s: %s", a.Name, stream, line)
	})
	if err != nil {
		return false, err
	}
	if res == nil {
		return false, fmt.Errorf("pip install returned nil response while installing %s", pkg)
	}
	if res.ExitCode != 0 {
		return false, fmt.Errorf("%s", chooseNonEmpty(strings.TrimSpace(res.Stderr), strings.TrimSpace(res.Stdout), fmt.Sprintf("pip install %s exited with code %d", pkg, res.ExitCode)))
	}
	return true, nil
}

func (a *CoderAgent) executeCodeInSandbox(ctx context.Context, task *models.Task) error {
	workspacePath := strings.TrimSpace(extractTaskInputLike(task, "workspace_path"))
	codeFilePath := strings.TrimSpace(extractTaskInputLike(task, "code_file_path"))
	code := extractTaskInputLike(task, "generated_code")
	if strings.TrimSpace(code) == "" && strings.TrimSpace(codeFilePath) == "" {
		task.Status = models.StatusFailed
		task.Error = "missing generated_code/code_file_path input for sandbox execution"
		return fmt.Errorf("%s", task.Error)
	}

	runtimeSession := chooseNonEmpty(extractTaskInputLike(task, "prepared_runtime"), extractTaskInputLike(task, "runtime_session"), extractTaskInputLike(task, "runtime_env"))
	sandboxID := runtimeSession
	ownsEphemeralSandbox := false
	if strings.TrimSpace(sandboxID) == "" || (!strings.HasPrefix(sandboxID, "dk-") && !strings.HasPrefix(sandboxID, "os-")) {
		if strings.TrimSpace(runtimeSession) != "" {
			task.Status = models.StatusFailed
			task.Error = "invalid prepared_runtime artifact: expected sandbox id"
			task.Result = strings.TrimSpace(runtimeSession)
			return fmt.Errorf("%s", task.Error)
		}
		image := sandboxID
		if strings.TrimSpace(image) == "" {
			image = defaultSandboxImage
		}
		logToContext(ctx, "[%s] Running %s in sandbox image %s", a.Name, task.Name, image)
		var err error
		sandboxID, err = a.Sandbox.CreatePersistentSandbox(ctx, task.ID, image, "")
		if err != nil {
			task.Status = models.StatusFailed
			task.Error = fmt.Sprintf("failed to create execution sandbox: %v", err)
			return err
		}
		ownsEphemeralSandbox = true
	}
	if ownsEphemeralSandbox {
		defer a.Sandbox.CleanupSandbox(context.Background(), sandboxID)
	}

	executesMountedFile := strings.TrimSpace(workspacePath) != "" && strings.TrimSpace(codeFilePath) != ""
	codeRepaired := false
	moduleRecovered := false
	var (
		res *sandbox.PythonRunResponse
		err error
	)
	for {
		res, err = a.runPythonTaskInSandbox(ctx, sandboxID, workspacePath, codeFilePath, code)
		if err != nil {
			errText := err.Error()
			if !executesMountedFile && !codeRepaired {
				if repairedCode, repaired, repairErr := a.repairGeneratedPythonCodeForRuntimeError(ctx, code, errText); repairErr == nil && repaired {
					code = repairedCode
					codeRepaired = true
					logToContext(ctx, "[%s] runtime code repair applied after validation error, rerunning generated Python code", a.Name)
					continue
				} else if repairErr != nil {
					logToContext(ctx, "[%s] runtime code repair after validation error failed: %v", a.Name, repairErr)
				}
			}
			task.Status = models.StatusFailed
			task.Error = fmt.Sprintf("sandbox execution failed: %v", err)
			return err
		}
		if res == nil {
			task.Status = models.StatusFailed
			task.Error = "sandbox execution returned nil response"
			return fmt.Errorf("%s", task.Error)
		}
		if res.ExitCode == 0 {
			break
		}

		errText := strings.TrimSpace(res.Stderr)
		if errText == "" {
			errText = fmt.Sprintf("sandbox exited with code %d", res.ExitCode)
		}

		// 第一层兜底：模块缺失时优先在当前运行时最小补装，再重跑一次。
		if !moduleRecovered {
			if retried, installErr := a.installMissingPythonModule(ctx, sandboxID, errText); installErr == nil && retried {
				moduleRecovered = true
				continue
			} else if installErr != nil {
				logToContext(ctx, "[%s] runtime missing-module recovery failed: %v", a.Name, installErr)
			}
		}

		// 第二层兜底：仅对直接执行的生成代码做一次 API 兼容修复，避免改动挂载仓库源码。
		if !executesMountedFile && !codeRepaired {
			if repairedCode, repaired, repairErr := a.repairGeneratedPythonCodeForRuntimeError(ctx, code, errText); repairErr == nil && repaired {
				code = repairedCode
				codeRepaired = true
				logToContext(ctx, "[%s] runtime code repair applied, rerunning generated Python code", a.Name)
				continue
			} else if repairErr != nil {
				logToContext(ctx, "[%s] runtime code repair failed: %v", a.Name, repairErr)
			}
		}
		break
	}
	if res.ExitCode != 0 {
		task.Status = models.StatusFailed
		task.Result = strings.TrimSpace(res.Stdout)
		task.Error = strings.TrimSpace(res.Stderr)
		if task.Error == "" {
			task.Error = fmt.Sprintf("sandbox exited with code %d", res.ExitCode)
		}
		logToContext(ctx, "[%s] sandbox run exit_code=%d", a.Name, res.ExitCode)
		return fmt.Errorf("%s", task.Error)
	}

	logToContext(ctx, "[%s] sandbox run exit_code=%d", a.Name, res.ExitCode)
	if len(res.Images) > 0 {
		logToContext(ctx, "[%s] sandbox produced %d image artifact(s)", a.Name, len(res.Images))
	}
	task.Code = code
	task.Result = strings.TrimSpace(res.Stdout)
	if task.Result == "" {
		task.Result = strings.TrimSpace(res.Stderr)
	}
	if len(res.Images) > 0 {
		task.ImageBase64 = res.Images[0]
	}
	task.Status = models.StatusCompleted
	return nil
}

func (a *CoderAgent) resolveDependenciesTask(ctx context.Context, task *models.Task) error {
	workspacePath := strings.TrimSpace(extractTaskInputLike(task, "workspace_path"))
	codeFilePath := strings.TrimSpace(extractTaskInputLike(task, "code_file_path"))
	code := extractTaskInputLike(task, "generated_code")
	if strings.TrimSpace(code) == "" && strings.TrimSpace(codeFilePath) != "" {
		if raw, err := os.ReadFile(codeFilePath); err == nil {
			code = string(raw)
		}
	}
	if strings.TrimSpace(code) == "" {
		code = task.Code
	}

	dependencies := make([]string, 0, 16)
	codeDependencies := detectPythonDependencies(code)
	if isReproductionSmokeRunnerPath(codeFilePath) {
		dependencies = append(dependencies, codeDependencies...)
	} else if strings.TrimSpace(workspacePath) != "" {
		workspaceDependencies := filterWorkspaceLocalDependencies(detectWorkspacePythonDependencies(workspacePath), workspacePath)
		dependencies = append(dependencies, workspaceDependencies...)
		repoDependencies := detectRepositoryDependencies(workspacePath)
		if len(workspaceDependencies) > 0 || len(codeDependencies) > 0 {
			repoDependencies = filterDependenciesToRoots(repoDependencies, dependencyRootSet(append(append([]string{}, workspaceDependencies...), codeDependencies...)))
		}
		dependencies = append(dependencies, repoDependencies...)
	}
	dependencies = append(dependencies, codeDependencies...)
	if strings.TrimSpace(workspacePath) != "" {
		dependencies = filterWorkspaceLocalDependencies(dependencies, workspacePath)
	}
	dependencies = filterFrameworkBenchmarkDependencies(task, dependencies)
	dependencies = normalizeDependenciesForPython39(uniqueDependencies(dependencies))
	dependencies = filterFrameworkBenchmarkDependencies(task, dependencies)
	payload, err := json.Marshal(dependencies)
	if err != nil {
		task.Status = models.StatusFailed
		task.Error = fmt.Sprintf("failed to marshal dependency spec: %v", err)
		return err
	}

	task.Result = string(payload)
	task.Status = models.StatusCompleted
	if task.Metadata == nil {
		task.Metadata = map[string]any{}
	}
	task.Metadata["artifact_values"] = map[string]string{
		"dependency_spec": string(payload),
	}
	logToContext(ctx, "[%s] 识别到依赖: %s", a.Name, chooseNonEmpty(strings.Join(dependencies, ", "), "none"))
	return nil
}

func isReproductionSmokeRunnerPath(path string) bool {
	return strings.EqualFold(filepath.Base(strings.TrimSpace(path)), "scholar_repro_smoke.py")
}

func extractTaskInput(task *models.Task, key string) string {
	if task == nil || task.Inputs == nil {
		return ""
	}
	value, ok := task.Inputs[key]
	if !ok || value == nil {
		return ""
	}
	if s, ok := value.(string); ok {
		return s
	}
	return fmt.Sprint(value)
}

func extractTaskInputLike(task *models.Task, key string) string {
	if value := extractTaskInput(task, key); strings.TrimSpace(value) != "" {
		return value
	}
	if task == nil || task.Inputs == nil {
		return ""
	}
	for inputKey, value := range task.Inputs {
		if strings.HasSuffix(strings.TrimSpace(inputKey), "_"+key) || strings.EqualFold(strings.TrimSpace(inputKey), key) {
			if stringValue := extractTaskInput(task, inputKey); strings.TrimSpace(stringValue) != "" {
				return stringValue
			}
		}
		if value == nil {
			continue
		}
	}
	return ""
}

func shouldUseDeterministicSandboxPath(task *models.Task) bool {
	if task == nil {
		return false
	}
	if strings.TrimSpace(task.Type) != "" {
		return true
	}
	if strings.TrimSpace(extractTaskInputLike(task, "generated_code")) != "" {
		return true
	}
	if strings.TrimSpace(extractTaskInputLike(task, "dependency_spec")) != "" {
		return true
	}
	if strings.TrimSpace(extractTaskInputLike(task, "runtime_session")) != "" || strings.TrimSpace(extractTaskInputLike(task, "prepared_runtime")) != "" || strings.TrimSpace(extractTaskInputLike(task, "runtime_env")) != "" {
		return true
	}
	return false
}

func inferSandboxTaskKind(task *models.Task) string {
	if task == nil {
		return "execute_code"
	}

	switch strings.ToLower(strings.TrimSpace(task.Type)) {
	case "prepare_runtime", "env_setup", "prepare_environment", "setup_runtime", "test_environment":
		return "prepare_runtime"
	case "install_dependencies", "dependency_install":
		return "install_dependencies"
	case "execute_code", "baseline_run", "run_benchmark":
		return "execute_code"
	}

	context := strings.ToLower(strings.Join([]string{task.Name, task.Description}, " "))
	switch {
	case strings.Contains(context, "prepare_runtime"), strings.Contains(context, "env_setup"), strings.Contains(context, "prepare runtime"), strings.Contains(context, "prepare environment"), strings.Contains(context, "setup runtime"), strings.Contains(context, "test environment"):
		return "prepare_runtime"
	case strings.Contains(context, "install_depend"), strings.Contains(context, "pip install"):
		return "install_dependencies"
	case strings.Contains(context, "execute_code"), strings.Contains(context, "baseline_run"), strings.Contains(context, "run benchmark"), strings.Contains(context, "benchmark"), strings.Contains(context, "execute"):
		return "execute_code"
	default:
		if strings.TrimSpace(extractTaskInputLike(task, "dependency_spec")) != "" && strings.TrimSpace(extractTaskInputLike(task, "runtime_session")) != "" && strings.TrimSpace(extractTaskInputLike(task, "generated_code")) == "" {
			return "install_dependencies"
		}
		if strings.TrimSpace(extractTaskInputLike(task, "generated_code")) == "" {
			return "prepare_runtime"
		}
		return "execute_code"
	}
}

func parseDependencySpec(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	var deps []string
	if err := json.Unmarshal([]byte(raw), &deps); err == nil {
		cleaned := sanitizeDependencyTokens(deps)
		for _, item := range cleaned {
			if !isValidDependencyToken(item) {
				return nil, fmt.Errorf("invalid dependency token %q", item)
			}
		}
		return uniqueDependencies(cleaned), nil
	}

	if looksLikeRichTextDependencySpec(raw) {
		return nil, fmt.Errorf("expected structured dependency list but received report-like content")
	}

	parts := strings.Split(raw, ",")
	cleaned := sanitizeDependencyTokens(parts)
	if len(cleaned) == 0 {
		return nil, nil
	}
	for _, item := range cleaned {
		if !isValidDependencyToken(item) {
			return nil, fmt.Errorf("invalid dependency token %q", item)
		}
	}
	return uniqueDependencies(cleaned), nil
}

// sanitizeDependencyTokens normalizes dependency_spec into a pip-friendly argv list.
//
// Why:
// - LLM outputs sometimes include Markdown backticks or trailing commas.
// - Some entries may embed multiple argv parts, e.g. "--find-links https://...".
// This function keeps things simple and safe: it strips obvious formatting noise and
// splits on whitespace (no shell involved; we pass argv directly).
func sanitizeDependencyTokens(items []string) []string {
	out := make([]string, 0, len(items))
	for _, raw := range items {
		item := strings.TrimSpace(raw)
		if item == "" {
			continue
		}

		// Remove Markdown/code formatting and common punctuation noise.
		item = strings.Trim(item, `"'`)
		item = strings.ReplaceAll(item, "`", "")
		item = strings.TrimSpace(item)

		// Some outputs append commas to tokens (or even to URLs within backticks).
		item = strings.TrimRight(item, ",")
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}

		// If a single list entry contains multiple argv parts (e.g. pip options),
		// split them so pip gets correct arguments.
		for _, part := range strings.Fields(item) {
			part = strings.TrimSpace(strings.Trim(part, `"'`))
			part = strings.ReplaceAll(part, "`", "")
			part = strings.TrimRight(part, ",")
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			out = append(out, part)
		}
	}
	return out
}

func detectPythonDependencies(code string) []string {
	deps := make([]string, 0, 8)
	lines := strings.Split(code, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "import ") {
			modules := strings.Split(strings.TrimPrefix(trimmed, "import "), ",")
			for _, module := range modules {
				name := strings.Fields(strings.TrimSpace(module))
				if len(name) == 0 {
					continue
				}
				importPath := name[0]
				if isPythonStandardLibraryModule(importPath) {
					continue
				}
				deps = append(deps, mapPythonImportToPackage(importPath))
			}
		}
		if strings.HasPrefix(trimmed, "from ") {
			parts := strings.Fields(trimmed)
			if len(parts) < 2 {
				continue
			}
			importPath := parts[1]
			if isPythonStandardLibraryModule(importPath) {
				continue
			}
			deps = append(deps, mapPythonImportToPackage(importPath))
		}
	}

	return filterStandardLibraryDependencies(uniqueDependencies(deps))
}

func uniqueDependencies(items []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(items))
	for _, item := range items {
		name := strings.TrimSpace(strings.Trim(item, `"'`))
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

func dependencyRootName(dependency string) string {
	root := strings.ToLower(strings.TrimSpace(dependency))
	root = strings.Split(root, "[")[0]
	root = strings.Split(root, "<")[0]
	root = strings.Split(root, ">")[0]
	root = strings.Split(root, "=")[0]
	return strings.TrimSpace(root)
}

func canonicalDependencyRoot(dependency string) string {
	root := strings.ReplaceAll(dependencyRootName(dependency), "_", "-")
	switch root {
	case "pytorch":
		return "torch"
	case "msgpack-python":
		return "msgpack"
	default:
		return root
	}
}

func dependencyRootSet(dependencies []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, dependency := range dependencies {
		root := canonicalDependencyRoot(dependency)
		if root == "" {
			continue
		}
		out[root] = struct{}{}
	}
	return out
}

func filterDependenciesToRoots(dependencies []string, roots map[string]struct{}) []string {
	if len(dependencies) == 0 || len(roots) == 0 {
		return dependencies
	}
	out := make([]string, 0, len(dependencies))
	for _, dependency := range dependencies {
		if _, ok := roots[canonicalDependencyRoot(dependency)]; ok {
			out = append(out, dependency)
		}
	}
	return out
}

func filterWorkspaceLocalDependencies(dependencies []string, workspacePath string) []string {
	workspacePath = strings.TrimSpace(workspacePath)
	if workspacePath == "" || len(dependencies) == 0 {
		return dependencies
	}
	out := make([]string, 0, len(dependencies))
	for _, dependency := range dependencies {
		root := strings.ReplaceAll(dependencyRootName(dependency), "-", "_")
		if root != "" && workspaceHasPythonModule(workspacePath, root) {
			continue
		}
		out = append(out, dependency)
	}
	return out
}

func workspaceHasPythonModule(workspacePath, module string) bool {
	found := false
	_ = filepath.WalkDir(workspacePath, func(path string, d os.DirEntry, err error) error {
		if err != nil || found {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			switch name {
			case ".git", ".github", "__pycache__", ".venv", "venv", "node_modules", "dist", "build":
				return filepath.SkipDir
			}
			if name == module {
				found = true
				return filepath.SkipDir
			}
			return nil
		}
		if name == module+".py" {
			found = true
		}
		return nil
	})
	return found
}

func detectWorkspacePythonDependencies(workspacePath string) []string {
	deps := make([]string, 0, 16)
	_ = filepath.WalkDir(workspacePath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			switch name {
			case ".git", ".github", "__pycache__", ".venv", "venv", "node_modules", "dist", "build", "docs":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(name), ".py") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		deps = append(deps, detectPythonDependencies(string(raw))...)
		return nil
	})
	return uniqueDependencies(deps)
}

func normalizeDependenciesForPython39(items []string) []string {
	normalized := make([]string, 0, len(items)+1)
	seen := map[string]struct{}{}
	appendOnce := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}

	// Some packages have optional submodules split into extra distributions.
	// Example: newer langchain code paths may import langchain_community at runtime.
	// We add a minimal compatibility shim: if langchain is requested, ensure
	// langchain-community is present to avoid ModuleNotFoundError during execution.
	hasLangchain := false
	hasLangchainCommunity := false
	for _, item := range items {
		name := strings.ToLower(strings.TrimSpace(item))
		if name == "" {
			continue
		}
		// Normalize to root package name for detection (strip version/extras).
		root := strings.Split(name, "[")[0]
		root = strings.Split(root, "<")[0]
		root = strings.Split(root, ">")[0]
		root = strings.Split(root, "=")[0]
		root = strings.TrimSpace(root)
		switch root {
		case "langchain":
			hasLangchain = true
		case "langchain-community":
			hasLangchainCommunity = true
		}
	}

	for _, item := range filterStandardLibraryDependencies(items) {
		name := strings.TrimSpace(item)
		lowerName := strings.ToLower(name)
		root := canonicalDependencyRoot(lowerName)
		switch {
		case root == "python" || root == "python3":
			continue
		case lowerName == "oscar==2.2.1":
			// 最小纠错：PyPI 上 `oscar` 没有 2.2.1；结合实际安装日志，
			// `django-oscar` 可用到 2.2，因此降到已存在的稳定版本。
			appendOnce("django-oscar==2.2")
		case root == "torch" && strings.HasPrefix(lowerName, "pytorch"):
			appendOnce("torch")
		case root == "msgpack" && strings.HasPrefix(lowerName, "msgpack-python"):
			appendOnce("msgpack")
		case lowerName == "tensorflow==1.14.0" || lowerName == "tensorflow==1.15.0":
			continue
		case lowerName == "llama-index":
			appendOnce("llama-index<0.12")
			appendOnce("pydantic<2.10")
		default:
			appendOnce(name)
		}
	}

	if hasLangchain && !hasLangchainCommunity {
		appendOnce("langchain-community")
	}
	return normalized
}

func filterStandardLibraryDependencies(items []string) []string {
	filtered := make([]string, 0, len(items))
	for _, item := range items {
		name := strings.TrimSpace(strings.Trim(item, `"'`))
		if isPythonStandardLibraryModule(name) {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func (a *CoderAgent) validatePythonSyntaxInSandbox(ctx context.Context, sandboxID string, code string) error {
	encoded, err := json.Marshal(code)
	if err != nil {
		return fmt.Errorf("marshal python source failed: %w", err)
	}

	validator := fmt.Sprintf(
		"import ast\nsource = %s\nast.parse(source)\nprint('syntax_ok')\n",
		string(encoded),
	)
	res, err := a.Sandbox.RunPythonCodeStream(ctx, sandboxID, validator, func(stream string, line string) {
		logToContext(ctx, "[%s] syntax %s: %s", a.Name, stream, line)
	})
	if err != nil {
		return fmt.Errorf("syntax validation sandbox error: %w", err)
	}
	if res == nil {
		return fmt.Errorf("syntax validation returned nil response")
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("%s", chooseNonEmpty(strings.TrimSpace(res.Stderr), strings.TrimSpace(res.Stdout), fmt.Sprintf("syntax validation exited with code %d", res.ExitCode)))
	}
	return nil
}

func chooseNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func buildArtifactValueMap(task *models.Task, base map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range base {
		if strings.TrimSpace(value) != "" {
			out[key] = value
		}
	}
	if task == nil {
		return out
	}
	for _, outputKey := range task.OutputArtifacts {
		lowerKey := strings.ToLower(strings.TrimSpace(outputKey))
		switch {
		case strings.Contains(lowerKey, "runtime_session"):
			out[outputKey] = chooseNonEmpty(base["runtime_session"], base["prepared_runtime"])
		case strings.Contains(lowerKey, "prepared_runtime"):
			out[outputKey] = chooseNonEmpty(base["prepared_runtime"], base["runtime_session"])
		case strings.Contains(lowerKey, "dependency_install_report"):
			out[outputKey] = base["dependency_install_report"]
		case strings.Contains(lowerKey, "dependency_spec"):
			out[outputKey] = base["dependency_spec"]
		}
	}
	return out
}

func detectRepositoryDependencies(workspacePath string) []string {
	candidates := []string{
		filepath.Join(workspacePath, "requirements.txt"),
		filepath.Join(workspacePath, "environment.yml"),
		filepath.Join(workspacePath, "environment.yaml"),
		filepath.Join(workspacePath, "setup.py"),
		filepath.Join(workspacePath, "pyproject.toml"),
	}

	deps := make([]string, 0, 16)
	for _, candidate := range candidates {
		raw, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		text := string(raw)
		switch filepath.Base(candidate) {
		case "requirements.txt":
			for _, line := range strings.Split(text, "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				deps = append(deps, line)
			}
		default:
			deps = append(deps, detectPythonDependencies(text)...)
		}
	}
	return uniqueDependencies(deps)
}

func shellEscape(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func buildMountedWorkspacePythonCommand(relPath string) string {
	entryDir := filepath.ToSlash(filepath.Dir(relPath))
	if entryDir == "." || entryDir == "/" {
		entryDir = ""
	}

	workspaceEntryDir := "/workspace"
	if entryDir != "" {
		workspaceEntryDir = "/workspace/" + strings.TrimPrefix(entryDir, "/")
	}

	// 兼容部分旧仓库：
	// 1. 目录里有 Python 源码但没有 __init__.py
	// 2. 与 site-packages 中的同名包（如 utils）发生导入冲突
	// 这里在执行前最小化补齐包标记，并显式把源码目录放到 PYTHONPATH 最前面。
	return fmt.Sprintf(
		"cd /workspace && "+
			"ENTRY_DIR=%s && "+
			"find \"$ENTRY_DIR\" -type d | while read -r d; do "+
			"if find \"$d\" -maxdepth 1 -type f -name '*.py' | grep -q . && [ ! -f \"$d/__init__.py\" ]; then : > \"$d/__init__.py\"; fi; "+
			"done && "+
			"PYTHONPATH=%s:/workspace:${PYTHONPATH:-} python3 %s",
		shellEscape(workspaceEntryDir),
		shellEscape(workspaceEntryDir),
		shellEscape(filepath.ToSlash(relPath)),
	)
}

func looksLikeRichTextDependencySpec(raw string) bool {
	normalized := strings.ToLower(raw)
	markers := []string{
		"\n#",
		"\n##",
		"```",
		"|",
		"任务目标",
		"结论",
		"建议",
		"report",
		"summary",
		"分析",
	}
	for _, marker := range markers {
		if strings.Contains(normalized, strings.ToLower(marker)) {
			return true
		}
	}
	return false
}

func isValidDependencyToken(value string) bool {
	if value == "" {
		return false
	}
	// pip argv is passed without a shell, but we still forbid whitespace and quoting chars
	// to avoid accidental token merging or Markdown artifacts.
	if strings.ContainsAny(value, " \t\r\n`\"'") {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		// Allow common requirement spec chars and URL chars used by pip options.
		case strings.ContainsRune("._-<>!=[]:+/@%?&=#~", r):
		case r == '+':
		default:
			return false
		}
	}
	return true
}

func (a *CoderAgent) mockLLMGenerateCode(prompt string) string {
	return `
import sys
import math

def main():
    print("环境初始化中 (Eino 驱动)...")
    print("正在运行为该任务生成的代码...")
    result = math.sqrt(144)
    print(f"计算结果: {result}")
    
if __name__ == "__main__":
    main()
`
}
