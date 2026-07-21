import { useMutation } from '@tanstack/react-query'
import { useState } from 'react'

import { accessApi, type SessionRevocation, type UserAccount } from '@/api'
import { Button } from '@/components/ui/button'
import { DialogFrame } from '@/components/ui/dialog'
import { Field, Input } from '@/components/ui/field'
import { FormProblem } from '@/features/auth/form-problem'

interface MemberPasswordDialogProps {
  user: UserAccount | null
  onOpenChange: (open: boolean) => void
}

export function MemberPasswordDialog({ user, onOpenChange }: MemberPasswordDialogProps) {
  const [password, setPassword] = useState('')
  const [confirmation, setConfirmation] = useState('')
  const [idempotencyKey, setIdempotencyKey] = useState('')
  const [result, setResult] = useState<SessionRevocation | null>(null)
  const [validation, setValidation] = useState('')
  const mutation = useMutation({
    mutationFn: (input: { userId: string; password: string; idempotencyKey: string }) =>
      accessApi.resetMemberPassword(input.userId, input.password, input.idempotencyKey),
    onSuccess: (nextResult) => {
      setPassword('')
      setConfirmation('')
      setResult(nextResult)
    },
  })

  function close(): void {
    if (mutation.isPending) return
    mutation.reset()
    setPassword('')
    setConfirmation('')
    setIdempotencyKey('')
    setResult(null)
    setValidation('')
    onOpenChange(false)
  }

  async function submit(): Promise<void> {
    if (!user || mutation.isPending) return
    if (password.length < 12) {
      setValidation('密码至少需要 12 个字符')
      return
    }
    if (password !== confirmation) {
      setValidation('两次输入的密码不一致')
      return
    }
    setValidation('')
    const operationKey = idempotencyKey || crypto.randomUUID()
    setIdempotencyKey(operationKey)
    try {
      await mutation.mutateAsync({ userId: user.id, password, idempotencyKey: operationKey })
    } catch {
      // Keep the password and operation key in memory so an unknown outcome can be retried safely.
    }
  }

  return (
    <DialogFrame
      open={user !== null}
      onOpenChange={(open) => {
        if (!open) close()
      }}
      title={result ? '成员密码已重置' : '重置成员密码'}
      description={user?.displayName ?? ''}
      dismissible={!mutation.isPending}
      footer={
        result ? (
          <Button onClick={close}>完成</Button>
        ) : (
          <>
            <Button type="button" variant="secondary" disabled={mutation.isPending} onClick={close}>
              取消
            </Button>
            <Button type="button" disabled={mutation.isPending} onClick={() => void submit()}>
              {mutation.isPending ? '提交中' : mutation.isError ? '重试重置' : '确认重置'}
            </Button>
          </>
        )
      }
    >
      {result ? (
        <dl className="fact-list">
          <div>
            <dt>已撤销会话</dt>
            <dd>{result.revokedSessions}</dd>
          </div>
        </dl>
      ) : (
        <div className="form-grid">
          <Field label="新密码" htmlFor="member-new-password" className="field--full">
            <Input
              id="member-new-password"
              type="password"
              autoComplete="new-password"
              value={password}
              disabled={mutation.isPending}
              onChange={(event) => setPassword(event.target.value)}
            />
          </Field>
          <Field
            label="确认新密码"
            htmlFor="member-confirm-password"
            className="field--full"
            error={validation || undefined}
          >
            <Input
              id="member-confirm-password"
              type="password"
              autoComplete="new-password"
              value={confirmation}
              disabled={mutation.isPending}
              onChange={(event) => setConfirmation(event.target.value)}
            />
          </Field>
          <FormProblem error={mutation.error} />
        </div>
      )}
    </DialogFrame>
  )
}
