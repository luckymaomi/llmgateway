import { expect, type Page } from '@playwright/test'

import {
  dataID,
  dataRecord,
  expectLocatorWidthToFit,
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
  revisionSummary: string
}

export async function completePublishedCatalog(
  page: Page,
  browserProblems: BrowserProblems,
): Promise<PublishedCatalogFacts> {
  const authorizedModelAlias = 'browser-chat'
  const ungrantedModelAlias = 'browser-batch'
  const draftOnlyModelAlias = 'browser-draft-only'
  await page.getByRole('link', { name: '模型', exact: true }).click()
  const authorizedModelID = await createModel(page, authorizedModelAlias, 'fixture-chat')
  const ungrantedModelID = await createModel(page, ungrantedModelAlias, 'upstream-browser-batch')
  await expect(page.getByRole('table', { name: '模型列表' })).toContainText(authorizedModelAlias)
  await expect(page.getByRole('table', { name: '模型列表' })).toContainText(ungrantedModelAlias)

  const navigation = page.getByRole('complementary', { name: '主导航' })
  await navigation.getByRole('link', { name: '上游凭据池' }).click()
  await page.getByRole('button', { name: '添加凭据' }).click()
  const credentialDialog = page.getByRole('dialog')
  const credentialSecret = 'core-upstream-secret'
  await credentialDialog.getByLabel('Provider').selectOption({ label: 'Browser Provider Mobile' })
  await credentialDialog.getByLabel('名称').fill('Browser credential')
  await credentialDialog.getByLabel('API Key / 凭据').fill(credentialSecret)
  await credentialDialog.getByRole('checkbox', { name: authorizedModelAlias }).check()
  await credentialDialog.getByRole('checkbox', { name: ungrantedModelAlias }).check()
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
    await expect(credentialDialog.getByRole('alert')).toContainText('结果暂时无法确认')
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
      authorizedModelIds?: unknown
    }
    expect(storedOperationData.label).toBe('Browser credential')
    expect(Array.isArray(storedOperationData.authorizedModelIds)).toBe(true)
    expect([...(storedOperationData.authorizedModelIds as string[])].sort()).toEqual(
      [authorizedModelID, ungrantedModelID].sort(),
    )
    await page.reload()
    const reconciliation = page.getByRole('alert')
    await expect(reconciliation).toContainText('已在持久列表中确认上次创建结果。')
    await expect(page.getByRole('table', { name: '上游凭据列表' })).toContainText(
      'Browser credential',
    )
    await expect(page.getByText(credentialSecret)).toHaveCount(0)
    await reconciliation.getByRole('button', { name: '完成对账' }).click()
    await expect(reconciliation).toBeHidden()
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
    .getByRole('table', { name: '上游凭据列表' })
    .getByRole('row')
    .filter({ hasText: 'Browser credential' })
  await expect(credentialRow.getByRole('button', { name: '测试连接' })).toBeVisible()
  const probePath = `${credentialPath}/${credentialID}/probe`
  const probeResponsePromise = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname === probePath && response.request().method() === 'POST',
  )
  await credentialRow.getByRole('button', { name: '测试连接' }).click()
  const probeResponse = await probeResponsePromise
  expect(probeResponse.status()).toBe(200)
  const probeResult = dataRecord(await probeResponse.json())
  if (!probeResult) throw new Error('Credential probe response did not contain a data record.')
  expect(probeResult).toEqual(expect.objectContaining({ status: 'succeeded', mayUseTokens: false }))
  expect(probeResult.requestId).toEqual(expect.stringMatching(/\S+/))
  const probePanel = page.getByRole('region', { name: '凭据连接测试' })
  await expect(probePanel).toContainText('未消耗模型 Token')
  await expect(probePanel).toContainText(String(probeResult.requestId))

  await credentialRow.getByRole('button', { name: '编辑凭据' }).click()
  const editDialog = page.getByRole('dialog')
  await editDialog.getByLabel('RPM').fill('75')
  const updateResponsePromise = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname === probePath.replace(/\/probe$/, '') &&
      response.request().method() === 'PUT',
  )
  await editDialog.getByRole('button', { name: '保存更新' }).click()
  expect((await updateResponsePromise).status()).toBe(200)
  await expect(editDialog).toBeHidden()
  await expect(credentialRow).toContainText('75')

  const statusPath = probePath.replace(/\/probe$/, '/status')
  const disabledResponsePromise = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname === statusPath && response.request().method() === 'PUT',
  )
  await credentialRow.getByRole('button', { name: '停用凭据' }).click()
  expect((await disabledResponsePromise).status()).toBe(200)
  await expect(credentialRow).toContainText('已停用')
  const enabledResponsePromise = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname === statusPath && response.request().method() === 'PUT',
  )
  await credentialRow.getByRole('button', { name: '启用凭据' }).click()
  expect((await enabledResponsePromise).status()).toBe(200)
  await expect(credentialRow).toContainText('可用')

  await navigation.getByRole('link', { name: 'Provider 与模型' }).click()
  await page.getByRole('link', { name: '配置版本', exact: true }).click()
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
    await expect(page.getByRole('alert')).toContainText('操作结果暂时无法确认')
    await page.reload()
    await expect(page.getByRole('alert')).toContainText('操作结果暂时无法确认')
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
    expect(captured?.createdBy).toBe('Browser Administrator')
    expect(captured?.summary).toBe(revisionSummary)
    expect(captured?.modelCount).toBe(2)
    expect(captured?.credentialCount).toBe(1)
    expect(captured?.routeCount).toBe(2)
    await expect(page.getByRole('region', { name: '操作结果' })).toContainText(
      '已捕获配置版本 ' + String(sequence),
    )
    await page.getByRole('dialog').getByRole('button', { name: '关闭' }).click()
  } finally {
    await page.unroute('**' + capturePath)
  }

  const revisionRows = page.getByRole('row').filter({
    has: page.getByRole('cell', { name: String(sequence), exact: true }),
  })
  await expect(revisionRows).toHaveCount(1)
  const revisionRow = revisionRows.first()
  await expect(revisionRow.getByRole('cell', { name: revisionSummary, exact: true })).toBeVisible()
  const creatorCell = revisionRow.getByRole('cell', { name: 'Browser Administrator', exact: true })
  await expect(creatorCell).toBeVisible()
  await expectLocatorWidthToFit(creatorCell)
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
    await expect(page.getByRole('alert')).toContainText('操作结果暂时无法确认')
    await page.reload()
    await expect(page.getByRole('alert')).toContainText('操作结果暂时无法确认')
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
    expect(publishedRevision?.createdBy).toBe('Browser Administrator')
    await expect(page.getByLabel('当前生效配置')).toContainText('版本 ' + String(sequence))
    await page.getByRole('dialog').getByRole('button', { name: '关闭' }).click()
  } finally {
    await page.unroute('**' + publishPath)
  }

  await page.getByRole('link', { name: '模型', exact: true }).click()
  await createModel(page, draftOnlyModelAlias, 'upstream-browser-draft-only')
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
    revisionSummary,
  }
}

async function createModel(page: Page, alias: string, upstreamModelID: string): Promise<string> {
  await page.getByRole('button', { name: '添加模型' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByLabel('Provider').selectOption({ label: 'Browser Provider Mobile' })
  await dialog.getByLabel('网关别名').fill(alias)
  await dialog.getByLabel('上游模型 ID').fill(upstreamModelID)
  await dialog.getByLabel('上下文 Token').fill('8192')
  const responsePromise = page.waitForResponse(
    (response) =>
      response.url().endsWith('/api/control/models') && response.request().method() === 'POST',
  )
  await dialog.getByRole('button', { name: '保存', exact: true }).click()
  const response = await responsePromise
  expect(response.status()).toBe(201)
  const modelID = dataID(await response.json()) ?? ''
  expect(modelID).toMatch(uuidPattern)
  return modelID
}
