import { CSSProperties, FormEvent, KeyboardEvent, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Events } from '@wailsio/runtime'
import { RuntimeService } from '../bindings/github.com/wojustme/otto'

type RuntimeInfo = {
  connected: boolean
  runtimeVersion: string
  protocolVersion: number
}

type RuntimeEvent = {
  protocolVersion: number
  type: string
  requestId?: string
  runId?: string
  sequence?: number
  occurredAt?: string
  data?: Record<string, unknown>
}

type Message = {
  id: string
  role: 'assistant' | 'user' | 'notice'
  text: string
  runId?: string
  streaming?: boolean
}

type TraceItem = {
  id: string
  type: string
  title: string
  detail: string
  state: 'active' | 'done' | 'waiting' | 'error'
  time: string
}

type Approval = {
  runId: string
  approvalId: string
  tool: string
  summary: string
  arguments: unknown
}

const initialMessages: Message[] = [{
  id: 'welcome',
  role: 'assistant',
  text: '我是 Otto。桌面端已经连接本地 Go Agent Runtime，可以从一条真实的审批链路开始。',
}]

const fontScaleStorageKey = 'otto.interface.font-scale'
const defaultFontScale = 120

function initialFontScale(): number {
  try {
    const stored = Number.parseInt(window.localStorage.getItem(fontScaleStorageKey) ?? '', 10)
    return Number.isFinite(stored) && stored >= 90 && stored <= 140 ? stored : defaultFontScale
  } catch {
    return defaultFontScale
  }
}

function valueAsRecord(value: unknown): Record<string, unknown> {
  return typeof value === 'object' && value !== null ? value as Record<string, unknown> : {}
}

function valueAsString(value: unknown, fallback = ''): string {
  return typeof value === 'string' ? value : fallback
}

function eventTime(value?: string): string {
  if (!value) return new Date().toLocaleTimeString('zh-CN', { hour12: false })
  return new Date(value).toLocaleTimeString('zh-CN', { hour12: false })
}

function traceCopy(event: RuntimeEvent): Pick<TraceItem, 'title' | 'detail' | 'state'> {
  const data = valueAsRecord(event.data)
  const call = valueAsRecord(data.call)
  const tool = valueAsString(call.name, valueAsString(data.name, 'tool'))
  switch (event.type) {
    case 'run.started':
      return { title: 'Run started', detail: 'Agent 已接收新任务', state: 'active' }
    case 'model.text.delta':
      return { title: 'Model response', detail: '收到模型输出', state: 'active' }
    case 'model.response.completed':
      return { title: 'Model completed', detail: valueAsString(data.stopReason, 'completed'), state: 'done' }
    case 'tool.requested':
      return { title: `${tool} requested`, detail: '模型请求调用本地工具', state: 'active' }
    case 'approval.requested':
      return { title: 'Approval required', detail: `${tool} 正在等待你的确认`, state: 'waiting' }
    case 'approval.resolved':
      return { title: 'Approval resolved', detail: data.approved ? '已允许执行' : '已拒绝执行', state: 'done' }
    case 'tool.started':
      return { title: `${tool} started`, detail: '工具正在本地运行', state: 'active' }
    case 'tool.completed':
      return { title: `${tool} completed`, detail: data.isError ? '工具返回错误' : '工具执行成功', state: data.isError ? 'error' : 'done' }
    case 'tool.denied':
      return { title: `${tool} denied`, detail: valueAsString(data.reason, '已拒绝'), state: 'error' }
    case 'run.completed':
      return { title: 'Run completed', detail: '本轮任务已完成', state: 'done' }
    case 'run.cancelled':
      return { title: 'Run cancelled', detail: '任务已取消', state: 'error' }
    case 'run.failed':
      return { title: 'Run failed', detail: valueAsString(data.message, 'Agent 执行失败'), state: 'error' }
    case 'runtime.error':
      return { title: 'Runtime error', detail: valueAsString(data.message, 'Runtime 请求失败'), state: 'error' }
    default:
      return { title: event.type, detail: 'Runtime event', state: 'done' }
  }
}

