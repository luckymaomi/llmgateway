import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useRef, useState } from 'react'

import { catalogApi, type Credential, type CredentialProbeResult } from '@/api'
import { StatusBadge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { DialogFrame } from '@/components/ui/dialog'
import { Field, NativeSelect } from '@/components/ui/field'
import { FormProblem } from '@/features/auth/form-problem'
import { formatNumber } from '@/lib/format'

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
      <div className="form-grid">
        <Field label="测试模型" htmlFor="credential-probe-model">
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
        <span>将向该模型发送一次 hi，消耗少量上游 Token。</span>
        {probe.isPending ? (
          <div className="operation-panel__heading" aria-live="polite">
            <strong>正在等待上游响应</strong>
            <StatusBadge status="running" />
          </div>
        ) : null}
        {result ? <ProbeResult result={result} /> : null}
        {stopped ? (
          <div className="operation-panel__facts" aria-live="polite">
            <div className="operation-panel__heading">
              <strong>已停止等待</strong>
              <StatusBadge status="uncertain" />
            </div>
            <span>请求可能已到达上游并消耗 Token，不会自动重试。</span>
          </div>
        ) : null}
        {!stopped ? <FormProblem error={probe.error} /> : null}
      </div>
    </DialogFrame>
  )
}

function ProbeResult({ result }: { result: CredentialProbeResult }) {
  return (
    <div className="operation-panel__facts" aria-live="polite">
      <div className="operation-panel__heading">
        <strong>{probeResultTitle(result.status)}</strong>
        <StatusBadge status={result.status} />
      </div>
      <span>模型：{result.modelName}</span>
      {result.responseText ? <span>上游回复：{result.responseText}</span> : null}
      <span>耗时：{formatNumber(result.latencyMillis)} ms</span>
      <span>
        Token：{formatTokenCount(result.inputTokens)} 输入 / {formatTokenCount(result.outputTokens)}{' '}
        输出
      </span>
      {result.errorKind ? (
        <span>
          错误：{result.errorKind}
          {result.retryable ? '，可以重试' : ''}
        </span>
      ) : null}
      <span>Request ID：{result.requestId}</span>
    </div>
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
