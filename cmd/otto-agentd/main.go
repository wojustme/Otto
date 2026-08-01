package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/wojustme/otto/internal/agent"
	"github.com/wojustme/otto/internal/agentd"
	"github.com/wojustme/otto/internal/fake"
)

// main 组装独立 Agent Runtime，并通过标准输入输出为桌面端提供服务。
func main() {
	// 日志必须写入 stderr，因为 stdout 是桌面进程消费的 NDJSON 协议通道。
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	// 确定性的 fake 适配器用于完整演练“模型 -> 审批 -> 工具 -> 结果”链路。
	// 后续接入真实 Provider 时无需修改 Engine。
	engine, err := agent.NewEngine(agent.EngineOptions{
		Model: fake.Model{}, Tools: []agent.Tool{fake.EchoTool{}}, Policy: fake.RequireApprovalPolicy{},
	})
	// 引擎依赖配置无效时 Runtime 无法提供任何 Agent 能力，直接退出。
	if err != nil {
		logger.Error("create agent engine", "error", err)
		os.Exit(1)
	}
	server, err := agentd.New(os.Stdin, os.Stdout, engine, logger)
	// 协议流或引擎缺失时无法创建 Server，直接退出并写入 stderr。
	if err != nil {
		logger.Error("create agent runtime", "error", err)
		os.Exit(1)
	}
	// Serve 返回错误表示协议流中断或发生致命传输错误。
	if err := server.Serve(context.Background()); err != nil {
		logger.Error("agent runtime stopped", "error", err)
		os.Exit(1)
	}
}
