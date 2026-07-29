export const PROTOCOL_VERSION = 1 as const;

export interface RuntimePingRequest {
  protocolVersion: typeof PROTOCOL_VERSION;
  requestId: string;
  type: "runtime.ping";
  sentAt: string;
}

export type RuntimeRequest = RuntimePingRequest;

export interface RuntimeReadyEvent {
  protocolVersion: typeof PROTOCOL_VERSION;
  type: "runtime.ready";
  runtimeVersion: string;
}

export interface RuntimePongEvent {
  protocolVersion: typeof PROTOCOL_VERSION;
  requestId: string;
  type: "runtime.pong";
  sentAt: string;
  receivedAt: string;
}

export interface RuntimeErrorEvent {
  protocolVersion: typeof PROTOCOL_VERSION;
  requestId?: string;
  type: "runtime.error";
  code: string;
  message: string;
}

export type RuntimeEvent =
  | RuntimeReadyEvent
  | RuntimePongEvent
  | RuntimeErrorEvent;

export function encodeProtocolMessage(message: RuntimeRequest | RuntimeEvent): string {
  return `${JSON.stringify(message)}\n`;
}

export function parseRuntimeRequest(line: string): RuntimeRequest {
  const value: unknown = JSON.parse(line);

  if (!isRecord(value)) {
    throw new Error("Runtime request must be a JSON object");
  }

  if (value.protocolVersion !== PROTOCOL_VERSION) {
    throw new Error(`Unsupported protocol version: ${String(value.protocolVersion)}`);
  }

  if (value.type !== "runtime.ping") {
    throw new Error(`Unsupported runtime request: ${String(value.type)}`);
  }

  if (typeof value.requestId !== "string" || typeof value.sentAt !== "string") {
    throw new Error("Invalid runtime.ping request");
  }

  return {
    protocolVersion: PROTOCOL_VERSION,
    requestId: value.requestId,
    type: "runtime.ping",
    sentAt: value.sentAt,
  };
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
