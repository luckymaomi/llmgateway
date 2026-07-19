import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { DatabaseBackup, Save } from 'lucide-react'
import { useState } from 'react'

import { settingsApi, type OperationSnapshot, type SettingsDocument } from '@/api'
import { Page, PageHeader, PageSection, SectionTabs } from '@/components/layout'
import { OperationPanel } from '@/components/operations/operation-panel'
import { Button } from '@/components/ui/button'
import { Field, Input, NativeSelect } from '@/components/ui/field'
import { ErrorState, LoadingState } from '@/components/ui/state'
import { FormProblem } from '@/features/auth/form-problem'
import { formatDateTime } from '@/lib/format'

type Section = SettingsDocument['section']
type SettingValue = string | number | boolean

interface SettingField {
  key: string
  label: string
  kind: 'text' | 'number' | 'boolean' | 'select'
  options?: Array<{ value: string; label: string }>
  unit?: string
}

const tabs = [
  { label: '安全', to: '/settings/security' },
  { label: '网络出口', to: '/settings/network' },
  { label: '可观测性', to: '/settings/observability' },
  { label: '备份恢复', to: '/settings/backups' },
  { label: '版本策略', to: '/settings/revisions' },
]

export function SettingsPage({ section }: { section: Section }) {
  const query = useQuery({
    queryKey: ['settings', section],
    queryFn: ({ signal }) => settingsApi.get(section, signal),
  })

  if (query.isLoading) return <LoadingState label="正在加载系统设置" />
  if (query.error || !query.data)
    return <ErrorState error={query.error} onRetry={() => void query.refetch()} />
  return <SettingsEditor key={query.data.revisionId} section={section} document={query.data} />
}

function SettingsEditor({ section, document }: { section: Section; document: SettingsDocument }) {
  const queryClient = useQueryClient()
  const [values, setValues] = useState<Record<string, SettingValue>>(document.values)
  const [operation, setOperation] = useState<OperationSnapshot | null>(null)
  const save = useMutation({
    mutationFn: () => settingsApi.update(section, document.revisionId, values),
    async onSuccess(updatedDocument) {
      queryClient.setQueryData(['settings', section], updatedDocument)
      await queryClient.invalidateQueries({ queryKey: ['settings', section] })
    },
  })
  const backup = useMutation({ mutationFn: settingsApi.runBackup, onSuccess: setOperation })

  const fields = sectionFields[section]
  return (
    <Page>
      <PageHeader
        title="系统设置"
        description="安全、网络、观测、备份和配置版本"
        actions={
          <Button icon={<Save size={16} />} disabled={save.isPending} onClick={() => save.mutate()}>
            {save.isPending ? '保存中' : '保存设置'}
          </Button>
        }
      />
      <SectionTabs tabs={tabs} />
      <div className="settings-meta">
        <span>
          Revision <code>{document.revisionId}</code>
        </span>
        <span>更新人 {document.updatedBy}</span>
        <span>{formatDateTime(document.updatedAt)}</span>
      </div>
      <PageSection title={sectionTitle[section]}>
        <div className="settings-form">
          {fields.map((field) => (
            <SettingControl
              key={field.key}
              field={field}
              value={values[field.key]}
              onChange={(value) => setValues((current) => ({ ...current, [field.key]: value }))}
            />
          ))}
        </div>
        <FormProblem error={save.error} />
        {section === 'backups' ? (
          <div className="settings-operation">
            <Button
              variant="secondary"
              icon={<DatabaseBackup size={16} />}
              disabled={backup.isPending}
              onClick={() => backup.mutate()}
            >
              立即备份
            </Button>
            {operation ? <OperationPanel initial={operation} /> : null}
            <FormProblem error={backup.error} />
          </div>
        ) : null}
      </PageSection>
    </Page>
  )
}

function SettingControl({
  field,
  value,
  onChange,
}: {
  field: SettingField
  value: SettingValue | undefined
  onChange: (value: SettingValue) => void
}) {
  const id = `setting-${field.key}`
  if (field.kind === 'boolean') {
    return (
      <label className="setting-toggle" htmlFor={id}>
        <span>
          <strong>{field.label}</strong>
          {field.unit ? <small>{field.unit}</small> : null}
        </span>
        <input
          id={id}
          type="checkbox"
          role="switch"
          checked={value === true}
          onChange={(event) => onChange(event.target.checked)}
        />
      </label>
    )
  }
  if (field.kind === 'select') {
    return (
      <Field label={field.label} htmlFor={id}>
        <NativeSelect
          id={id}
          value={String(value ?? '')}
          onChange={(event) => onChange(event.target.value)}
        >
          {field.options?.map((option) => (
            <option key={option.value} value={option.value}>
              {option.label}
            </option>
          ))}
        </NativeSelect>
      </Field>
    )
  }
  return (
    <Field label={field.label} htmlFor={id} hint={field.unit}>
      <Input
        id={id}
        type={field.kind}
        value={value === undefined ? '' : String(value)}
        onChange={(event) =>
          onChange(field.kind === 'number' ? Number(event.target.value) : event.target.value)
        }
      />
    </Field>
  )
}

const sectionTitle: Record<Section, string> = {
  security: '安全与留存',
  network: '网络与固定出口',
  observability: '日志、指标与告警',
  backups: '备份与恢复',
  revisions: '配置版本策略',
}

const sectionFields: Record<Section, SettingField[]> = {
  security: [
    { key: 'sessionLifetimeMinutes', label: '会话有效期', kind: 'number', unit: '分钟' },
    { key: 'loginAttemptsPerMinute', label: '登录频率上限', kind: 'number', unit: '次/分钟' },
    { key: 'contentRetentionDays', label: '受控内容保留期', kind: 'number', unit: '天' },
    { key: 'requireSecureCookies', label: '仅使用安全 Cookie', kind: 'boolean' },
  ],
  network: [
    { key: 'publicListenAddress', label: '公共监听地址', kind: 'text' },
    { key: 'trustedProxyCidrs', label: '可信反向代理 CIDR', kind: 'text' },
    { key: 'providerTimeoutSeconds', label: 'Provider 请求上限', kind: 'number', unit: '秒' },
    { key: 'allowPrivateUpstreams', label: '允许受控私网端点', kind: 'boolean' },
  ],
  observability: [
    {
      key: 'logLevel',
      label: '日志级别',
      kind: 'select',
      options: [
        { value: 'info', label: 'Info' },
        { value: 'warn', label: 'Warn' },
        { value: 'error', label: 'Error' },
      ],
    },
    { key: 'metricsEnabled', label: 'Prometheus 指标', kind: 'boolean' },
    { key: 'tracesEnabled', label: 'OpenTelemetry Trace', kind: 'boolean' },
    { key: 'alertWebhookUrl', label: '告警 Webhook', kind: 'text' },
  ],
  backups: [
    { key: 'backupDirectory', label: '备份目录', kind: 'text' },
    { key: 'retentionCopies', label: '保留副本', kind: 'number', unit: '份' },
    { key: 'scheduledEnabled', label: '启用计划备份', kind: 'boolean' },
  ],
  revisions: [
    { key: 'requireValidation', label: '发布前必须完整校验', kind: 'boolean' },
    { key: 'rollbackWindowMinutes', label: '回滚窗口', kind: 'number', unit: '分钟' },
    { key: 'maxDrafts', label: '最大草稿数', kind: 'number', unit: '份' },
  ],
}
