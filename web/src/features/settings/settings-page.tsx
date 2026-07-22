import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Monitor, Moon, Save, Sun } from 'lucide-react'
import { useEffect } from 'react'
import { useForm } from 'react-hook-form'
import { z } from 'zod'

import { siteProfileApi } from '@/api'
import { siteProfileQuery } from '@/app/site-profile'
import { type ThemePreference, useThemePreference } from '@/app/theme'
import { useSession } from '@/app/session'
import { Page, PageHeader, PageSection } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { Field, Input, Textarea } from '@/components/ui/field'
import { FormProblem } from '@/features/auth/form-problem'

const schema = z.object({
  name: z.string().trim().min(2).max(80),
  description: z.string().trim().max(240),
  contact: z.string().trim().max(200),
  expectedVersion: z.number().int().positive(),
})

type Values = z.infer<typeof schema>

export function SettingsPage() {
  const session = useSession()
  const profile = useQuery(siteProfileQuery)
  const queryClient = useQueryClient()
  const [theme, setTheme] = useThemePreference()
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: { name: '', description: '', contact: '', expectedVersion: 1 },
  })
  useEffect(() => {
    if (profile.data) form.reset({ ...profile.data, expectedVersion: profile.data.version })
  }, [form, profile.data])
  const update = useMutation({
    mutationFn: siteProfileApi.update,
    onSuccess(value) {
      queryClient.setQueryData(siteProfileQuery.queryKey, value)
      form.reset({ ...value, expectedVersion: value.version })
    },
    async onError() {
      await queryClient.invalidateQueries({ queryKey: siteProfileQuery.queryKey })
    },
  })
  return (
    <Page>
      <PageHeader title="设置" />
      <PageSection title="外观">
        <ThemeSelector value={theme} onChange={setTheme} />
      </PageSection>
      {session.role === 'administrator' ? (
        <PageSection title="站点资料">
          <form
            className="settings-form"
            onSubmit={form.handleSubmit((value) => update.mutate(value))}
          >
            <Field label="站点名称" htmlFor="site-name" error={form.formState.errors.name?.message}>
              <Input id="site-name" autoComplete="organization" {...form.register('name')} />
            </Field>
            <Field
              label="联系信息"
              htmlFor="site-contact"
              error={form.formState.errors.contact?.message}
            >
              <Input id="site-contact" {...form.register('contact')} />
            </Field>
            <Field
              label="简短说明"
              htmlFor="site-description"
              error={form.formState.errors.description?.message}
              className="field--full"
            >
              <Textarea id="site-description" rows={3} {...form.register('description')} />
            </Field>
            <input type="hidden" {...form.register('expectedVersion', { valueAsNumber: true })} />
            <FormProblem error={profile.error ?? update.error} />
            <div className="settings-form__actions">
              <Button
                type="submit"
                icon={<Save size={16} />}
                disabled={profile.isLoading || update.isPending}
              >
                保存
              </Button>
            </div>
          </form>
        </PageSection>
      ) : null}
    </Page>
  )
}

function ThemeSelector({
  value,
  onChange,
}: {
  value: ThemePreference
  onChange: (value: ThemePreference) => void
}) {
  const options = [
    { value: 'system' as const, label: '跟随系统', icon: Monitor },
    { value: 'light' as const, label: '浅色', icon: Sun },
    { value: 'dark' as const, label: '深色', icon: Moon },
  ]
  return (
    <div className="segmented-control" role="radiogroup" aria-label="外观">
      {options.map((option) => {
        const Icon = option.icon
        return (
          <button
            key={option.value}
            type="button"
            role="radio"
            aria-checked={value === option.value}
            onClick={() => onChange(option.value)}
          >
            <Icon size={16} />
            <span>{option.label}</span>
          </button>
        )
      })}
    </div>
  )
}
