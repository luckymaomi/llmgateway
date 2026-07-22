import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { Check, Copy, ShieldCheck } from 'lucide-react'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { z } from 'zod'

import { authApi, type BootstrapResult } from '@/api'
import { establishAuthenticatedSession } from '@/app/session'
import { AuthPanel } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { Field, Input } from '@/components/ui/field'

import { FormProblem } from './form-problem'

const schema = z.object({ email: z.email('请输入有效邮箱') })
type FormValues = z.infer<typeof schema>
type CopyState = 'idle' | 'copied' | 'failed'

export function SetupPage() {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const [created, setCreated] = useState<BootstrapResult | null>(null)
  const [copyState, setCopyState] = useState<CopyState>('idle')
  const form = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: { email: '' },
  })
  const bootstrap = useMutation({
    mutationFn: authApi.bootstrap,
    onSuccess(result) {
      establishAuthenticatedSession(queryClient, result)
      setCreated(result)
      form.reset({ email: '' })
    },
  })

  async function copyPassword(): Promise<void> {
    if (!created) return
    try {
      if (!navigator.clipboard?.writeText) throw new Error('clipboard unavailable')
      await navigator.clipboard.writeText(created.initialPassword)
      setCopyState('copied')
    } catch {
      setCopyState('failed')
    }
  }

  if (created) {
    return (
      <AuthPanel title="管理员已创建">
        <div className="auth-marker">
          <ShieldCheck size={18} />
          立即保存到密码管理器，刷新或离开后无法再次查看
        </div>
        <div className="secret-reveal">
          <code data-testid="initial-administrator-password">{created.initialPassword}</code>
          <Button
            type="button"
            variant="secondary"
            icon={copyState === 'copied' ? <Check size={16} /> : <Copy size={16} />}
            onClick={() => void copyPassword()}
          >
            {copyState === 'copied' ? '已复制' : copyState === 'failed' ? '复制失败' : '复制'}
          </Button>
        </div>
        {copyState === 'failed' ? (
          <span className="field__error" role="alert">
            浏览器无法访问剪贴板，请手动保存密码。
          </span>
        ) : null}
        <Button type="button" onClick={() => void navigate({ to: '/', replace: true })}>
          我已保存，进入控制面
        </Button>
      </AuthPanel>
    )
  }

  return (
    <AuthPanel title="初始化 LLMGateway">
      <div className="auth-marker">
        <ShieldCheck size={18} />
        系统将生成高熵初始密码并只显示一次
      </div>
      <form
        className="form-stack"
        onSubmit={form.handleSubmit((values) => bootstrap.mutate({ email: values.email }))}
      >
        <Field
          label="管理员邮箱"
          htmlFor="setup-email"
          error={form.formState.errors.email?.message}
        >
          <Input id="setup-email" type="email" autoComplete="email" {...form.register('email')} />
        </Field>
        <FormProblem error={bootstrap.error} />
        <Button type="submit" disabled={bootstrap.isPending}>
          {bootstrap.isPending ? '正在创建' : '创建管理员'}
        </Button>
      </form>
    </AuthPanel>
  )
}
