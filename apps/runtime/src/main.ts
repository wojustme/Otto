import { createInterface } from "node:readline";
import { stdin, stdout, stderr } from "node:process";
import {
  encodeProtocolMessage,
  parseRuntimeRequest,
  type RuntimeEvent,
} from "@otto/protocol";
import {
  createErrorEvent,
  createReadyEvent,
  handleRuntimeRequest,
} from "./runtime.js";

function emit(event: RuntimeEvent): void {
  stdout.write(encodeProtocolMessage(event));
}

function log(message: string): void {
  stderr.write(`[otto-runtime] ${message}\n`);
}

const input = createInterface({
  input: stdin,
  crlfDelay: Number.POSITIVE_INFINITY,
});

emit(createReadyEvent());
log("ready");

input.on("line", (line) => {
  if (line.trim().length === 0) {
    return;
  }

  try {
    emit(handleRuntimeRequest(parseRuntimeRequest(line)));
  } catch (error) {
    emit(createErrorEvent(error));
  }
});

input.on("close", () => {
  log("stdin closed");
});

process.on("SIGTERM", () => {
  log("received SIGTERM");
  input.close();
  process.exitCode = 0;
});
