import { CSSProperties, FormEvent, KeyboardEvent, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Events } from '@wailsio/runtime'
import { RuntimeService } from '../bindings/github.com/wojustme/otto'

// RuntimeInfo 是界面启动时从 Wails 后端读取的 sidecar 状态快照。
type RuntimeInfo = {
  connected: boolean
  runtimeVersion: string
  protocolVersion: number
}

// RuntimeEvent 对应 Go protocol.Event，是驱动消息区和运行轨迹的统一事件。
type RuntimeEvent = {
  protocolVersion: number
  type: string
  requestId?: string
  runId?: string
  sequence?: number
  occurredAt?: string
  data?: Record<string, unknown>
}

// Message 是聊天区的展示模型；notice 用于呈现不属于对话的系统错误。
type Message = {
  id: string
  role: 'assistant' | 'user' | 'notice'
  text: string
  runId?: string
  streaming?: boolean
}

// TraceItem 是协议事件在右侧运行轨迹中的可视化形式。
// active 表示进行中，done 表示完成，waiting 表示等待审批，error 表示失败或拒绝。
type TraceItem = {
  id: string
  type: string
  title: string
  detail: string
  state: 'active' | 'done' | 'waiting' | 'error'
  time: string
}

// Approval 保存当前唯一待处理的工具审批及其参数预览。
type Approval = {
  runId: string
  approvalId: string
  tool: string
  summary: string
  arguments: unknown
}

// initialMessages 是新会话的欢迎内容，也是“New session”重置后的初始状态。
const initialMessages: Message[] = [{
  id: 'welcome',
  role: 'assistant',
  text: '我是 Otto。桌面端已经连接本地 Go Agent Runtime，可以从一条真实的审批链路开始。',
}]

// fontScaleStorageKey 是字号偏好在当前 WebView localStorage 中的持久化键。
const fontScaleStorageKey = 'otto.interface.font-scale'
// Otto 面向信息密集的桌面窗口，默认字号高于浏览器基础值；
// 用户仍可在 90% 到 140% 之间调整，无需重新构建 CSS。
const defaultFontScale = 120

// initialFontScale 读取并校验持久化字号；无效或不可访问时回退到默认值。
function initialFontScale(): number {
  try {
    const stored = Number.parseInt(window.localStorage.getItem(fontScaleStorageKey) ?? '', 10)
    // 只有有限数值且位于滑杆范围内时才接受，防止旧数据或手工修改破坏布局。
    return Number.isFinite(stored) && stored >= 90 && stored <= 140 ? stored : defaultFontScale
  } catch {
    // 隐私模式等环境可能禁止 localStorage，此时仍允许应用使用默认字号运行。
    return defaultFontScale
  }
}

// valueAsRecord 把未知事件数据安全收窄为对象，避免直接访问非对象字段。
function valueAsRecord(value: unknown): Record<string, unknown> {
  return typeof value === 'object' && value !== null ? value as Record<string, unknown> : {}
}

// valueAsString 提取字符串字段；类型不符时返回调用方给定的回退文案。
function valueAsString(value: unknown, fallback = ''): string {
  return typeof value === 'string' ? value : fallback
}

// eventTime 把协议时间格式化为本地 24 小时时间；缺失时使用当前时间。
function eventTime(value?: string): string {
  // 某些本地错误没有协议时间，使用当前时间可以保持轨迹仍可读。
  if (!value) return new Date().toLocaleTimeString('zh-CN', { hour12: false })
  return new Date(value).toLocaleTimeString('zh-CN', { hour12: false })
}

