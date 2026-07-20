import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { Tooltip } from 'radix-ui'
import { describe, expect, it, vi } from 'vitest'

import type { ProviderRecord } from '@/api'
import { server } from '@/test/server'

import { ProviderForm } from './provider-form'

describe('ProviderForm conflict recovery', () => {
  it('automatically rebases non-overlapping draft and latest changes', async () => {
    const user = userEvent.setup()
    const submitted: unknown[] = []
    const onOpenChange = vi.fn()
    let updateAttempts = 0
    server.use(
      http.put('/api/control/providers/:providerID', async ({ request }) => {
        submitted.push(await request.json())
        updateAttempts += 1
        if (updateAttempts === 1) return conflictResponse()
        return HttpResponse.json({
          data: {
            ...baseUrlLatestProvider,
            name: 'Operator Draft',
            updatedAt: '2026-07-20T10:02:00.000Z',
          },
        })
      }),
      http.get('/api/control/providers/:providerID', () =>
        HttpResponse.json({ data: baseUrlLatestProvider }),
      ),
    )
    renderProviderForm(disabledProvider, onOpenChange)
    const dialog = await screen.findByRole('dialog')

    await replaceValue(user, within(dialog).getByLabelText('名称'), 'Operator Draft')
    await user.click(within(dialog).getByRole('button', { name: '保存' }))

    await within(dialog).findByRole('heading', { name: '合并并发修改' })
    expect(within(dialog).getByLabelText('名称')).toHaveValue('Operator Draft')
    expect(within(dialog).getByLabelText('Base URL')).toHaveValue(baseUrlLatestProvider.baseUrl)
    expect(
      within(within(dialog).getByRole('group', { name: '名称' })).getByText(/保留你的草稿/),
    ).toBeInTheDocument()
    expect(
      within(within(dialog).getByRole('group', { name: 'Base URL' })).getByText(/采用当前最新值/),
    ).toBeInTheDocument()
    expect(within(dialog).queryByRole('radio')).not.toBeInTheDocument()

    await user.click(within(dialog).getByRole('button', { name: '保存合并结果' }))

    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false))
    expect(submitted).toEqual([
      {
        name: 'Operator Draft',
        kind: disabledProvider.kind,
        baseUrl: disabledProvider.baseUrl,
        expectedUpdatedAt: disabledProvider.updatedAt,
      },
      {
        name: 'Operator Draft',
        kind: baseUrlLatestProvider.kind,
        baseUrl: baseUrlLatestProvider.baseUrl,
        expectedUpdatedAt: baseUrlLatestProvider.updatedAt,
      },
    ])
  })

  it('requires an explicit decision when draft and latest changed the same field', async () => {
    const user = userEvent.setup()
    const submitted: unknown[] = []
    const onOpenChange = vi.fn()
    let updateAttempts = 0
    server.use(
      http.put('/api/control/providers/:providerID', async ({ request }) => {
        submitted.push(await request.json())
        updateAttempts += 1
        if (updateAttempts === 1) return conflictResponse()
        return HttpResponse.json({
          data: {
            ...disabledLatestProvider,
            updatedAt: '2026-07-20T10:02:00.000Z',
          },
        })
      }),
      http.get('/api/control/providers/:providerID', () =>
        HttpResponse.json({ data: disabledLatestProvider }),
      ),
    )
    renderProviderForm(disabledProvider, onOpenChange)
    const dialog = await screen.findByRole('dialog')

    await replaceValue(user, within(dialog).getByLabelText('名称'), 'Operator Draft')
    await user.click(within(dialog).getByRole('button', { name: '保存' }))
    await within(dialog).findByRole('heading', { name: '合并并发修改' })

    expect(updateAttempts).toBe(1)
    expect(within(dialog).getByLabelText('名称')).toHaveValue('Operator Draft')
    const nameConflict = within(dialog).getByRole('group', { name: '名称' })
    expect(within(nameConflict).getByText('Operator Draft')).toBeInTheDocument()
    expect(within(nameConflict).getByText(disabledLatestProvider.name)).toBeInTheDocument()
    expect(within(nameConflict).getByRole('radio', { name: '保留草稿' })).not.toBeChecked()
    expect(within(nameConflict).getByRole('radio', { name: '采用最新' })).not.toBeChecked()
    expect(within(dialog).getByRole('button', { name: '保存合并结果' })).toBeDisabled()

    await user.click(within(nameConflict).getByRole('radio', { name: '采用最新' }))

    expect(within(dialog).getByLabelText('名称')).toHaveValue(disabledLatestProvider.name)
    expect(within(dialog).getByRole('button', { name: '保存合并结果' })).toBeEnabled()
    await user.click(within(dialog).getByRole('button', { name: '保存合并结果' }))

    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false))
    expect(submitted).toEqual([
      {
        name: 'Operator Draft',
        kind: disabledProvider.kind,
        baseUrl: disabledProvider.baseUrl,
        expectedUpdatedAt: disabledProvider.updatedAt,
      },
      {
        name: disabledLatestProvider.name,
        kind: disabledProvider.kind,
        baseUrl: disabledProvider.baseUrl,
        expectedUpdatedAt: disabledLatestProvider.updatedAt,
      },
    ])
  })

  it('cannot retain a routing draft after the latest Provider was enabled', async () => {
    const user = userEvent.setup()
    const submitted: unknown[] = []
    const onOpenChange = vi.fn()
    let updateAttempts = 0
    server.use(
      http.put('/api/control/providers/:providerID', async ({ request }) => {
        submitted.push(await request.json())
        updateAttempts += 1
        if (updateAttempts === 1) return conflictResponse()
        return HttpResponse.json({
          data: {
            ...enabledLatestProvider,
            name: 'Operator Draft',
            updatedAt: '2026-07-20T10:02:00.000Z',
          },
        })
      }),
      http.get('/api/control/providers/:providerID', () =>
        HttpResponse.json({ data: enabledLatestProvider }),
      ),
    )
    renderProviderForm(disabledProvider, onOpenChange)
    const dialog = await screen.findByRole('dialog')

    await replaceValue(user, within(dialog).getByLabelText('名称'), 'Operator Draft')
    await user.selectOptions(within(dialog).getByLabelText('类型'), 'deepseek')
    await replaceValue(
      user,
      within(dialog).getByLabelText('Base URL'),
      'https://draft.example.test/v1',
    )
    await user.click(within(dialog).getByRole('button', { name: '保存' }))

    await within(dialog).findByRole('heading', { name: '合并并发修改' })
    expect(within(dialog).getByLabelText('名称')).toHaveValue('Operator Draft')
    expect(within(dialog).getByLabelText('类型')).toHaveValue(enabledLatestProvider.kind)
    expect(within(dialog).getByLabelText('类型')).toBeDisabled()
    expect(within(dialog).getByLabelText('Base URL')).toHaveValue(enabledLatestProvider.baseUrl)
    expect(within(dialog).getByLabelText('Base URL')).toHaveAttribute('readonly')
    expect(
      within(within(dialog).getByRole('group', { name: '类型' })).getByText(/路由字段/),
    ).toBeInTheDocument()
    expect(
      within(within(dialog).getByRole('group', { name: 'Base URL' })).getByText(/路由字段/),
    ).toBeInTheDocument()

    await user.click(within(dialog).getByRole('button', { name: '保存合并结果' }))

    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false))
    expect(submitted).toEqual([
      {
        name: 'Operator Draft',
        kind: 'deepseek',
        baseUrl: 'https://draft.example.test/v1',
        expectedUpdatedAt: disabledProvider.updatedAt,
      },
      {
        name: 'Operator Draft',
        kind: enabledLatestProvider.kind,
        baseUrl: enabledLatestProvider.baseUrl,
        expectedUpdatedAt: enabledLatestProvider.updatedAt,
      },
    ])
  })

  it('locks routing fields while allowing an enabled Provider rename', async () => {
    const user = userEvent.setup()
    let submitted: unknown
    const onOpenChange = vi.fn()
    server.use(
      http.put('/api/control/providers/:providerID', async ({ request }) => {
        submitted = await request.json()
        return HttpResponse.json({
          data: { ...enabledLatestProvider, name: 'Renamed Provider' },
        })
      }),
    )
    renderProviderForm(enabledLatestProvider, onOpenChange)
    const dialog = await screen.findByRole('dialog')

    expect(within(dialog).getByRole('combobox', { name: '类型' })).toBeDisabled()
    expect(within(dialog).getByLabelText('Base URL')).toHaveAttribute('readonly')
    expect(within(dialog).getByLabelText('名称')).toBeEnabled()
    await replaceValue(user, within(dialog).getByLabelText('名称'), 'Renamed Provider')
    await user.click(within(dialog).getByRole('button', { name: '保存' }))

    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false))
    expect(submitted).toEqual({
      name: 'Renamed Provider',
      kind: enabledLatestProvider.kind,
      baseUrl: enabledLatestProvider.baseUrl,
      expectedUpdatedAt: enabledLatestProvider.updatedAt,
    })
  })
})

