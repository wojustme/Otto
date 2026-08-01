package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/wojustme/otto/internal/agent"
	"github.com/wojustme/otto/internal/agentd"
	"github.com/wojustme/otto/internal/fake"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	engine, err := agent.NewEngine(agent.EngineOptions{
		Model: fake.Model{}, Tools: []agent.Tool{fake.EchoTool{}}, Policy: fake.RequireApprovalPolicy{},
	})
	if err != nil {
		logger.Error("create agent engine", "error", err)
		os.Exit(1)
	}
	server, err := agentd.New(os.Stdin, os.Stdout, engine, logger)
	if err != nil {
		logger.Error("create agent runtime", "error", err)
		os.Exit(1)
	}
	if err := server.Serve(context.Background()); err != nil {
		logger.Error("agent runtime stopped", "error", err)
		os.Exit(1)
	}
}