// traceCopy 将底层事件转换为用户可理解的标题、详情和视觉状态。
function traceCopy(event: RuntimeEvent): Pick<TraceItem, 'title' | 'detail' | 'state'> {
  const data = valueAsRecord(event.data)
  const call = valueAsRecord(data.call)
  const tool = valueAsString(call.name, valueAsString(data.name, 'tool'))
  switch (event.type) {
    case 'run.started':
      // 新任务已经被 Runtime 接受。
      return { title: 'Run started', detail: 'Agent 已接收新任务', state: 'active' }
    case 'model.text.delta':
      // 模型正在产生流式文本，任务仍处于进行中。
      return { title: 'Model response', detail: '收到模型输出', state: 'active' }
    case 'model.response.completed':
      // 当前模型回合结束，但后续仍可能有任务完成事件。
      return { title: 'Model completed', detail: valueAsString(data.stopReason, 'completed'), state: 'done' }
    case 'tool.requested':
      // 模型提出工具意图，尚未代表工具已经执行。
      return { title: `${tool} requested`, detail: '模型请求调用本地工具', state: 'active' }
    case 'approval.requested':
      // 工具在副作用发生前暂停，等待用户决定。
      return { title: 'Approval required', detail: `${tool} 正在等待你的确认`, state: 'waiting' }
    case 'approval.resolved':
      // 用户已经给出审批结论，任务可以继续推进。
      return { title: 'Approval resolved', detail: data.approved ? '已允许执行' : '已拒绝执行', state: 'done' }
    case 'tool.started':
      // 工具已通过策略检查，开始在本机执行。
      return { title: `${tool} started`, detail: '工具正在本地运行', state: 'active' }
    case 'tool.completed':
      // isError 区分结构化工具错误和正常结果。
      return { title: `${tool} completed`, detail: data.isError ? '工具返回错误' : '工具执行成功', state: data.isError ? 'error' : 'done' }
    case 'tool.denied':
      // 工具被用户或策略拒绝，没有产生本地副作用。
      return { title: `${tool} denied`, detail: valueAsString(data.reason, '已拒绝'), state: 'error' }
    case 'run.completed':
      // 整个任务正常进入完成终态。
      return { title: 'Run completed', detail: '本轮任务已完成', state: 'done' }
    case 'run.cancelled':
      // 任务被主动终止，界面以错误色提示非正常完成。
      return { title: 'Run cancelled', detail: '任务已取消', state: 'error' }
    case 'run.failed':
      // 引擎或模型失败，优先展示协议提供的错误信息。
      return { title: 'Run failed', detail: valueAsString(data.message, 'Agent 执行失败'), state: 'error' }
    case 'runtime.error':
      // 命令校验或 Runtime 状态错误不一定属于模型回合。
      return { title: 'Runtime error', detail: valueAsString(data.message, 'Runtime 请求失败'), state: 'error' }
    default:
      // 未识别事件仍保留原始类型，方便协议升级期间调试。
      return { title: event.type, detail: 'Runtime event', state: 'done' }
  }
}