function renderProviderForm(provider: ProviderRecord, onOpenChange = vi.fn()): void {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={queryClient}>
      <Tooltip.Provider>
        <ProviderForm open provider={provider} onOpenChange={onOpenChange} />
      </Tooltip.Provider>
    </QueryClientProvider>,
  )
}

async function replaceValue(
  user: ReturnType<typeof userEvent.setup>,
  field: HTMLElement,
  value: string,
): Promise<void> {
  await user.clear(field)
  await user.type(field, value)
}

function conflictResponse() {
  return HttpResponse.json(
    {
      error: {
        status: 409,
        code: 'conflict',
        message: 'Registry facts changed.',
        retryable: false,
        requestId: 'request-conflict',
      },
    },
    { status: 409 },
  )
}

const disabledProvider: ProviderRecord = {
  id: 'provider-1',
  slug: 'fixture-provider',
  name: 'Fixture Provider',
  kind: 'openai-compatible',
  baseUrl: 'https://provider.example.test/v1',
  status: 'disabled',
  updatedAt: '2026-07-20T10:00:00.000Z',
}

const disabledLatestProvider: ProviderRecord = {
  ...disabledProvider,
  name: 'Concurrent Rename',
  updatedAt: '2026-07-20T10:01:00.000Z',
}

const baseUrlLatestProvider: ProviderRecord = {
  ...disabledProvider,
  baseUrl: 'https://latest.example.test/v1',
  updatedAt: '2026-07-20T10:01:00.000Z',
}

const enabledLatestProvider: ProviderRecord = {
  ...disabledProvider,
  kind: 'zhipu',
  baseUrl: 'https://enabled.example.test/v1',
  status: 'enabled',
  updatedAt: '2026-07-20T10:01:00.000Z',
}
