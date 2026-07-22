import { expect, type Page } from '@playwright/test'

import {
  dataID,
  dataRecord,
  expectPageWidthToFit,
  isRecord,
  uuidPattern,
} from './acceptance-helpers'
import { acceptanceArtifactPath, type BrowserProblems } from './runtime'

export interface PublishedCatalogFacts {
  authorizedModelAlias: string
  authorizedModelID: string
  ungrantedModelAlias: string
  draftOnlyModelAlias: string
  revisionSequence: number
}

export async function completePublishedCatalog(
  page: Page,
  browserProblems: BrowserProblems,
  providerID: string,
): Promise<PublishedCatalogFacts> {
  const authorizedModelAlias = 'browser-chat'
  const ungrantedModelAlias = 'browser-batch'
  const draftOnlyModelAlias = 'browser-draft-only'
  await page.getByRole('link', { name: '模型', exact: true }).click()
  const authorizedModelID = await createModel(
    page,
    providerID,
    authorizedModelAlias,
    'fixture-chat',
    true,
  )
  const ungrantedModelID = await createModel(
    page,
    providerID,
    ungrantedModelAlias,
    'upstream-browser-batch',
  )

  await page.getByRole('link', { name: '用量与额度', exact: true }).click()
  await page.getByRole('link', { name: '上游成本', exact: true }).click()
  await page.getByRole('button', { name: '新增价格' }).click()
  const priceDialog = page.getByRole('dialog')
  await priceDialog.getByLabel('模型').selectOption(authorizedModelID)
  await priceDialog.getByLabel('输入价格 / 百万 Token').fill('1.5')
  await priceDialog.getByLabel('输出价格 / 百万 Token').fill('4')
  const priceResponsePromise = page.waitForResponse(
    (response) =>
      response.url().endsWith('/api/control/model-prices') &&
      response.request().method() === 'POST',
  )
  await priceDialog.getByRole('button', { name: '保存', exact: true }).click()
  expect((await priceResponsePromise).status()).toBe(201)

  const navigation = page.getByRole('complementary', { name: '主导航' })
  await navigation.getByRole('link', { name: 'Provider API Key' }).click()
  await page.getByRole('button', { name: '添加 API Key' }).click()
  const credentialDialog = page.getByRole('dialog')
  const credentialSecret = 'core-upstream-secret'
  await credentialDialog.getByLabel('所属 Provider').selectOption(providerID)
  await credentialDialog.getByLabel('名称').fill('Browser credential')
  await credentialDialog.getByLabel('Provider API Key').fill(credentialSecret)
  await credentialDialog.getByRole('checkbox', { name: authorizedModelAlias }).check()
  await credentialDialog.getByRole('checkbox', { name: ungrantedModelAlias }).check()
  await credentialDialog.getByLabel(`${authorizedModelAlias} 优先级`).fill('10')
  await credentialDialog.getByLabel(`${authorizedModelAlias} 权重`).fill('70')
  await credentialDialog.getByLabel(`${ungrantedModelAlias} 优先级`).fill('20')
  await credentialDialog.getByLabel(`${ungrantedModelAlias} 权重`).fill('30')
  await credentialDialog.getByLabel('RPM').fill('60')
  await credentialDialog.getByLabel('TPM').fill('100000')
  await credentialDialog.getByLabel('并发上限').fill('2')
  const credentialPath = '/api/control/credentials'
  let credentialID = ''
  let credentialResponseInterrupted = false
  await page.route('**' + credentialPath, async (route) => {
    const request = route.request()
    if (credentialResponseInterrupted || request.method() !== 'POST') {
      await route.continue()
      return
    }
    credentialResponseInterrupted = true
    const committed = await route.fetch()
    expect(committed.status()).toBe(201)
    credentialID = dataID(await committed.json()) ?? ''
    expect(credentialID).toMatch(uuidPattern)
    await route.abort('failed')
  })
  browserProblems.allowRequestFailure('POST', credentialPath, 'net::ERR_FAILED')
  try {
    const failedRequest = page.waitForEvent(
      'requestfailed',
      (request) =>
        new URL(request.url()).pathname === credentialPath && request.method() === 'POST',
    )
    await credentialDialog.getByRole('button', { name: '保存', exact: true }).click()
    const interruptedRequest = await failedRequest
    expect(interruptedRequest.headers()['idempotency-key']).toMatch(uuidPattern)
    await expect(credentialDialog.getByRole('alert')).toBeVisible()
    const storedOperation = await page.evaluate(() => {
      for (let index = 0; index < sessionStorage.length; index += 1) {
        const key = sessionStorage.key(index)
        if (key?.startsWith('llmgateway:pending-credential:')) return sessionStorage.getItem(key)
      }
      return null
    })
    expect(storedOperation).not.toBeNull()
    expect(storedOperation).not.toContain(credentialSecret)
    const storedOperationData = JSON.parse(storedOperation ?? '{}') as {
      label?: unknown
      modelBindings?: unknown
    }
    expect(storedOperationData.label).toBe('Browser credential')
    expect(storedOperationData.modelBindings).toEqual(
      expect.arrayContaining([
        { modelId: authorizedModelID, priority: 10, weight: 70 },
        { modelId: ungrantedModelID, priority: 20, weight: 30 },
      ]),
    )
    await page.reload()
    const reconciliation = page.getByRole('alert')
    await expect(reconciliation).toBeVisible()
    await expect(page.getByRole('table', { name: 'Provider API Key 列表' })).toContainText(
      'Browser credential',
    )
    await expect(page.getByText(credentialSecret)).toHaveCount(0)
    await reconciliation.getByRole('button', { name: '完成对账' }).click()
    const pendingMarkerCount = await page.evaluate(
      () =>
        Object.keys(sessionStorage).filter((key) =>
          key.startsWith('llmgateway:pending-credential:'),
        ).length,
    )
    expect(pendingMarkerCount).toBe(0)
  } finally {
    await page.unroute('**' + credentialPath)
  }

  const credentialRow = page
    .getByRole('table', { name: 'Provider API Key 列表' })
    .getByRole('row')
    .filter({ hasText: 'Browser credential' })
  const probePath = `${credentialPath}/${credentialID}/probe`
  const probeResponsePromise = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname === probePath && response.request().method() === 'POST',
  )
  await credentialRow.getByRole('button', { name: '测试连接' }).click()
  const probeDialog = page.getByRole('dialog', { name: '测试 Provider API Key' })
  await probeDialog.getByLabel('测试模型').selectOption(authorizedModelID)
  await probeDialog.getByRole('button', { name: '开始测试' }).click()
  const probeResponse = await probeResponsePromise
  expect(probeResponse.status()).toBe(200)
  expect(probeResponse.request().postDataJSON()).toEqual({ modelId: authorizedModelID })
  const probeResult = dataRecord(await probeResponse.json())
  if (!probeResult) throw new Error('Credential probe response did not contain a data record.')
  expect(probeResult).toEqual(
    expect.objectContaining({
      status: 'succeeded',
      mayUseTokens: true,
      modelId: authorizedModelID,
      modelName: authorizedModelAlias,
      responseText: 'fixture response',
      inputTokens: 4,
      outputTokens: 2,
    }),
  )
  expect(probeResult.requestId).toEqual(expect.stringMatching(/\S+/))
  await probeDialog.getByText('关闭', { exact: true }).click()
  await credentialRow.getByRole('button', { name: '编辑 API Key' }).click()
  const editDialog = page.getByRole('dialog')
  await editDialog.getByLabel('RPM').fill('75')
  await editDialog.getByLabel(`${authorizedModelAlias} 权重`).fill('80')
  const updateResponsePromise = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname === probePath.replace(/\/probe$/, '') &&
      response.request().method() === 'PUT',
  )
  await editDialog.getByRole('button', { name: '保存更新' }).click()
  const updateResponse = await updateResponsePromise
  expect(updateResponse.status()).toBe(200)
  expect(updateResponse.request().postDataJSON()).toMatchObject({ rpmLimit: 75 })

  const statusPath = probePath.replace(/\/probe$/, '/status')
  const disabledResponsePromise = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname === statusPath && response.request().method() === 'PUT',
  )
  await credentialRow.getByRole('button', { name: '停用 API Key' }).click()
  expect((await disabledResponsePromise).status()).toBe(200)
  const enabledResponsePromise = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname === statusPath && response.request().method() === 'PUT',
  )
  await credentialRow.getByRole('button', { name: '启用 API Key' }).click()
  expect((await enabledResponsePromise).status()).toBe(200)

  await navigation.getByRole('link', { name: 'Provider 接入' }).click()
  await page.getByRole('link', { name: '发布', exact: true }).click()
  const capturePath = '/api/control/configuration/revisions'
  let captureInterrupted = false
  let captureKey = ''
  let revisionID: string
  let sequence: number
  const revisionSummary = '1 Provider / 2 模型 / 1 凭据'
  await page.route('**' + capturePath, async (route) => {
    const request = route.request()
    if (captureInterrupted || request.method() !== 'POST') {
      await route.continue()
      return
    }
    captureInterrupted = true
    captureKey = request.headers()['idempotency-key'] ?? ''
    const committed = await route.fetch()
    expect(committed.status()).toBe(201)
    await route.abort('failed')
  })
  browserProblems.allowRequestFailure('POST', capturePath, 'net::ERR_FAILED')
  try {
    const failedCapture = page.waitForEvent(
      'requestfailed',
      (request) => new URL(request.url()).pathname === capturePath && request.method() === 'POST',
    )
    await page.getByRole('button', { name: '捕获当前配置' }).click()
    await failedCapture
    expect(captureKey).toMatch(uuidPattern)
    await expect(page.getByRole('alert')).toBeVisible()
    await page.reload()
    await expect(page.getByRole('alert')).toBeVisible()
    const replayResponse = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === capturePath && response.request().method() === 'POST',
    )
    await page.getByRole('button', { name: '重试原操作' }).click()
    const replayed = await replayResponse
    expect(replayed.status()).toBe(201)
    expect(replayed.request().headers()['idempotency-key']).toBe(captureKey)
    const captured = dataRecord(await replayed.json())
    revisionID = typeof captured?.id === 'string' ? captured.id : ''
    sequence = typeof captured?.sequence === 'number' ? captured.sequence : 0
    expect(revisionID).toMatch(uuidPattern)
    expect(sequence).toBeGreaterThan(0)
    expect(captured?.createdBy).toBe('Administrator')
    expect(captured?.summary).toBe(revisionSummary)
    expect(captured?.modelCount).toBe(2)
    expect(captured?.credentialCount).toBe(1)
    expect(captured?.routeCount).toBe(2)
    await page.getByRole('dialog').getByRole('button', { name: '关闭' }).click()
  } finally {
    await page.unroute('**' + capturePath)
  }

  const revisionRows = page.getByRole('row').filter({
    has: page.getByRole('cell', { name: String(sequence), exact: true }),
  })
  await expect(revisionRows).toHaveCount(1)
  const revisionRow = revisionRows.first()
  await expectPageWidthToFit(page)

  const publishPath = '/api/control/configuration/revisions/' + revisionID + '/publish'
  let interrupted = false
  let originalKey = ''
  let originalBody = ''
  await page.route('**' + publishPath, async (route) => {
    const request = route.request()
    if (interrupted || request.method() !== 'POST') {
      await route.continue()
      return
    }
    interrupted = true
    originalKey = request.headers()['idempotency-key'] ?? ''
    originalBody = request.postData() ?? ''
    const committed = await route.fetch()
    expect(committed.status()).toBe(200)
    await route.abort('failed')
  })
  browserProblems.allowRequestFailure('POST', publishPath, 'net::ERR_FAILED')
  try {
    const failedRequest = page.waitForEvent(
      'requestfailed',
      (request) => new URL(request.url()).pathname === publishPath && request.method() === 'POST',
    )
    await revisionRow.getByRole('button', { name: '发布', exact: true }).click()
    await failedRequest
    expect(originalKey).toMatch(uuidPattern)
    expect(JSON.parse(originalBody)).toEqual({ expectedActiveVersion: 0 })
    await expect(page.getByRole('alert')).toBeVisible()
    await page.reload()
    await expect(page.getByRole('alert')).toBeVisible()
    const replayResponse = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === publishPath && response.request().method() === 'POST',
    )
    await page.getByRole('button', { name: '重试原操作' }).click()
    const replayed = await replayResponse
    expect(replayed.status()).toBe(200)
    expect(replayed.request().headers()['idempotency-key']).toBe(originalKey)
    expect(replayed.request().postData()).toBe(originalBody)
    const published = dataRecord(await replayed.json())
    const publishedRevision = isRecord(published?.result) ? published.result : undefined
    expect(publishedRevision?.createdBy).toBe('Administrator')
    await page.getByRole('dialog').getByRole('button', { name: '关闭' }).click()
  } finally {
    await page.unroute('**' + publishPath)
  }

  await page.getByRole('link', { name: '模型', exact: true }).click()
  await createModel(page, providerID, draftOnlyModelAlias, 'upstream-browser-draft-only')
  const activeResponse = await page.request.get('/api/control/configuration/active')
  expect(activeResponse.status()).toBe(200)
  const active = dataRecord(await activeResponse.json())
  const activeModels = Array.isArray(active?.models) ? active.models : []
  const activeAliases = activeModels
    .map((model) => (isRecord(model) && typeof model.alias === 'string' ? model.alias : ''))
    .filter(Boolean)
  expect(activeAliases.sort()).toEqual([authorizedModelAlias, ungrantedModelAlias].sort())
  await page.screenshot({
    path: acceptanceArtifactPath('catalog-desktop.png'),
    fullPage: true,
    animations: 'disabled',
  })
  return {
    authorizedModelAlias,
    authorizedModelID,
    ungrantedModelAlias,
    draftOnlyModelAlias,
    revisionSequence: sequence,
  }
}

