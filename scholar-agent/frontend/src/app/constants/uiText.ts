export const uiText = {
  appWelcome: '你好，我是 ScholarAgent 科研助手。你可以直接让我做规划、复现实验、代码执行、论文总结或框架对比。',
  planGenerated: '我已经生成执行计划，右侧会按依赖顺序展示任务节点。',
  backendError: '后端请求失败，请确认 Go 服务正在 :8080 端口运行。',
  graphTitle: '多智能体执行流程',
  graphHint: '等待生成执行计划',
  runAll: '运行计划',
  planStartMessage: '正在启动整张拓扑图执行，并订阅计划级状态流。',
  planStartFailedMessage: '启动整图执行失败，请确认后端计划接口已经启动。',
  noPlanMessage: '当前前端已经切换到 plan_graph 主流程，无法回退旧任务列表执行链路。请重新生成计划后再执行。',
} as const;