function App() {
  const [runtime, setRuntime] = useState<RuntimeInfo>({ connected: false, runtimeVersion: 'starting', protocolVersion: 1 })
  const [messages, setMessages] = useState<Message[]>(initialMessages)
  const [trace, setTrace] = useState<TraceItem[]>([])
  const [approval, setApproval] = useState<Approval | null>(null)
  const [activeRun, setActiveRun] = useState<string | null>(null)
  const [draft, setDraft] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [resolvingApproval, setResolvingApproval] = useState(false)
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [fontScale, setFontScale] = useState(initialFontScale)
  const conversationRef = useRef<HTMLDivElement>(null)

  const handleRuntimeEvent = useCallback((event: RuntimeEvent) => {
    const data = valueAsRecord(event.data)
    const runId = event.runId ?? ''

    if (event.type === 'runtime.ready') {
      setRuntime(current => ({ ...current, connected: true, runtimeVersion: valueAsString(data.runtimeVersion, 'unknown') }))
      return
    }

    const copy = traceCopy(event)
    setTrace(current => [{
      id: `${event.requestId ?? 'event'}-${event.sequence ?? Date.now()}-${event.type}`,
      type: event.type,
      time: eventTime(event.occurredAt),
      ...copy,
    }, ...current].slice(0, 30))

    switch (event.type) {
      case 'run.started':
        setActiveRun(runId)
        setMessages(current => [...current, {
          id: `${runId}-user`, role: 'user', runId, text: valueAsString(data.message),
        }])
        break
      case 'model.text.delta': {
        const delta = valueAsString(data.delta)
        setMessages(current => {
          const last = current[current.length - 1]
          if (last?.role === 'assistant' && last.runId === runId && last.streaming) {
            return [...current.slice(0, -1), { ...last, text: last.text + delta }]
          }
          return [...current, { id: `${runId}-assistant-${event.sequence}`, role: 'assistant', runId, text: delta, streaming: true }]
        })
        break
      }
      case 'model.response.completed':
        setMessages(current => current.map(message => message.runId === runId ? { ...message, streaming: false } : message))
        break
      case 'approval.requested': {
        const call = valueAsRecord(data.call)
        setApproval({
          runId,
          approvalId: valueAsString(data.approvalId),
          tool: valueAsString(call.name, 'tool'),
          summary: valueAsString(data.summary, 'Agent 请求执行本地工具'),
          arguments: call.arguments ?? {},
        })
        break
      }
      case 'approval.resolved':
        setApproval(null)
        setResolvingApproval(false)
        break
      case 'run.completed':
      case 'run.cancelled':
        setActiveRun(null)
        setSubmitting(false)
        break
      case 'run.failed':
      case 'runtime.error':
        setActiveRun(null)
        setSubmitting(false)
        setResolvingApproval(false)
        setMessages(current => [...current, {
          id: `error-${Date.now()}`,
          role: 'notice',
          text: valueAsString(data.message, 'Agent Runtime 返回了一个错误。'),
        }])
        break
    }
  }, [])

  useEffect(() => {
    RuntimeService.RuntimeInfo()
      .then(info => setRuntime({
        connected: info.connected,
        runtimeVersion: info.runtimeVersion || 'unknown',
        protocolVersion: info.protocolVersion,
      }))
      .catch(error => setMessages(current => [...current, {
        id: 'runtime-start-error', role: 'notice', text: `Runtime 启动失败：${String(error)}`,
      }]))

    return Events.On('otto:runtime-event', event => handleRuntimeEvent(event.data as RuntimeEvent))
  }, [handleRuntimeEvent])

  useEffect(() => {
    conversationRef.current?.scrollTo({ top: conversationRef.current.scrollHeight, behavior: 'smooth' })
  }, [messages, approval])

  useEffect(() => {
    try {
      window.localStorage.setItem(fontScaleStorageKey, String(fontScale))
    } catch {
      // The selected size still applies for the current session when storage is unavailable.
    }
  }, [fontScale])

  useEffect(() => {
    if (!settingsOpen) return
    const closeOnEscape = (event: globalThis.KeyboardEvent) => {
      if (event.key === 'Escape') setSettingsOpen(false)
    }
    window.addEventListener('keydown', closeOnEscape)
    return () => window.removeEventListener('keydown', closeOnEscape)
  }, [settingsOpen])

  const submit = async (event?: FormEvent) => {
    event?.preventDefault()
    const message = draft.trim()
    if (!message || submitting || activeRun || !runtime.connected) return
    setDraft('')
    setSubmitting(true)
    try {
      const runId = await RuntimeService.StartRun(message)
      setActiveRun(runId)
    } catch (error) {
      setSubmitting(false)
      setDraft(message)
      setMessages(current => [...current, { id: `send-error-${Date.now()}`, role: 'notice', text: `发送失败：${String(error)}` }])
    }
  }

  const onComposerKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === 'Enter' && !event.shiftKey) {
      event.preventDefault()
      void submit()
    }
  }

  const resolveApproval = async (approved: boolean) => {
    if (!approval || resolvingApproval) return
    setResolvingApproval(true)
    try {
      await RuntimeService.RespondToApproval(approval.runId, approval.approvalId, approved)
    } catch (error) {
      setResolvingApproval(false)
      setMessages(current => [...current, { id: `approval-error-${Date.now()}`, role: 'notice', text: `审批提交失败：${String(error)}` }])
    }
  }

  const cancelRun = async () => {
    if (!activeRun) return
    try {
      await RuntimeService.CancelRun(activeRun)
    } catch (error) {
      setMessages(current => [...current, { id: `cancel-error-${Date.now()}`, role: 'notice', text: `取消失败：${String(error)}` }])
    }
  }

  const statusLabel = runtime.connected ? 'Runtime online' : 'Runtime offline'
  const runLabel = approval ? 'Waiting approval' : activeRun ? 'Agent running' : 'Ready'
  const formattedArguments = useMemo(() => approval ? JSON.stringify(approval.arguments, null, 2) : '', [approval])
  const interfaceStyle = { '--font-scale': fontScale / 100 } as CSSProperties

  return (
    <main className="shell" style={interfaceStyle}>
      <header className="titlebar">
        <div className="traffic-space" aria-hidden="true" />
        <div className="brand-lockup">
          <span className="brand-mark">O</span>
          <span className="brand-name">Otto</span>
          <span className="brand-tag">desktop node</span>
        </div>
        <button
          className={`settings-trigger ${settingsOpen ? 'active' : ''}`}
          type="button"
          aria-label="Open settings"
          title="Settings"
          onClick={() => setSettingsOpen(current => !current)}
        >
          <svg aria-hidden="true" viewBox="0 0 24 24">
            <path d="M4 7h10M18 7h2M4 17h2M10 17h10M14 4v6M7 14v6" />
          </svg>
        </button>
        <div className="runtime-pill" title={`Agent Runtime ${runtime.runtimeVersion}, Protocol v${runtime.protocolVersion}`}>
          <span className={`status-dot ${runtime.connected ? 'online' : ''}`} />
          {statusLabel}
          <span className="runtime-version">v{runtime.runtimeVersion}</span>
        </div>
      </header>

      {settingsOpen && (
        <div className="settings-backdrop" role="presentation" onMouseDown={() => setSettingsOpen(false)}>
          <section className="settings-panel" role="dialog" aria-modal="true" aria-labelledby="settings-title" onMouseDown={event => event.stopPropagation()}>
            <header className="settings-header">
              <div>
                <p className="eyebrow">PREFERENCES</p>
                <h2 id="settings-title">Settings</h2>
              </div>
              <button className="settings-close" type="button" aria-label="Close settings" onClick={() => setSettingsOpen(false)}>×</button>
            </header>

            <div className="settings-section">
              <div className="setting-label">
                <span>
                  <strong>Interface text size</strong>
                  <small>Adjust text across conversations, navigation and run traces.</small>
                </span>
                <output>{fontScale}%</output>
              </div>
              <input
                className="font-scale-slider"
                type="range"
                min="90"
                max="140"
                step="5"
                value={fontScale}
                aria-label="Interface text size"
                onChange={event => setFontScale(Number(event.target.value))}
              />
              <div className="slider-scale" aria-hidden="true"><span>Smaller</span><span>Larger</span></div>
              <div className="font-presets">
                {[
                  { label: 'Standard', value: 100 },
                  { label: 'Comfortable', value: 120 },
                  { label: 'Large', value: 135 },
                ].map(preset => (
                  <button
                    className={fontScale === preset.value ? 'active' : ''}
                    type="button"
                    key={preset.value}
                    onClick={() => setFontScale(preset.value)}
                  >
                    {preset.label}
                  </button>
                ))}
              </div>
              <div className="font-preview">
                <span className="message-avatar">O</span>
                <span><strong>Otto</strong><small>这是一段界面字号预览。</small></span>
              </div>
            </div>
          </section>
        </div>
      )}

      <div className="workspace">
        <aside className="sidebar">
          <button className="new-session" type="button" disabled={Boolean(activeRun)} onClick={() => setMessages(initialMessages)}>
            <span>＋</span> New session
          </button>

          <div className="sidebar-label">Today</div>
          <button className="session-item active" type="button">
            <span className="session-icon">⌁</span>
            <span>
              <strong>Local workspace</strong>
              <small>{runLabel}</small>
            </span>
          </button>

          <div className="sidebar-spacer" />
          <section className="node-card">
            <div className="node-card-header">
              <span className="node-glyph">⌘</span>
              <span>Execution node</span>
            </div>
            <dl>
              <div><dt>Core</dt><dd>Go 1.25</dd></div>
              <div><dt>Shell</dt><dd>Wails v3</dd></div>
              <div><dt>Protocol</dt><dd>v{runtime.protocolVersion}</dd></div>
            </dl>
          </section>
          <div className="profile-row">
            <span className="profile-avatar">L</span>
            <span><strong>Local operator</strong><small>This Mac</small></span>
            <span className="profile-more">···</span>
          </div>
        </aside>

        <section className="conversation-panel">
          <div className="conversation-header">
            <div>
              <p className="eyebrow">LOCAL AGENT</p>
              <h1>What should Otto handle?</h1>
            </div>
            <div className={`run-state ${activeRun ? 'busy' : ''}`}>
              <span />{runLabel}
            </div>
          </div>

          <div className="conversation" ref={conversationRef}>
            {messages.map(message => (
              <article className={`message ${message.role}`} key={message.id}>
                <div className="message-avatar">{message.role === 'assistant' ? 'O' : message.role === 'user' ? 'You' : '!'}</div>
                <div className="message-body">
                  <div className="message-meta">{message.role === 'assistant' ? 'Otto' : message.role === 'user' ? 'You' : 'System'}</div>
                  <p>{message.text}{message.streaming && <span className="cursor" />}</p>
                </div>
              </article>
            ))}

            {approval && (
              <article className="approval-card">
                <div className="approval-topline">
                  <span className="approval-icon">!</span>
                  <span>Action requires approval</span>
                  <span className="approval-scope">LOCAL</span>
                </div>
                <h2>{approval.summary}</h2>
                <p>Agent 请求调用 <code>{approval.tool}</code>。只有你确认后，命令才会在本机继续执行。</p>
                <div className="command-preview">
                  <span className="command-prompt">›</span>
                  <pre>{formattedArguments}</pre>
                </div>
                <div className="approval-actions">
                  <button className="button secondary" type="button" disabled={resolvingApproval} onClick={() => void resolveApproval(false)}>Deny</button>
                  <button className="button primary" type="button" disabled={resolvingApproval} onClick={() => void resolveApproval(true)}>
                    {resolvingApproval ? 'Submitting…' : 'Allow once'}
                  </button>
                </div>
              </article>
            )}
          </div>

          <form className="composer" onSubmit={submit}>
            <textarea
              aria-label="Message Otto"
              value={draft}
              onChange={event => setDraft(event.target.value)}
              onKeyDown={onComposerKeyDown}
              placeholder={runtime.connected ? 'Tell Otto what to do on this computer…' : 'Waiting for local Agent Runtime…'}
              disabled={!runtime.connected || Boolean(activeRun)}
              rows={2}
            />
            <div className="composer-footer">
              <span><kbd>↵</kbd> send · <kbd>⇧↵</kbd> newline</span>
              {activeRun ? (
                <button className="stop-button" type="button" onClick={() => void cancelRun()}><span /> Stop</button>
              ) : (
                <button className="send-button" type="submit" disabled={!draft.trim() || !runtime.connected || submitting} aria-label="Send message">↑</button>
              )}
            </div>
          </form>
        </section>

        <aside className="trace-panel">
          <div className="trace-header">
            <div>
              <p className="eyebrow">LIVE ACTIVITY</p>
              <h2>Run trace</h2>
            </div>
            <span className="trace-count">{trace.length}</span>
          </div>
          <div className="trace-list">
            {trace.length === 0 ? (
              <div className="trace-empty">
                <span className="trace-empty-glyph">⌁</span>
                <strong>No run yet</strong>
                <p>Agent 的每个决策、工具调用和审批都会记录在这里。</p>
              </div>
            ) : trace.map(item => (
              <article className={`trace-item ${item.state}`} key={item.id}>
                <div className="trace-rail"><span /></div>
                <div className="trace-content">
                  <div><strong>{item.title}</strong><time>{item.time}</time></div>
                  <p>{item.detail}</p>
                  <code>{item.type}</code>
                </div>
              </article>
            ))}
          </div>
        </aside>
      </div>
    </main>
  )
}

export default App
