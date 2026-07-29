export type MessageRole = "system" | "user" | "assistant" | "tool";

export interface AgentMessage {
  id: string;
  role: MessageRole;
  content: string;
  createdAt: string;
}

export interface ToolDefinition {
  name: string;
  description: string;
  parameters: Record<string, unknown>;
}

export interface ModelRequest {
  messages: readonly AgentMessage[];
  tools: readonly ToolDefinition[];
}

export type ModelEvent =
  | { type: "text.delta"; delta: string }
  | { type: "tool.completed"; callId: string; name: string; arguments: unknown }
  | { type: "usage"; inputTokens: number; outputTokens: number }
  | { type: "response.completed"; stopReason: string };

export interface ModelAdapter {
  stream(
    request: ModelRequest,
    signal: AbortSignal,
  ): AsyncIterable<ModelEvent>;
}
