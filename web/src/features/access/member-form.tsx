import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Check, Copy } from 'lucide-react'
import { useState, type FormEvent } from 'react'

import { accessApi, type CreatedMember, type UserAccount } from '@/api'
import { Button } from '@/components/ui/button'
import { DialogFrame } from '@/components/ui/dialog'
import { Field, Input } from '@/components/ui/field'
import { FormProblem } from '@/features/auth/form-problem'

export function MemberForm({
  member,
  open,
  onOpenChange,
}: {
  member: UserAccount | null
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const queryClient = useQueryClient()
  const [email, setEmail] = useState(member?.email ?? '')
  const [displayName, setDisplayName] = useState(member?.displayName ?? '')
  const [created, setCreated] = useState<CreatedMember>()
  const [copied, setCopied] = useState(false)

  const mutation = useMutation<CreatedMember | UserAccount>({
    mutationFn: async (): Promise<CreatedMember | UserAccount> =>
      member
        ? accessApi.updateMember(
            member.id,
            {
              email: email.trim(),
              displayName: displayName.trim(),
              expectedUpdatedAt: member.updatedAt,
            },
            crypto.randomUUID(),
          )
        : accessApi.createMember(
            { email: email.trim(), displayName: displayName.trim() },
            crypto.randomUUID(),
          ),
    async onSuccess(result) {
      await queryClient.invalidateQueries({ queryKey: ['members'] })
      if ('initialPassword' in result) setCreated(result)
      else onOpenChange(false)
    },
  })

  function close() {
    if (mutation.isPending) return
    setCreated(undefined)
    setCopied(false)
    mutation.reset()
    onOpenChange(false)
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!email.trim() || !displayName.trim()) return
    mutation.mutate()
  }

  return (
    <DialogFrame
      open={open}
      onOpenChange={(next) => !next && close()}
      title={created ? '成员已创建' : member ? '编辑成员' : '创建成员'}
      dismissible={!mutation.isPending}
      footer={
        created ? (
          <Button onClick={close}>完成</Button>
        ) : (
          <>
            <Button type="button" variant="secondary" disabled={mutation.isPending} onClick={close}>
              取消
            </Button>
            <Button type="submit" form="member-form" disabled={mutation.isPending}>
              {mutation.isPending ? '保存中' : '保存'}
            </Button>
          </>
        )
      }
    >
      {created ? (
        <div className="one-time-result">
          <dl className="fact-list">
            <div>
              <dt>成员</dt>
              <dd>{created.member.displayName}</dd>
            </div>
            <div>
              <dt>邮箱</dt>
              <dd>{created.member.email}</dd>
            </div>
          </dl>
          <div className="secret-reveal">
            <code>{created.initialPassword}</code>
            <Button
              variant="secondary"
              icon={copied ? <Check size={16} /> : <Copy size={16} />}
              onClick={() =>
                void navigator.clipboard
                  .writeText(created.initialPassword)
                  .then(() => setCopied(true))
              }
            >
              {copied ? '已复制' : '复制初始密码'}
            </Button>
          </div>
        </div>
      ) : (
        <form id="member-form" className="form-grid" onSubmit={submit}>
          <Field label="显示名称" htmlFor="member-name" className="field--full">
            <Input
              id="member-name"
              autoFocus
              value={displayName}
              readOnly={mutation.isPending}
              onChange={(event) => setDisplayName(event.target.value)}
            />
          </Field>
          <Field label="邮箱" htmlFor="member-email" className="field--full">
            <Input
              id="member-email"
              type="email"
              value={email}
              readOnly={mutation.isPending}
              onChange={(event) => setEmail(event.target.value)}
            />
          </Field>
          <FormProblem error={mutation.error} />
        </form>
      )}
    </DialogFrame>
  )
}
