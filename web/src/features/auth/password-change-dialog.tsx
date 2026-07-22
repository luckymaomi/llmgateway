import { useMutation } from '@tanstack/react-query'
import { useState } from 'react'

import { authApi } from '@/api'
import { Button } from '@/components/ui/button'
import { DialogFrame } from '@/components/ui/dialog'
import { Field, Input } from '@/components/ui/field'

import { FormProblem } from './form-problem'

interface PasswordChangeDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function PasswordChangeDialog({ open, onOpenChange }: PasswordChangeDialogProps) {
  const [currentPassword, setCurrentPassword] = useState('')
  const [replacementPassword, setReplacementPassword] = useState('')
  const [confirmation, setConfirmation] = useState('')
  const [validation, setValidation] = useState('')
  const mutation = useMutation({
    mutationFn: () => authApi.changePassword(currentPassword, replacementPassword),
    onSuccess: () => {
      setCurrentPassword('')
      setReplacementPassword('')
      setConfirmation('')
    },
  })

  function close(): void {
    if (mutation.isPending) return
    mutation.reset()
    setCurrentPassword('')
    setReplacementPassword('')
    setConfirmation('')
    setValidation('')
    onOpenChange(false)
  }

  async function submit(): Promise<void> {
    if (replacementPassword.length < 12) {
      setValidation('新密码至少需要 12 个字符')
      return
    }
    if (replacementPassword !== confirmation) {
      setValidation('两次输入的新密码不一致')
      return
    }
    if (currentPassword === replacementPassword) {
      setValidation('新密码必须与当前密码不同')
      return
    }
    setValidation('')
    await mutation.mutateAsync()
  }

  return (
    <DialogFrame
      open={open}
      onOpenChange={(nextOpen) => !nextOpen && close()}
      title={mutation.isSuccess ? '密码已更换' : '更换密码'}
      description="当前设备保持登录，其他活动会话将被撤销"
      dismissible={!mutation.isPending}
      footer={
        mutation.isSuccess ? (
          <Button onClick={close}>完成</Button>
        ) : (
          <>
            <Button type="button" variant="secondary" disabled={mutation.isPending} onClick={close}>
              取消
            </Button>
            <Button type="button" disabled={mutation.isPending} onClick={() => void submit()}>
              {mutation.isPending ? '提交中' : '确认更换'}
            </Button>
          </>
        )
      }
    >
      {mutation.isSuccess ? (
        <dl className="fact-list">
          <div>
            <dt>已撤销其他会话</dt>
            <dd>{mutation.data.revokedSessions}</dd>
          </div>
        </dl>
      ) : (
        <div className="form-grid">
          <Field label="当前密码" htmlFor="current-password" className="field--full">
            <Input
              id="current-password"
              type="password"
              autoComplete="current-password"
              value={currentPassword}
              disabled={mutation.isPending}
              onChange={(event) => setCurrentPassword(event.target.value)}
            />
          </Field>
          <Field label="新密码" htmlFor="replacement-password" className="field--full">
            <Input
              id="replacement-password"
              type="password"
              autoComplete="new-password"
              value={replacementPassword}
              disabled={mutation.isPending}
              onChange={(event) => setReplacementPassword(event.target.value)}
            />
          </Field>
          <Field
            label="确认新密码"
            htmlFor="replacement-confirmation"
            className="field--full"
            error={validation || undefined}
          >
            <Input
              id="replacement-confirmation"
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
