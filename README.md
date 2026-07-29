# Otto

Otto is a desktop AI agent built to make agent-runtime behavior observable and
easy to study.

## Architecture

- **Tauri 2** owns the desktop lifecycle and native integration.
- **React + TypeScript** renders the user interface.
- **Node.js + TypeScript** runs the agent loop, model adapters, tools, policy,
  persistence, and traces as a sidecar process.
- **NDJSON over stdio** is the versioned process protocol.

```text
React UI <-> Tauri 2 <-> Node.js Agent Runtime
                              |-- Model adapters
                              |-- Tool registry
                              |-- Policy and approvals
                              `-- Storage and traces
```

## Prerequisites

- Node.js 24
- pnpm 11
- Rust 1.97.1
- Xcode Command Line Tools on macOS

## Commands

```bash
pnpm install
pnpm dev:runtime
pnpm dev:ui
pnpm dev
pnpm build
pnpm test
pnpm typecheck
pnpm check:rust
```

The runtime currently implements a minimal versioned `ping` protocol. The next
milestone is a deterministic fake-model agent loop before adding any real model
provider.
