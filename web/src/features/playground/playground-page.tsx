import { useQuery } from '@tanstack/react-query'
import {
  Activity,
  Ban,
  Braces,
  Eraser,
  MessageSquare,
  Play,
  Send,
  SlidersHorizontal,
  Wrench,
} from 'lucide-react'
import { useMemo, useState } from 'react'

import { accessApi, playgroundApi, type PlaygroundRunInput } from '@/api'
import { Page, PageHeader } from '@/components/layout'
import { Badge, StatusBadge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Field, NativeSelect, Textarea } from '@/components/ui/field'
import { ErrorState } from '@/components/ui/state'
import { formatNumber } from '@/lib/format'

import { usePlaygroundRun } from './use-playground-run'

const terminal = ['idle', 'completed', 'failed', 'canceled', 'uncertain']
type PlaygroundView = 'conversation' | 'settings' | 'facts'

export function PlaygroundPage() {
  const keys = useQuery({
    queryKey: ['playground-keys'],
    queryFn: ({ signal }) => accessApi.keys({ page: 1, pageSize: 200 }, signal),
  })
  const activeKeys = useMemo(
    () => keys.data?.items.filter((item) => item.status === 'active') ?? [],
    [keys.data],
  )
  const [gatewayKeyId, setGatewayKeyId] = useState('')
  const selectedKey = activeKeys.find((item) => item.id === gatewayKeyId) ?? activeKeys[0]
  const activeGatewayKeyId = gatewayKeyId || selectedKey?.id || ''
  const models = useQuery({
    queryKey: ['playground-models', activeGatewayKeyId],
    queryFn: ({ signal }) => playgroundApi.models(activeGatewayKeyId, signal),
    enabled: Boolean(activeGatewayKeyId),
  })
  const [model, setModel] = useState('')
  const [prompt, setPrompt] = useState('')
  const [system, setSystem] = useState('')
  const [reasoningEnabled, setReasoningEnabled] = useState(false)
  const [reasoningEffort, setReasoningEffort] = useState<'low' | 'medium' | 'high'>('medium')
  const [stream, setStream] = useState(true)
  const [toolJson, setToolJson] = useState('')
  const [toolError, setToolError] = useState('')
  const [mobileView, setMobileView] = useState<PlaygroundView>('conversation')
  const run = usePlaygroundRun()
  const selectedModel = models.data?.find((item) => item.alias === model) ?? models.data?.[0]
  const activeModel = model || selectedModel?.alias || ''
  const reasoningMode = selectedModel?.reasoningMode
  const supportsReasoningToggle = reasoningMode === 'toggle' || reasoningMode === 'hybrid'
  const supportsReasoningEffort = reasoningMode === 'effort' || reasoningMode === 'hybrid'
  const running = !terminal.includes(run.facts.phase)

  const canSubmit = Boolean(activeModel && prompt.trim() && !running)

  const historyMessages = useMemo(
    () => run.messages.map(({ role, content }) => ({ role, content })),
    [run.messages],
  )

  const submit = () => {
    if (!canSubmit) return
    let tools: PlaygroundRunInput['tools']
    if (toolJson.trim()) {
      try {
        const parsed = JSON.parse(toolJson) as PlaygroundRunInput['tools']
        if (!Array.isArray(parsed)) throw new Error('tools must be an array')
        tools = parsed
        setToolError('')
      } catch {
        setToolError('工具定义必须是 JSON 数组')
        return
      }
    }
    const messages: PlaygroundRunInput['messages'] = [
      ...(system.trim() ? [{ role: 'system' as const, content: system.trim() }] : []),
      ...historyMessages,
      { role: 'user', content: prompt.trim() },
    ]
    const input: PlaygroundRunInput = {
      gatewayKeyId: activeGatewayKeyId,
      model: activeModel,
      stream,
      messages,
      ...(supportsReasoningToggle ? { reasoningEnabled } : {}),
      ...(supportsReasoningEffort && (!supportsReasoningToggle || reasoningEnabled)
        ? { reasoningEffort }
        : {}),
      ...(tools ? { tools } : {}),
    }
    setPrompt('')
    void run.run(input)
  }

  return (
    <Page className="page--playground">
      <PageHeader
        title="Playground"
        description="文本、工具调用、推理与流式响应"
        actions={
          <Button variant="secondary" icon={<Eraser size={16} />} onClick={run.clear}>
            清空
          </Button>
        }
      />
      {keys.error ? (
        <ErrorState error={keys.error} onRetry={() => void keys.refetch()} />
      ) : models.error ? (
        <ErrorState error={models.error} onRetry={() => void models.refetch()} />
      ) : (
        <div className="playground-workspace" data-mobile-view={mobileView}>
          <div className="playground-view-switcher" role="group" aria-label="Playground 视图">
            <button
              type="button"
              aria-pressed={mobileView === 'conversation'}
              onClick={() => setMobileView('conversation')}
            >
              <MessageSquare size={16} />
              对话
            </button>
            <button
              type="button"
              aria-pressed={mobileView === 'settings'}
              onClick={() => setMobileView('settings')}
            >
              <SlidersHorizontal size={16} />
              设置
            </button>
            <button
              type="button"
              aria-pressed={mobileView === 'facts'}
              onClick={() => setMobileView('facts')}
            >
              <Activity size={16} />
              运行事实
            </button>
          </div>

          <aside id="playground-settings" className="playground-controls" aria-label="请求设置">
            <Field label="网关 Key" htmlFor="playground-key">
              <NativeSelect
                id="playground-key"
                value={activeGatewayKeyId}
                onChange={(event) => {
                  setGatewayKeyId(event.target.value)
                  setModel('')
                }}
              >
                {activeKeys.map((item) => (
                  <option key={item.id} value={item.id}>
                    {item.name} · {item.prefix}…
                  </option>
                ))}
              </NativeSelect>
            </Field>
            <Field label="模型" htmlFor="playground-model">
              <NativeSelect
                id="playground-model"
                value={activeModel}
                onChange={(event) => setModel(event.target.value)}
              >
                {models.data?.map((item) => (
                  <option key={item.id} value={item.alias}>
                    {item.alias} · {item.providerName}
                  </option>
                ))}
              </NativeSelect>
            </Field>
            <div className="capability-strip">
              {selectedModel?.capabilities.map((capability) => (
                <Badge key={capability}>{capabilityLabel[capability]}</Badge>
              ))}
            </div>
            <Field label="System" htmlFor="playground-system">
              <Textarea
                id="playground-system"
                rows={4}
                value={system}
                onChange={(event) => setSystem(event.target.value)}
              />
            </Field>
            {supportsReasoningToggle ? (
              <label className="switch-row">
                <span>启用推理</span>
                <input
                  type="checkbox"
                  role="switch"
                  checked={reasoningEnabled}
                  onChange={(event) => setReasoningEnabled(event.target.checked)}
                />
              </label>
            ) : null}
            {supportsReasoningEffort ? (
              <Field label="推理强度" htmlFor="playground-reasoning">
                <NativeSelect
                  id="playground-reasoning"
                  value={reasoningEffort}
                  disabled={supportsReasoningToggle && !reasoningEnabled}
                  onChange={(event) =>
                    setReasoningEffort(event.target.value as typeof reasoningEffort)
                  }
                >
                  <option value="low">低</option>
                  <option value="medium">中</option>
                  <option value="high">高</option>
                </NativeSelect>
              </Field>
            ) : null}
            <label className="switch-row">
              <span>流式响应</span>
              <input
                type="checkbox"
                role="switch"
                checked={stream}
                onChange={(event) => setStream(event.target.checked)}
              />
            </label>
            <Field
              label="Function tools"
              htmlFor="playground-tools"
              error={toolError}
              hint="JSON 数组"
            >
              <Textarea
                id="playground-tools"
                rows={8}
                value={toolJson}
                onChange={(event) => setToolJson(event.target.value)}
                spellCheck={false}
              />
            </Field>
          </aside>

          <section id="playground-conversation" className="conversation" aria-label="对话">
            <div className="conversation__messages" aria-live="polite">
              {run.messages.length === 0 ? (
                <div className="conversation__empty">
                  <Send size={24} />
                  <span>等待第一条消息</span>
                </div>
              ) : (
                run.messages.map((message) => (
                  <article className={`message message--${message.role}`} key={message.id}>
                    <header>
                      <strong>
                        {message.role === 'user'
                          ? '你'
                          : message.role === 'assistant'
                            ? activeModel
                            : message.role}
                      </strong>
                    </header>
                    {message.reasoning ? (
                      <details className="reasoning">
                        <summary>推理内容</summary>
                        <pre>{message.reasoning}</pre>
                      </details>
                    ) : null}
                    {message.content ? (
                      <div className="message__content">{message.content}</div>
                    ) : message.role === 'assistant' && running ? (
                      <span className="stream-cursor">正在生成</span>
                    ) : null}
                    {message.toolCall ? (
                      <div className="tool-call">
                        <Wrench size={15} />
                        <strong>{message.toolCall.name}</strong>
                        <pre>{message.toolCall.arguments}</pre>
                      </div>
                    ) : null}
                  </article>
                ))
              )}
            </div>
            <div className="composer">
              <label htmlFor="playground-prompt" className="sr-only">
                消息
              </label>
              <Textarea
                id="playground-prompt"
                rows={3}
                placeholder="输入消息"
                value={prompt}
                onChange={(event) => setPrompt(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === 'Enter' && (event.ctrlKey || event.metaKey)) submit()
                }}
              />
              <div className="composer__actions">
                {running ? (
                  <Button variant="danger" icon={<Ban size={16} />} onClick={run.cancel}>
                    取消
                  </Button>
                ) : (
                  <Button icon={<Play size={16} />} disabled={!canSubmit} onClick={submit}>
                    运行
                  </Button>
                )}
              </div>
            </div>
          </section>

          <aside
            id="playground-facts"
            className="run-facts"
            aria-label="运行事实"
            aria-live="polite"
          >
            <header>
              <h2>运行事实</h2>
              {run.facts.phase !== 'idle' ? <StatusBadge status={run.facts.phase} /> : null}
            </header>
            <dl>
              <div>
                <dt>阶段</dt>
                <dd>{run.facts.step}</dd>
              </div>
              <div>
                <dt>Request ID</dt>
                <dd>
                  <code>{run.facts.requestId ?? '尚未分配'}</code>
                </dd>
              </div>
              <div>
                <dt>输入 Token</dt>
                <dd>
                  {run.facts.inputTokens === undefined
                    ? '未知'
                    : formatNumber(run.facts.inputTokens)}
                </dd>
              </div>
              <div>
                <dt>输出 Token</dt>
                <dd>
                  {run.facts.outputTokens === undefined
                    ? '未知'
                    : formatNumber(run.facts.outputTokens)}
                </dd>
              </div>
              <div>
                <dt>Usage 来源</dt>
                <dd>
                  {run.facts.usageSource === 'authoritative'
                    ? '上游权威'
                    : run.facts.usageSource === 'estimated'
                      ? '本地估算'
                      : '未知'}
                </dd>
              </div>
            </dl>
            {run.facts.error ? (
              <div className="inline-problem" role="alert">
                <strong>{run.facts.error.message}</strong>
                <code>{run.facts.error.code}</code>
                {run.facts.error.stage ? <span>阶段：{run.facts.error.stage}</span> : null}
              </div>
            ) : null}
            <div className="run-facts__protocol">
              <Braces size={15} />
              <span>
                {stream ? 'SSE' : 'JSON'} ·{' '}
                {reasoningFact(reasoningMode, reasoningEnabled, reasoningEffort)}
              </span>
            </div>
          </aside>
        </div>
      )}
    </Page>
  )
}

const capabilityLabel = {
  streaming: '流式',
  tools: '工具',
  reasoning: '推理',
  structured_output: '结构化',
} as const

function reasoningFact(
  mode: 'toggle' | 'effort' | 'hybrid' | undefined,
  enabled: boolean,
  effort: 'low' | 'medium' | 'high',
) {
  if (mode === 'effort') return effort
  if (mode === 'hybrid' && enabled) return effort
  if (mode === 'toggle' || mode === 'hybrid') return enabled ? '推理开启' : '推理关闭'
  return '无推理'
}
