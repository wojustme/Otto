import { describe, expect, it } from "vitest";
import { PROTOCOL_VERSION } from "@otto/protocol";
import { createReadyEvent, handleRuntimeRequest } from "./runtime.js";

describe("runtime protocol", () => {
  it("announces the supported protocol version", () => {
    expect(createReadyEvent()).toMatchObject({
      protocolVersion: PROTOCOL_VERSION,
      type: "runtime.ready",
    });
  });

  it("responds to a ping deterministically", () => {
    const receivedAt = new Date("2026-07-29T12:00:01.000Z");

    expect(
      handleRuntimeRequest(
        {
          protocolVersion: PROTOCOL_VERSION,
          requestId: "request-1",
          type: "runtime.ping",
          sentAt: "2026-07-29T12:00:00.000Z",
        },
        () => receivedAt,
      ),
    ).toEqual({
      protocolVersion: PROTOCOL_VERSION,
      requestId: "request-1",
      type: "runtime.pong",
      sentAt: "2026-07-29T12:00:00.000Z",
      receivedAt: "2026-07-29T12:00:01.000Z",
    });
  });
});
