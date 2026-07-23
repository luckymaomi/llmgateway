import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'

import { gatewayKeyTestApi, type GatewayKey } from '@/api'
import { StatusBadge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { DialogFrame } from '@/components/ui/dialog'
import { Field, NativeSelect, Textarea } from '@/components/ui/field'
import { ErrorState } from '@/components/ui/state'
import { formatNumber } from '@/lib/format'

import { useGatewayKeyTestRun } from './gateway-key-test-run'

const terminalPhases = ['idle', 'completed', 'failed', 'canceled', 'uncertain']

export function GatewayKeyTestDialog({
  gatewayKey,
  onOpenChange,
}: {
  gatewayKey: GatewayKey
  onOpenChange: (open: boolean) => void
}) {
  const models = useQuery({
    queryKey: ['gateway-key-test-models', gatewayKey.id],
    queryFn: ({ signal }) => gatewayKeyTestApi.models(gatewayKey.id, signal),
  })
  const [model, setModel] = useState('')
  const [message, setMessage] = useState('hi')
  const selectedModel = model || models.data?.[0]?.alias || ''
  const test = useGatewayKeyTestRun()
  const running = !terminalPhases.includes(test.facts.phase)
  const canRun = Boolean(selectedModel && message.trim() && !running)

  const start = () => {
    if (!canRun) return
    void test.run({
      apiKeyId: gatewayKey.id,
      model: selectedModel,
      message: message.trim(),
    })
  }

  return (
    <DialogFrame
      open
      onOpenChange={(open) => {
        if (!running || open) onOpenChange(open)
      }}
      title="测试 API 密钥"
      dismissible={!running}
      width="sm"
      footer={
        running ? (
          <Button type="button" variant="secondary" onClick={test.cancel}>
            停止等待
          </Button>
        ) : (
          <>
            <Button type="button" variant="secondary" onClick={() => onOpenChange(false)}>
              关闭
            </Button>
            <Button type="button" disabled={!canRun} onClick={start}>
              {test.facts.phase === 'idle' ? '开始测试' : '重新测试'}
            </Button>
          </>
        )
      }
    >
      {models.error ? (
        <ErrorState error={models.error} onRetry={() => void models.refetch()} />
      ) : (
        <div className="form-grid">
          <Field label="模型" htmlFor="gateway-key-test-model">
            <NativeSelect
              id="gateway-key-test-model"
              autoFocus
              value={selectedModel}
              disabled={running || models.isLoading}
              onChange={(event) => setModel(event.target.value)}
            >
              {models.data?.map((item) => (
                <option key={item.id} value={item.alias}>
                  {item.alias}
                </option>
              ))}
            </NativeSelect>
          </Field>
          <Field label="测试消息" htmlFor="gateway-key-test-message">
            <Textarea
              id="gateway-key-test-message"
              rows={3}
              value={message}
              disabled={running}
              onChange={(event) => setMessage(event.target.value)}
            />
          </Field>
          <span>请求会计入这把 API 密钥所属成员的额度和用量。</span>
          {test.facts.phase !== 'idle' ? <GatewayKeyTestResult facts={test.facts} /> : null}
        </div>
      )}
    </DialogFrame>
  )
}

function GatewayKeyTestResult({
  facts,
}: {
  facts: ReturnType<typeof useGatewayKeyTestRun>['facts']
}) {
  return (
    <div className="operation-panel__facts" aria-live="polite">
      <div className="operation-panel__heading">
        <strong>{facts.step}</strong>
        <StatusBadge status={facts.phase} />
      </div>
      {facts.responseText ? <span>上游回复：{facts.responseText}</span> : null}
      <span>
        Token：{formatTokenCount(facts.inputTokens)} 输入 / {formatTokenCount(facts.outputTokens)}{' '}
        输出
      </span>
      {facts.requestId ? <span>Request ID：{facts.requestId}</span> : null}
      {facts.error ? (
        <span>
          {facts.error.message}（{facts.error.code}）
        </span>
      ) : null}
    </div>
  )
}

function formatTokenCount(value: number | undefined): string {
  return value === undefined ? '未知' : formatNumber(value)
}
