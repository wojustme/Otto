import { useEffect, useState } from "react";
import { invoke } from "@tauri-apps/api/core";
import { PROTOCOL_VERSION } from "@otto/protocol";
import "./App.css";

interface EnvironmentInfo {
  desktopShell: string;
  agentRuntime: string;
  userInterface: string;
}

function App() {
  const [environment, setEnvironment] = useState<EnvironmentInfo | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    invoke<EnvironmentInfo>("environment_info")
      .then(setEnvironment)
      .catch((cause: unknown) => {
        setError(cause instanceof Error ? cause.message : String(cause));
      });
  }, []);

  return (
    <main className="app-shell">
      <section className="hero">
        <span className="eyebrow">OTTO DEVELOPMENT ENVIRONMENT</span>
        <h1>Desktop agent foundations are ready.</h1>
        <p>
          The shell, user interface, and agent runtime have explicit process
          boundaries. The next milestone is a deterministic fake-model loop.
        </p>
      </section>

      <section className="status-grid" aria-label="Architecture status">
        <article>
          <span>Desktop shell</span>
          <strong>{environment?.desktopShell ?? "Checking…"}</strong>
        </article>
        <article>
          <span>User interface</span>
          <strong>{environment?.userInterface ?? "Checking…"}</strong>
        </article>
        <article>
          <span>Agent runtime</span>
          <strong>{environment?.agentRuntime ?? "Checking…"}</strong>
        </article>
        <article>
          <span>IPC protocol</span>
          <strong>Version {PROTOCOL_VERSION}</strong>
        </article>
      </section>

      <section className="next-step">
        <span>Next</span>
        <p>
          Fake model → tool request → policy check → tool result → final answer
        </p>
      </section>

      {error ? <p className="error">Tauri IPC check failed: {error}</p> : null}
    </main>
  );
}

export default App;