async function createModel(
  page: Page,
  providerID: string,
  alias: string,
  upstreamModelID: string,
  reasoning = false,
): Promise<string> {
  await page.getByRole('button', { name: '添加模型' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByLabel('Provider', { exact: true }).selectOption(providerID)
  await dialog.getByLabel('网关别名').fill(alias)
  await dialog.getByLabel('上游模型 ID').fill(upstreamModelID)
  await dialog.getByLabel('上下文 Token').fill('8192')
  if (reasoning) {
    await dialog.getByRole('checkbox', { name: '推理内容' }).check()
    await dialog.getByLabel('推理控制').selectOption('toggle')
  }
  const responsePromise = page.waitForResponse(
    (response) =>
      response.url().endsWith('/api/control/models') && response.request().method() === 'POST',
  )
  await dialog.getByRole('button', { name: '保存', exact: true }).click()
  const response = await responsePromise
  expect(response.status()).toBe(201)
  const responseBody: unknown = await response.json()
  const modelID = dataID(responseBody) ?? ''
  expect(modelID).toMatch(uuidPattern)
  const csrfToken = (await page.context().cookies()).find(
    (cookie) => cookie.name === 'llmgateway_csrf',
  )?.value
  expect(csrfToken).toMatch(/^[A-Za-z0-9_-]+$/)
  const priceResponse = await page.request.post('/api/control/model-prices', {
    data: {
      modelId: modelID,
      currency: 'USD',
      inputPricePerMillionTokens: '0',
      outputPricePerMillionTokens: '0',
      effectiveAt: new Date(Date.now() - 60_000).toISOString(),
    },
    headers: { 'Idempotency-Key': crypto.randomUUID(), 'X-CSRF-Token': csrfToken ?? '' },
  })
  expect(priceResponse.status()).toBe(201)
  if (reasoning) {
    expect(dataRecord(responseBody)?.reasoningMode).toBe('toggle')
  }
  return modelID
}
