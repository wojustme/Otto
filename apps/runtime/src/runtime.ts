import {
  PROTOCOL_VERSION,
  type RuntimeEvent,
  type RuntimeRequest,
} from "@otto/protocol";

export const RUNTIME_VERSION = "0.1.0";

export function createReadyEvent(): RuntimeEvent {
  return {
    protocolVersion: PROTOCOL_VERSION,
    type: "runtime.ready",
    runtimeVersion: RUNTIME_VERSION,
  };
}

export function handleRuntimeRequest(
  request: RuntimeRequest,
  now: () => Date = () => new Date(),
): RuntimeEvent {
  return {
    protocolVersion: PROTOCOL_VERSION,
    requestId: request.requestId,
    type: "runtime.pong",
    sentAt: request.sentAt,
    receivedAt: now().toISOString(),
  };
}

export function createErrorEvent(error: unknown): RuntimeEvent {
  return {
    protocolVersion: PROTOCOL_VERSION,
    type: "runtime.error",
    code: "invalid_request",
    message: error instanceof Error ? error.message : "Unknown runtime error",
  };
}
