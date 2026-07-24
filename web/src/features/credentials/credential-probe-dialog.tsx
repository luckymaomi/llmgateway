import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useRef, useState } from 'react'

import { catalogApi, type Credential, type CredentialProbeResult } from '@/api'
import { StatusBadge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { DialogFrame } from '@/components/ui/dialog'
import { Field, NativeSelect } from '@/components/ui/field'
import { FormProblem } from '@/features/auth/form-problem'
import { formatNumber } from '@/lib/format'

import { probeErrorLabel } from './credential-probe-copy'

export function CredentialProbeDialog({
  credential,
  onOpenChange,
}: {
  credential: Credential
  onOpenChange: (open: boolean) => void
}) {
  const queryClient = useQueryClient()
  const [modelId, setModelId] = useState(credential.modelBindings[0]?.modelId ?? '')
  const [result, setResult] = useState<CredentialProbeResult>()
  const [stopped, setStopped] = useState(false)
  const controller = useRef<AbortController | undefined>(undefined)
  const probe = useMutation({
    mutationFn: () => {
      const nextController = new AbortController()
      controller.current = nextController
      return catalogApi.probeCredential(credential.id, modelId, nextController.signal)
    },
    onSuccess(value) {
      setResult(value)
      return queryClient.invalidateQueries({ queryKey: ['credentials'] })
    },
    onSettled() {
      controller.current = undefined
    },
  })

  const startProbe = () => {
    setStopped(false)
    setResult(undefined)
    probe.reset()
    probe.mutate()
  }

  const stopWaiting = () => {
    setStopped(true)
    controller.current?.abort()
  }

  return (
    <DialogFrame
      open
      onOpenChange={(open) => {
        if (!probe.isPending || open) onOpenChange(open)
      }}
      title="测试上游 API Key"
      description={`${credential.name} · ${credential.resourcePoolName}`}
      dismissible={!probe.isPending}
      width="sm"
      footer={
        probe.isPending ? (
          <Button type="button" variant="secondary" onClick={stopWaiting}>
            停止等待
          </Button>
        ) : (
          <>
            <Button type="button" variant="secondary" onClick={() => onOpenChange(false)}>
              关闭
            </Button>
            <Button type="button" disabled={!modelId} onClick={startProbe}>
              {result ? '重新测试' : '开始测试'}
            </Button>
          </>
        )
      }
    >
      <div className="form-stack">
        <Field label="选择测试模型" htmlFor="credential-probe-model">
          <NativeSelect
            id="credential-probe-model"
            autoFocus
            value={modelId}
            disabled={probe.isPending}
            onChange={(event) => {
              setModelId(event.target.value)
              setResult(undefined)
            }}
          >
            {credential.modelBindings.map((binding) => (
              <option key={binding.modelId} value={binding.modelId}>
                {binding.modelName}
              </option>
            ))}
          </NativeSelect>
        </Field>
        <p className="probe-note">会向上游发送一条最小消息并等待完整响应，可能消耗少量 Token。</p>
        {probe.isPending ? (
          <div className="probe-pending" aria-live="polite">
            <div>
              <strong>正在连接上游</strong>
              <span>请等待模型返回，超时前不会提前判定失败</span>
            </div>
            <StatusBadge status="running" />
          </div>
        ) : null}
        {result ? <ProbeResult result={result} /> : null}
        {stopped ? (
          <div className="probe-result" aria-live="polite">
            <div className="probe-result__header">
              <strong>已停止等待</strong>
              <StatusBadge status="uncertain" />
            </div>
            <p>请求可能已经到达上游并消耗 Token，系统不会自动重试。</p>
          </div>
        ) : null}
        {!stopped ? <FormProblem error={probe.error} /> : null}
      </div>
    </DialogFrame>
  )
}

function ProbeResult({ result }: { result: CredentialProbeResult }) {
  return (
    <section className="probe-result" data-status={result.status} aria-live="polite">
      <div className="probe-result__header">
        <strong>{probeResultTitle(result.status)}</strong>
        <StatusBadge status={result.status} />
      </div>
      <dl className="probe-result__facts">
        <div>
          <dt>模型</dt>
          <dd>{result.modelName}</dd>
        </div>
        <div>
          <dt>耗时</dt>
          <dd>{formatNumber(result.latencyMillis)} ms</dd>
        </div>
        <div>
          <dt>输入 Token</dt>
          <dd>{formatTokenCount(result.inputTokens)}</dd>
        </div>
        <div>
          <dt>输出 Token</dt>
          <dd>{formatTokenCount(result.outputTokens)}</dd>
        </div>
      </dl>
      {result.errorKind ? (
        <div className="probe-result__error">
          <strong>{probeErrorLabel(result.errorKind)}</strong>
          <span>{result.retryable ? '可以直接重新测试' : '请先检查 Key、模型或网络设置'}</span>
        </div>
      ) : (
        <p className="probe-result__message">已收到并解析上游模型响应。</p>
      )}
      <div className="probe-result__request">
        <span>Request ID</span>
        <code>{result.requestId}</code>
      </div>
    </section>
  )
}

function probeResultTitle(status: CredentialProbeResult['status']): string {
  if (status === 'succeeded') return '连接成功'
  if (status === 'uncertain') return '结果待确认'
  return '连接失败'
}

function formatTokenCount(value: number | undefined): string {
  return value === undefined ? '未知' : formatNumber(value)
}
