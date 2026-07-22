import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Check, Copy } from 'lucide-react'
import { useState } from 'react'

import { accessApi, type CreatedGatewayKey, type GatewayKey } from '@/api'
import { Button } from '@/components/ui/button'
import { DialogFrame } from '@/components/ui/dialog'
import { FormProblem } from '@/features/auth/form-problem'

interface KeyReplacementDialogProps {
  gatewayKey: GatewayKey | null
  onOpenChange: (open: boolean) => void
}

export function KeyReplacementDialog({ gatewayKey, onOpenChange }: KeyReplacementDialogProps) {
  const queryClient = useQueryClient()
  const [operationKey, setOperationKey] = useState('')
  const [created, setCreated] = useState<CreatedGatewayKey | null>(null)
  const [copied, setCopied] = useState(false)
  const [copyFailed, setCopyFailed] = useState(false)
  const mutation = useMutation({
    mutationFn: (input: { keyId: string; idempotencyKey: string }) =>
      accessApi.replaceKey(input.keyId, input.idempotencyKey),
    onSuccess: async (result) => {
      setCreated(result)
      await queryClient.invalidateQueries({ queryKey: ['gateway-keys'] })
    },
  })
  const gatewayBaseURL = `${window.location.origin}/v1`

  function close(): void {
    if (mutation.isPending) return
    mutation.reset()
    setOperationKey('')
    setCreated(null)
    setCopied(false)
    setCopyFailed(false)
    onOpenChange(false)
  }

  async function replace(): Promise<void> {
    if (!gatewayKey || mutation.isPending) return
    const idempotencyKey = operationKey || crypto.randomUUID()
    setOperationKey(idempotencyKey)
    try {
      await mutation.mutateAsync({ keyId: gatewayKey.id, idempotencyKey })
    } catch {
      // Reuse the same in-memory operation key when the outcome is unknown.
    }
  }

  return (
    <DialogFrame
      open={gatewayKey !== null}
      onOpenChange={(open) => {
        if (!open) close()
      }}
      title={created ? '替换 Key 已创建' : '更换 API Key'}
      description={gatewayKey?.name ?? ''}
      dismissible={!mutation.isPending}
      footer={
        created ? (
          <Button onClick={close}>完成</Button>
        ) : (
          <>
            <Button variant="secondary" disabled={mutation.isPending} onClick={close}>
              取消
            </Button>
            <Button disabled={mutation.isPending} onClick={() => void replace()}>
              {mutation.isPending ? '创建中' : mutation.isError ? '重试创建' : '创建替换 Key'}
            </Button>
          </>
        )
      }
    >
      {created ? (
        <>
          <div className="secret-reveal">
            <code data-testid="replacement-key-secret">{created.secret}</code>
            <Button
              variant="secondary"
              icon={copied ? <Check size={16} /> : <Copy size={16} />}
              onClick={async () => {
                try {
                  await navigator.clipboard.writeText(
                    `OPENAI_BASE_URL=${gatewayBaseURL}\nOPENAI_API_KEY=${created.secret}`,
                  )
                  setCopied(true)
                  setCopyFailed(false)
                } catch {
                  setCopyFailed(true)
                }
              }}
            >
              {copied ? '已复制' : '复制调用配置'}
            </Button>
          </div>
          {copyFailed ? (
            <div className="inline-problem" role="alert">
              浏览器未允许写入剪贴板。
            </div>
          ) : null}
          <dl className="fact-list">
            <div>
              <dt>Base URL</dt>
              <dd>
                <code>{gatewayBaseURL}</code>
              </dd>
            </div>
            <div>
              <dt>新前缀</dt>
              <dd>{created.key.prefix}</dd>
            </div>
            <div>
              <dt>原 Key 状态</dt>
              <dd>仍可用</dd>
            </div>
            <div>
              <dt>模型授权</dt>
              <dd>{created.key.authorizedModels.join(', ')}</dd>
            </div>
          </dl>
        </>
      ) : (
        <>
          <dl className="fact-list">
            <div>
              <dt>当前前缀</dt>
              <dd>{gatewayKey?.prefix}</dd>
            </div>
            <div>
              <dt>模型授权</dt>
              <dd>{gatewayKey?.authorizedModels.join(', ')}</dd>
            </div>
          </dl>
          <FormProblem error={mutation.error} />
        </>
      )}
    </DialogFrame>
  )
}