// App 维护 Otto 主窗口的聊天、审批、轨迹、连接和界面偏好状态。
function App() {
  // runtime 描述 sidecar 连接；messages 和 trace 分别驱动中间与右侧区域。
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

  // handleRuntimeEvent 是协议事件到 React 状态的唯一归约入口。
  const handleRuntimeEvent = useCallback((event: RuntimeEvent) => {
    const data = valueAsRecord(event.data)
    const runId = event.runId ?? ''

    // 就绪事件只更新连接状态，不进入运行轨迹：它属于进程生命周期，
    // 而不是某一次 Agent 决策。
    if (event.type === 'runtime.ready') {
      setRuntime(current => ({ ...current, connected: true, runtimeVersion: valueAsString(data.runtimeVersion, 'unknown') }))
      return
    }

    const copy = traceCopy(event)
    // 轨迹按最新事件优先展示并限制数量，既便于观察实时状态，
    // 也避免长会话让 React 状态无限增长。
    setTrace(current => [{
      id: `${event.requestId ?? 'event'}-${event.sequence ?? Date.now()}-${event.type}`,
      type: event.type,
      time: eventTime(event.occurredAt),
      ...copy,
    }, ...current].slice(0, 30))

    switch (event.type) {
      case 'run.started':
        // 只有 Runtime 确认启动后才把用户消息加入对话，并记录活动任务。
        setActiveRun(runId)
        setMessages(current => [...current, {
          id: `${runId}-user`, role: 'user', runId, text: valueAsString(data.message),
        }])
        break
      case 'model.text.delta': {
        // 模型增量按 runID 合并，支持未来多个任务事件交错到达。
        const delta = valueAsString(data.delta)
        setMessages(current => {
          const last = current[current.length - 1]
          // 同一任务的连续增量会追加到同一个气泡，
          // 避免为每个模型流式片段创建独立的 React 节点。
          if (last?.role === 'assistant' && last.runId === runId && last.streaming) {
            return [...current.slice(0, -1), { ...last, text: last.text + delta }]
          }
          return [...current, { id: `${runId}-assistant-${event.sequence}`, role: 'assistant', runId, text: delta, streaming: true }]
        })
        break
      }
      case 'model.response.completed':
        // 模型回合结束时关闭对应消息的流式光标。
        setMessages(current => current.map(message => message.runId === runId ? { ...message, streaming: false } : message))
        break
      case 'approval.requested': {
        // 保存审批快照后，界面会显示参数预览和允许/拒绝按钮。
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
        // Runtime 已接受审批决定，可以关闭卡片并恢复按钮状态。
        setApproval(null)
        setResolvingApproval(false)
        break
      case 'run.completed':
      case 'run.cancelled':
        // 正常完成和主动取消都会释放单任务输入锁。
        setActiveRun(null)
        setSubmitting(false)
        break
      case 'run.failed':
      case 'runtime.error':
        // 失败分支重置所有进行中状态，并追加可见的系统通知。
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

  // 首次挂载时读取 Runtime 状态并订阅持续事件流。
  useEffect(() => {
    RuntimeService.RuntimeInfo()
      .then(info => setRuntime({
        connected: info.connected,
        runtimeVersion: info.runtimeVersion || 'unknown',
        protocolVersion: info.protocolVersion,
      }))
      // 初始化查询失败时保留应用界面，并把错误作为系统消息展示。
      .catch(error => setMessages(current => [...current, {
        id: 'runtime-start-error', role: 'notice', text: `Runtime 启动失败：${String(error)}`,
      }]))

    // Wails 会返回取消订阅函数，React 卸载时调用它，
    // 防止开发环境重复挂载产生多个监听器。
    return Events.On('otto:runtime-event', event => handleRuntimeEvent(event.data as RuntimeEvent))
  }, [handleRuntimeEvent])

  // 消息或审批变化后自动滚动到底部，让最新交互保持可见。
  useEffect(() => {
    conversationRef.current?.scrollTo({ top: conversationRef.current.scrollHeight, behavior: 'smooth' })
  }, [messages, approval])

  // 字号变化后立即持久化；写入失败不会影响当前会话内的显示。
  useEffect(() => {
    try {
      window.localStorage.setItem(fontScaleStorageKey, String(fontScale))
    } catch {
      // 存储不可用时，所选字号仍会在当前会话内生效。
    }
  }, [fontScale])

  // 设置面板打开时监听 Escape，关闭后及时移除全局监听器。
  useEffect(() => {
    // 面板关闭时无需注册键盘事件。
    if (!settingsOpen) return
    const closeOnEscape = (event: globalThis.KeyboardEvent) => {
      // 仅 Escape 用于关闭，其他快捷键继续交给应用处理。
      if (event.key === 'Escape') setSettingsOpen(false)
    }
    window.addEventListener('keydown', closeOnEscape)
    return () => window.removeEventListener('keydown', closeOnEscape)
  }, [settingsOpen])

  // submit 校验输入状态并通过 Wails 服务启动新任务。
  const submit = async (event?: FormEvent) => {
    event?.preventDefault()
    const message = draft.trim()
    // 空消息、重复提交、已有活动任务或 Runtime 离线时都禁止发送。
    if (!message || submitting || activeRun || !runtime.connected) return
    setDraft('')
    setSubmitting(true)
    try {
      const runId = await RuntimeService.StartRun(message)
      setActiveRun(runId)
    } catch (error) {
      // 发送失败时恢复草稿，避免用户输入丢失。
      setSubmitting(false)
      setDraft(message)
      setMessages(current => [...current, { id: `send-error-${Date.now()}`, role: 'notice', text: `发送失败：${String(error)}` }])
    }
  }

  // onComposerKeyDown 实现 Enter 发送、Shift+Enter 换行的桌面输入习惯。
  const onComposerKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    // 只有未按 Shift 的 Enter 才提交，其余按键保持文本框默认行为。
    if (event.key === 'Enter' && !event.shiftKey) {
      event.preventDefault()
      void submit()
    }
  }

  // resolveApproval 把当前审批决定提交给 Runtime，并防止按钮重复点击。
  const resolveApproval = async (approved: boolean) => {
    // 没有待审批项或正在提交时不重复发送响应。
    if (!approval || resolvingApproval) return
    setResolvingApproval(true)
    try {
      await RuntimeService.RespondToApproval(approval.runId, approval.approvalId, approved)
    } catch (error) {
      // 提交失败后重新开放按钮，并把错误加入对话区。
      setResolvingApproval(false)
      setMessages(current => [...current, { id: `approval-error-${Date.now()}`, role: 'notice', text: `审批提交失败：${String(error)}` }])
    }
  }

  // cancelRun 请求 Runtime 取消当前活动任务。
  const cancelRun = async () => {
    // 没有活动任务时取消操作没有目标。
    if (!activeRun) return
    try {
      await RuntimeService.CancelRun(activeRun)
    } catch (error) {
      // 取消失败不清除 activeRun，避免界面误认为任务已经停止。
      setMessages(current => [...current, { id: `cancel-error-${Date.now()}`, role: 'notice', text: `取消失败：${String(error)}` }])
    }
  }

  // statusLabel 只描述 sidecar 的在线状态，不代表某个任务是否正在运行。
  const statusLabel = runtime.connected ? 'Runtime online' : 'Runtime offline'
  // runLabel 按“等待审批、正在运行、空闲”的优先级呈现当前任务状态。
  const runLabel = approval ? 'Waiting approval' : activeRun ? 'Agent running' : 'Ready'
  // 仅在存在审批时格式化工具参数，避免空闲状态执行无意义的序列化。
  const formattedArguments = useMemo(() => approval ? JSON.stringify(approval.arguments, null, 2) : '', [approval])
  // 使用一个 CSS 自定义属性统一缩放所有字号，
  // 同时保持面板尺寸和信息密度不变。
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

      {/* 只有用户打开设置时才挂载遮罩和对话框，关闭后不占用交互层。 */}
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
                {/* 每个预设代表一个常用字号百分比；自定义滑杆仍可选择其余档位。 */}
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
          {/* 活动任务存在时禁止重置会话，避免丢失仍在到达的事件上下文。 */}
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

            {/* approval 非空表示工具仍在等待决定，此时展示参数和一次性授权操作。 */}
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
              {/* 有活动任务时发送入口切换为停止按钮，否则允许提交新消息。 */}
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
            {/* 没有运行事件时展示引导空态；收到事件后改为按时间排列的轨迹。 */}
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
